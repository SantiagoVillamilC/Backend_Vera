package auth

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	platformerrors "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/errors"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMapearErrorDeConsulta(t *testing.T) {
	otro := errors.New("otro error")
	casos := map[string]struct {
		err  error
		want error
	}{
		"correo repetido": {
			err:  &pgconn.PgError{Code: pgerrcode.UniqueViolation, ConstraintName: "credenciales_correo_uk"},
			want: ErrIdentificadorEnUso,
		},
		"celular repetido": {
			err:  &pgconn.PgError{Code: pgerrcode.UniqueViolation, ConstraintName: "credenciales_celular_uk"},
			want: ErrIdentificadorEnUso,
		},
		"documento repetido": {
			err:  &pgconn.PgError{Code: pgerrcode.UniqueViolation, ConstraintName: "personas_documento_uk"},
			want: ErrIdentificadorEnUso,
		},
		"nit repetido": {
			err:  &pgconn.PgError{Code: pgerrcode.UniqueViolation, ConstraintName: "empresas_nit_uk"},
			want: ErrIdentificadorEnUso,
		},
		"error no relacionado": {
			err:  otro,
			want: otro,
		},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			got := MapearErrorDeConsulta(caso.err)
			if !errors.Is(got, caso.want) {
				t.Fatalf("se esperaba %v, se obtuvo %v", caso.want, got)
			}
		})
	}
}

func TestMapearErrorDeConsultaSinDetail(t *testing.T) {
	original := &pgconn.PgError{Code: "08006", Detail: "Key (email)=(a@b.com) already exists."}
	got := MapearErrorDeConsulta(original)
	var pgErr *pgconn.PgError
	if !errors.As(got, &pgErr) {
		t.Fatalf("quiero un *pgconn.PgError, obtuve %T", got)
	}
	if pgErr.Detail != "" {
		t.Fatalf("Detail = %q, quiero vacío para que no llegue al log del 5xx", pgErr.Detail)
	}
	if original.Detail == "" {
		t.Fatal("el error original se mutó: conserva su Detail")
	}
}

func TestMapearError(t *testing.T) {
	casos := map[string]struct {
		err    error
		code   string
		status int
	}{
		"sin identidad es 401": {err: ErrSinIdentidad, code: "Unauthorized", status: 401},
		"correo en uso es 409": {err: ErrIdentificadorEnUso, code: "DuplicateIdentifier", status: 409},
		"auth no disponible es 503": {
			err:    ConFailClosed(errors.New("dial tcp 127.0.0.1:1: connect: connection refused")),
			code:   "AuthUnavailable",
			status: 503,
		},
		"desafio invalido es 400 ChallengeInvalid": {err: ErrDesafioInvalido, code: "ChallengeInvalid", status: 400},
		"codigo invalido es 400 OtpInvalid":        {err: ErrCodigoInvalido, code: "OtpInvalid", status: 400},
		"codigo vencido es 400 OtpExpired":         {err: ErrCodigoExpirado, code: "OtpExpired", status: 400},
		"demasiados intentos es 429":               {err: ErrDemasiadosIntentos, code: "OtpTooManyAttempts", status: 429},
		"demasiados envios es 429 TooManyRequests": {err: ErrDemasiadosEnvios, code: "TooManyRequests", status: 429},
		"credenciales invalidas es 401":            {err: ErrCredencialesInvalidas, code: "InvalidCredentials", status: 401},
		"cuenta suspendida es 403":                 {err: ErrCuentaSuspendida, code: "AccountSuspended", status: 403},
		"sin rol de empresa es 403":                {err: ErrSinRolDeEmpresa, code: "NoCompanyRole", status: 403},
		"segundo factor invalido es 401":           {err: ErrSegundoFactorRequerido, code: "SecondFactorRequired", status: 401},
		"reset invalido es 400":                    {err: ErrResetInvalido, code: "ResetInvalid", status: 400},
		"cambio de numero en enfriamiento es 429":  {err: ErrCambioNumeroEnEnfriamiento, code: "PhoneChangeCooldown", status: 429},
		"celular nuevo igual es 422":               {err: ErrCelularNuevoIgual, code: "ValidationError", status: 422},
		"whatsapp unico canal es 409":              {err: ErrWhatsappUnicoCanal, code: "WhatsappUnicoCanal", status: 409},
		"version de consentimiento inexistente es 422": {
			err: ErrVersionDeConsentimientoInexistente, code: "ValidationError", status: 422,
		},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			got := MapearError(caso.err)
			var apiErr *platformerrors.APIError
			if !errors.As(got, &apiErr) {
				t.Fatalf("quiero un *platformerrors.APIError, obtuve %T", got)
			}
			if apiErr.Code != caso.code || apiErr.Status != caso.status {
				t.Fatalf("código/status = %s/%d, quiero %s/%d", apiErr.Code, apiErr.Status, caso.code, caso.status)
			}
		})
	}
}

// TestTodoErrorExportadoTieneMapeo obliga a que cada var Err... del paquete esté referenciada en MapearError.
func TestTodoErrorExportadoTieneMapeo(t *testing.T) {
	fset := token.NewFileSet()
	entradas, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("no se pudo leer el paquete: %v", err)
	}

	exportados := map[string]bool{}
	var archivoErrores *ast.File
	for _, entrada := range entradas {
		nombre := entrada.Name()
		if entrada.IsDir() || !strings.HasSuffix(nombre, ".go") || strings.HasSuffix(nombre, "_test.go") {
			continue
		}
		archivo, err := parser.ParseFile(fset, nombre, nil, 0)
		if err != nil {
			t.Fatalf("no se pudo parsear %s: %v", nombre, err)
		}
		if nombre == "errores.go" {
			archivoErrores = archivo
		}
		for _, decl := range archivo.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				for _, id := range spec.(*ast.ValueSpec).Names {
					if id.IsExported() && strings.HasPrefix(id.Name, "Err") {
						exportados[id.Name] = true
					}
				}
			}
		}
	}
	if len(exportados) == 0 {
		t.Fatal("no se encontró ninguna var Err... exportada en el paquete")
	}
	if archivoErrores == nil {
		t.Fatal("no se encontró internal/auth/errores.go para revisar el catálogo")
	}

	mapeados := map[string]bool{}
	for _, decl := range archivoErrores.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "MapearError" || fd.Body == nil {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			llamada, ok := n.(*ast.CallExpr)
			if !ok || len(llamada.Args) != 2 {
				return true
			}
			selector, ok := llamada.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Is" {
				return true
			}
			id, ok := llamada.Args[1].(*ast.Ident)
			if ok && strings.HasPrefix(id.Name, "Err") {
				mapeados[id.Name] = true
			}
			return true
		})
	}
	if len(mapeados) == 0 {
		t.Fatal("no se encontró ninguna rama errors.Is(err, Err...) en MapearError")
	}

	for nombre := range exportados {
		if !mapeados[nombre] {
			t.Errorf("el error exportado %s no está mapeado en MapearError", nombre)
		}
	}
}

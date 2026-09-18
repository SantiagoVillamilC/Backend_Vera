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
		"violación de unicidad": {
			err:  &pgconn.PgError{Code: pgerrcode.UniqueViolation, ConstraintName: "users_email_key"},
			want: ErrEmailEnUso,
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
		"correo en uso es 409": {err: ErrEmailEnUso, code: "DuplicateIdentifier", status: 409},
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

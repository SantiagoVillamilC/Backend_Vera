package auth

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// tokensDePrueba arma el firmador de los tests con emisor y audiencia fijos.
func tokensDePrueba(t *testing.T) *TokensDeAcceso {
	t.Helper()
	tokens, err := NuevoTokensDeAcceso([]byte(strings.Repeat("s", 32)), "centinela-auth", "centinela-api")
	if err != nil {
		t.Fatalf("NuevoTokensDeAcceso: %v", err)
	}
	return tokens
}

func TestAccessFirmaYVerificaConTodosLosClaims(t *testing.T) {
	tokens := tokensDePrueba(t)
	parte, sesion := uuid.New(), uuid.New()

	firmado, err := tokens.Firmar(ClaimsAccess{
		ParteID:   parte,
		SesionID:  sesion,
		Contexto:  ContextoDeEmpresa(uuid.New()),
		Rol:       "representante_legal",
		EmitidoEn: time.Now().UTC().Truncate(time.Second),
	})
	if err != nil {
		t.Fatalf("Firmar: %v", err)
	}
	if firmado == "" || strings.Count(firmado, ".") != 2 {
		t.Fatalf("token = %q, quiero tres segmentos separados por punto", firmado)
	}

	claims, err := tokens.Verificar(firmado)
	if err != nil {
		t.Fatalf("Verificar: %v", err)
	}
	if claims.ParteID != parte || claims.SesionID != sesion {
		t.Errorf("parte/sesion = %s/%s, quiero %s/%s", claims.ParteID, claims.SesionID, parte, sesion)
	}
	if claims.Rol != "representante_legal" || !strings.HasPrefix(claims.Contexto, "empresa:") {
		t.Errorf("rol/contexto = %q/%q", claims.Rol, claims.Contexto)
	}
	if claims.JTI == uuid.Nil || claims.EmitidoEn.IsZero() || claims.ExpiraEn.IsZero() {
		t.Errorf("claims incompletos: jti=%s iat=%s exp=%s", claims.JTI, claims.EmitidoEn, claims.ExpiraEn)
	}
	if got := claims.ExpiraEn.Sub(claims.EmitidoEn); got != duracionAccess {
		t.Errorf("vida del access = %s, quiero %s", got, duracionAccess)
	}
}

func TestAccessRechazaVencido(t *testing.T) {
	tokens := tokensDePrueba(t)
	ahora := time.Now().UTC().Add(-2 * duracionAccess)
	firmado, err := tokens.Firmar(ClaimsAccess{
		ParteID:   uuid.New(),
		SesionID:  uuid.New(),
		Contexto:  ContextoUsuario,
		EmitidoEn: ahora,
	})
	if err != nil {
		t.Fatalf("Firmar: %v", err)
	}

	if _, err := tokens.Verificar(firmado); !errors.Is(err, ErrAccessInvalido) {
		t.Fatalf("Verificar = %v, quiero ErrAccessInvalido", err)
	}
}

func TestAccessRechazaAlterado(t *testing.T) {
	tokens := tokensDePrueba(t)
	firmado, err := tokens.Firmar(ClaimsAccess{ParteID: uuid.New(), SesionID: uuid.New(), Contexto: ContextoUsuario})
	if err != nil {
		t.Fatalf("Firmar: %v", err)
	}

	partes := strings.Split(firmado, ".")
	cambiado := partes[1]
	if cambiado[0] == 'A' {
		cambiado = "B" + cambiado[1:]
	} else {
		cambiado = "A" + cambiado[1:]
	}
	alterado := strings.Join([]string{partes[0], cambiado, partes[2]}, ".")

	if _, err := tokens.Verificar(alterado); !errors.Is(err, ErrAccessInvalido) {
		t.Fatalf("Verificar alterado = %v, quiero ErrAccessInvalido", err)
	}
}

func TestAccessRechazaOtraClave(t *testing.T) {
	otro, err := NuevoTokensDeAcceso([]byte(strings.Repeat("o", 32)), "centinela-auth", "centinela-api")
	if err != nil {
		t.Fatalf("NuevoTokensDeAcceso: %v", err)
	}
	firmado, err := otro.Firmar(ClaimsAccess{ParteID: uuid.New(), SesionID: uuid.New(), Contexto: ContextoUsuario})
	if err != nil {
		t.Fatalf("Firmar: %v", err)
	}

	if _, err := tokensDePrueba(t).Verificar(firmado); !errors.Is(err, ErrAccessInvalido) {
		t.Fatalf("Verificar con otra clave = %v, quiero ErrAccessInvalido", err)
	}
}

func TestAccessRechazaAlgNone(t *testing.T) {
	codificar := func(raw string) string { return base64.RawURLEncoding.EncodeToString([]byte(raw)) }
	sinFirma := codificar(`{"alg":"none","typ":"JWT"}`) + "." + codificar(`{"sub":"`+uuid.NewString()+`"}`) + "."

	if _, err := tokensDePrueba(t).Verificar(sinFirma); !errors.Is(err, ErrAccessInvalido) {
		t.Fatalf("Verificar alg none = %v, quiero ErrAccessInvalido", err)
	}
}

func TestAccessRechazaEmisorOAudienciaAjenos(t *testing.T) {
	casos := map[string]struct{ emisor, audiencia string }{
		"emisor ajeno":    {emisor: "otro", audiencia: "centinela-api"},
		"audiencia ajena": {emisor: "centinela-auth", audiencia: "otra"},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			otro, err := NuevoTokensDeAcceso([]byte(strings.Repeat("s", 32)), caso.emisor, caso.audiencia)
			if err != nil {
				t.Fatalf("NuevoTokensDeAcceso: %v", err)
			}
			firmado, err := otro.Firmar(ClaimsAccess{ParteID: uuid.New(), SesionID: uuid.New(), Contexto: ContextoUsuario})
			if err != nil {
				t.Fatalf("Firmar: %v", err)
			}
			if _, err := tokensDePrueba(t).Verificar(firmado); !errors.Is(err, ErrAccessInvalido) {
				t.Fatalf("Verificar = %v, quiero ErrAccessInvalido", err)
			}
		})
	}
}

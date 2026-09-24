package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const personaValida = `{"tipo_cuenta":"persona","correo":"ana@example.com","contrasena":"secret12",` +
	`"celular":"+573001234567","nombre_completo":"Ana","tipo_documento":"CC","numero_documento":"1",` +
	`"consentimientos":[{"tipo":"tratamiento_datos","version_texto":"v1","aceptado":true}]}`

const empresaValida = `{"tipo_cuenta":"empresa","razon_social":"ACME","nit":"9001234568",` +
	`"correo":"acme@example.com","contrasena":"secret12","celular":"+573001234567",` +
	`"representante_legal":{"nombre_completo":"Ana","tipo_documento":"CC","numero_documento":"1"},` +
	`"consentimientos":[{"tipo":"tratamiento_datos","version_texto":"v1","aceptado":true}]}`

func peticion(t *testing.T, cuerpo string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/auth/register", strings.NewReader(cuerpo))
	return httptest.NewRecorder(), req
}

func detallesDe(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var env struct {
		Error struct {
			Details []map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("la respuesta no es JSON: %v (cuerpo %q)", err, rec.Body.String())
	}
	return env.Error.Details
}

func TestDecodeAndValidateNombraCamposAnidadosEnSnakeCase(t *testing.T) {
	cuerpo := strings.Replace(empresaValida, `"nombre_completo":"Ana"`, `"nombre_completo":""`, 1)
	rec, req := peticion(t, cuerpo)
	var dst RegisterEmpresaRequest

	if DecodeAndValidate(rec, req, &dst) {
		t.Fatal("quiero false cuando el representante legal no trae nombre")
	}
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, quiero 422 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	detalles := detallesDe(t, rec)
	if len(detalles) != 1 {
		t.Fatalf("details = %v, quiero uno", detalles)
	}
	if detalles[0]["field"] != "representante_legal.nombre_completo" {
		t.Fatalf("field = %v, quiero representante_legal.nombre_completo", detalles[0]["field"])
	}
}

func TestDecodeAndValidateNombraCamposRaizEnSnakeCase(t *testing.T) {
	cuerpo := strings.Replace(empresaValida, `"nit":"9001234568"`, `"nit":""`, 1)
	rec, req := peticion(t, cuerpo)
	var dst RegisterEmpresaRequest

	if DecodeAndValidate(rec, req, &dst) {
		t.Fatal("quiero false cuando el nit falta")
	}
	detalles := detallesDe(t, rec)
	if len(detalles) != 1 || detalles[0]["field"] != "nit" || detalles[0]["rule"] != "required" {
		t.Fatalf("details = %v, quiero nit/required", detalles)
	}
}

func TestValidarNIT(t *testing.T) {
	casos := map[string]bool{
		"9001234568":  true,
		"900123456-8": true,
		"9001234569":  false,
		"abc":         false,
		"":            false,
	}
	for valor, valido := range casos {
		t.Run(valor, func(t *testing.T) {
			if err := validate.Var(valor, "nit"); (err == nil) != valido {
				t.Fatalf("validate.Var(%q, nit) err = %v, quiero válido = %v", valor, err, valido)
			}
		})
	}
}

func TestValidarE164(t *testing.T) {
	casos := map[string]bool{
		"+573001234567": true,
		"573001234567":  false,
		"+57":           true,
		"+":             false,
		"+57a":          false,
	}
	for valor, valido := range casos {
		t.Run(valor, func(t *testing.T) {
			if err := validate.Var(valor, "e164"); (err == nil) != valido {
				t.Fatalf("validate.Var(%q, e164) err = %v, quiero válido = %v", valor, err, valido)
			}
		})
	}
}

func TestDecodeDiscriminated(t *testing.T) {
	destinos := func() map[string]any {
		return map[string]any{"persona": &RegisterPersonaRequest{}, "empresa": &RegisterEmpresaRequest{}}
	}

	t.Run("campo extra invalida el cuerpo", func(t *testing.T) {
		cuerpo := strings.Replace(personaValida, `"tipo_documento"`, `"campo_extra":true,"tipo_documento"`, 1)
		rec, req := peticion(t, cuerpo)

		if _, ok := DecodeDiscriminated(rec, req, "tipo_cuenta", destinos()); ok {
			t.Fatal("quiero false cuando el cuerpo trae un campo desconocido")
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, quiero 400 (cuerpo %q)", rec.Code, rec.Body.String())
		}
	})

	t.Run("sin discriminador responde 422", func(t *testing.T) {
		cuerpo := strings.Replace(personaValida, `"tipo_cuenta":"persona",`, "", 1)
		rec, req := peticion(t, cuerpo)

		if _, ok := DecodeDiscriminated(rec, req, "tipo_cuenta", destinos()); ok {
			t.Fatal("quiero false cuando falta el discriminador")
		}
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, quiero 422 (cuerpo %q)", rec.Code, rec.Body.String())
		}
		detalles := detallesDe(t, rec)
		if len(detalles) != 1 || detalles[0]["field"] != "tipo_cuenta" || detalles[0]["rule"] != "oneof" {
			t.Fatalf("details = %v, quiero tipo_cuenta/oneof", detalles)
		}
		if param, _ := detalles[0]["param"].(string); param != "persona empresa" {
			t.Fatalf("param = %q, quiero %q", param, "persona empresa")
		}
	})

	t.Run("discriminador válido decodifica", func(t *testing.T) {
		rec, req := peticion(t, personaValida)

		got, ok := DecodeDiscriminated(rec, req, "tipo_cuenta", destinos())
		if !ok {
			t.Fatalf("quiero true, obtuve status %d (cuerpo %q)", rec.Code, rec.Body.String())
		}
		persona, ok := got.(*RegisterPersonaRequest)
		if !ok {
			t.Fatalf("tipo = %T, quiero *RegisterPersonaRequest", got)
		}
		if persona.NombreCompleto != "Ana" {
			t.Fatalf("nombre_completo = %q, quiero Ana", persona.NombreCompleto)
		}
	})
}

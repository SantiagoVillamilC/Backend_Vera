package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
)

// rutaDeTransferencia monta el router real con el usuario de prueba y un servicio sin base para los rechazos de validación.
func rutaDeTransferencia() http.Handler {
	return NewRouter(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{Identidad: auth.UsuarioDePrueba{}})
}

// campoDelPrimerDetalle devuelve el campo del primer error de validación del envelope.
func campoDelPrimerDetalle(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	env := decodeEnvelope(t, rec)
	apiErr, ok := env["error"].(map[string]any)
	if !ok {
		t.Fatalf("error ausente en %q", rec.Body.String())
	}
	detalles, ok := apiErr["details"].([]any)
	if !ok || len(detalles) == 0 {
		t.Fatalf("details ausente en %q", rec.Body.String())
	}
	primero, ok := detalles[0].(map[string]any)
	if !ok {
		t.Fatalf("details con forma rara en %q", rec.Body.String())
	}
	campo, _ := primero["field"].(string)
	return campo
}

func peticionDeTransferencia(cuerpo string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(http.MethodPost, "/v1/transferencias", strings.NewReader(cuerpo))
	req.Header.Set(auth.CabeceraUsuarioDePrueba, "1")
	return req, httptest.NewRecorder()
}

// HU-NF-06 Criterio 1: sin destino o sin monto el rechazo nombra el campo obligatorio que falta.
func TestHU_NF_06_Criterio1_EnvioSinDestinoOSinMonto(t *testing.T) {
	const destino = "00000000-0000-4000-8000-000000000002"
	casos := map[string]struct {
		cuerpo string
		campo  string
	}{
		"sin destino": {`{"monto":"1000","idempotency_key":"k-1"}`, "destino"},
		"sin monto":   {`{"destino":"` + destino + `","idempotency_key":"k-1"}`, "monto"},
	}

	for nombre, tc := range casos {
		t.Run(nombre, func(t *testing.T) {
			req, rec := peticionDeTransferencia(tc.cuerpo)
			rutaDeTransferencia().ServeHTTP(rec, req)
			assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
			if campo := campoDelPrimerDetalle(t, rec); campo != tc.campo {
				t.Errorf("campo faltante = %q, quiero %q", campo, tc.campo)
			}
		})
	}
}

// HU-NF-06 Criterio 2: un monto que no es positivo se rechaza como inválido.
func TestHU_NF_06_Criterio2_EnvioConMontoNoPositivo(t *testing.T) {
	const destino = "00000000-0000-4000-8000-000000000002"
	casos := map[string]string{
		"cero":     `{"destino":"` + destino + `","monto":"0","idempotency_key":"k-1"}`,
		"negativo": `{"destino":"` + destino + `","monto":"-5","idempotency_key":"k-1"}`,
	}

	for nombre, cuerpo := range casos {
		t.Run(nombre, func(t *testing.T) {
			req, rec := peticionDeTransferencia(cuerpo)
			rutaDeTransferencia().ServeHTTP(rec, req)
			assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
			if campo := campoDelPrimerDetalle(t, rec); campo != "monto" {
				t.Errorf("campo inválido = %q, quiero %q", campo, "monto")
			}
		})
	}
}

// HU-NF-06 comparte con HU-NF-10 (criterio 3 de esa historia) el rechazo de una moneda distinta de COP.
func TestHU_NF_06_MonedaDistintaDeCOPSeRechaza(t *testing.T) {
	const destino = "00000000-0000-4000-8000-000000000002"
	cuerpo := `{"destino":"` + destino + `","monto":"1000","moneda":"USD","idempotency_key":"k-1"}`

	req, rec := peticionDeTransferencia(cuerpo)
	rutaDeTransferencia().ServeHTTP(rec, req)
	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
	if campo := campoDelPrimerDetalle(t, rec); campo != "moneda" {
		t.Errorf("campo inválido = %q, quiero %q", campo, "moneda")
	}
}

package httpserver

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
)

// tokenCustodioDePrueba es el token con el que el router de prueba autentica al custodio.
const tokenCustodioDePrueba = "token-de-prueba-del-custodio-0123456789"

// autenticadorDePrueba compara el token presentado contra uno fijo.
type autenticadorDePrueba struct{ token string }

// TokenValido reporta si el token presentado es el del custodio de prueba.
func (a autenticadorDePrueba) TokenValido(presentado string) bool { return presentado == a.token }

var custodioDePrueba = autenticadorDePrueba{token: tokenCustodioDePrueba}

func routerDeConfirmacion() http.Handler {
	return NewRouter(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{Identidad: auth.UsuarioDePrueba{}})
}

func pedirConfirmacion(t *testing.T, r http.Handler, cuerpo, cabecera string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/v1/depositos/confirmaciones", strings.NewReader(cuerpo))
	if cabecera != "" {
		req.Header.Set("Authorization", cabecera)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func detalleSenalaCampo(t *testing.T, rec *httptest.ResponseRecorder, campo string) bool {
	t.Helper()

	env := decodeEnvelope(t, rec)
	apiErr, _ := env["error"].(map[string]any)
	detalles, _ := apiErr["details"].([]any)
	for _, d := range detalles {
		if m, ok := d.(map[string]any); ok && m["field"] == campo {
			return true
		}
	}
	return false
}

// Un router sin autenticador del custodio es un error de programación: falla al construir.
func TestNewRouterSinAutenticadorDelCustodioFalla(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("NewRouter sin autenticador del custodio no falló")
		}
		err, ok := r.(error)
		if !ok || !errors.Is(err, ErrCustodioNulo) {
			t.Fatalf("NewRouter falló con %v, quiero %v", r, ErrCustodioNulo)
		}
	}()

	NewRouter(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), nil, DependenciasAuth{Identidad: auth.UsuarioDePrueba{}})
}

// HU-NF-07 Criterio 5: solo el custodio simulado confirma; cualquier otra procedencia da 401.
func TestHU_NF_07_Criterio5_SoloElCustodioConfirma(t *testing.T) {
	r := routerDeConfirmacion()
	cuerpo := `{"referencia":"dep-x","estado":"aprobado","monto":"1000","ocurrido_en":"2026-09-16T10:00:00Z"}`

	cabeceras := map[string]string{
		"sin cabecera":       "",
		"token ajeno":        "Bearer otro-token-que-no-es-el-del-custodio",
		"esquema equivocado": "Basic " + tokenCustodioDePrueba,
		"bearer vacio":       "Bearer ",
	}
	for name, cabecera := range cabeceras {
		t.Run(name, func(t *testing.T) {
			rec := pedirConfirmacion(t, r, cuerpo, cabecera)

			assertErrorEnvelope(t, rec, http.StatusUnauthorized, "Unauthorized")
			if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
				t.Errorf("WWW-Authenticate = %q, quiero Bearer", got)
			}
		})
	}
}

// HU-NF-07 Criterio 1: sin referencia o sin un monto positivo la confirmación no pasa.
func TestHU_NF_07_Criterio1_ReferenciaYMontoObligatorios(t *testing.T) {
	tests := map[string]struct {
		cuerpo string
		campo  string
	}{
		"sin referencia": {cuerpo: `{"estado":"aprobado","monto":"1000","ocurrido_en":"2026-09-16T10:00:00Z"}`, campo: "referencia"},
		"sin monto":      {cuerpo: `{"referencia":"dep-x","estado":"aprobado","ocurrido_en":"2026-09-16T10:00:00Z"}`, campo: "monto"},
		"monto cero":     {cuerpo: `{"referencia":"dep-x","estado":"aprobado","monto":"0","ocurrido_en":"2026-09-16T10:00:00Z"}`, campo: "monto"},
		"monto negativo": {cuerpo: `{"referencia":"dep-x","estado":"aprobado","monto":"-5","ocurrido_en":"2026-09-16T10:00:00Z"}`, campo: "monto"},
	}

	r := routerDeConfirmacion()
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			rec := pedirConfirmacion(t, r, tc.cuerpo, "Bearer "+tokenCustodioDePrueba)

			assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
			if !detalleSenalaCampo(t, rec, tc.campo) {
				t.Errorf("details no señala %s: %s", tc.campo, rec.Body.String())
			}
		})
	}
}

// HU-NF-07 Criterio 17: el estado es obligatorio y solo aprobado o rechazado pasan la validación.
func TestHU_NF_07_Criterio17_EstadoObligatorioYValido(t *testing.T) {
	tests := map[string]string{
		"estado ausente":  `{"referencia":"dep-x","monto":"1000","ocurrido_en":"2026-09-16T10:00:00Z"}`,
		"estado distinto": `{"referencia":"dep-x","estado":"pendiente","monto":"1000","ocurrido_en":"2026-09-16T10:00:00Z"}`,
		"estado femenino": `{"referencia":"dep-x","estado":"rechazada","monto":"1000","ocurrido_en":"2026-09-16T10:00:00Z"}`,
	}

	r := routerDeConfirmacion()
	for name, cuerpo := range tests {
		t.Run(name, func(t *testing.T) {
			rec := pedirConfirmacion(t, r, cuerpo, "Bearer "+tokenCustodioDePrueba)

			assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
			if !detalleSenalaCampo(t, rec, "estado") {
				t.Errorf("details no señala estado: %s", rec.Body.String())
			}
		})
	}
}

// HU-NF-07 Criterio 11: la marca de la fuente es obligatoria en la aprobación para no mentir en la traza.
func TestHU_NF_07_Criterio11_OcurridoEnObligatorio(t *testing.T) {
	r := routerDeConfirmacion()
	rec := pedirConfirmacion(t, r,
		`{"referencia":"dep-x","estado":"aprobado","monto":"1000"}`,
		"Bearer "+tokenCustodioDePrueba)

	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
	if !detalleSenalaCampo(t, rec, "ocurrido_en") {
		t.Errorf("details no señala ocurrido_en: %s", rec.Body.String())
	}
}

// Un cuerpo con un campo desconocido o mal formado es 400, nunca una confirmación a medias.
func TestConfirmacionConCuerpoInvalidoDa400(t *testing.T) {
	tests := map[string]string{
		"campo desconocido": `{"referencia":"dep-x","estado":"aprobado","monto":"1000","ocurrido_en":"2026-09-16T10:00:00Z","campo_ajeno":"x"}`,
		"json mal formado":  `{"referencia":`,
		"monto json numero": `{"referencia":"dep-x","estado":"aprobado","monto":1000,"ocurrido_en":"2026-09-16T10:00:00Z"}`,
	}

	r := routerDeConfirmacion()
	for name, cuerpo := range tests {
		t.Run(name, func(t *testing.T) {
			rec := pedirConfirmacion(t, r, cuerpo, "Bearer "+tokenCustodioDePrueba)

			assertErrorEnvelope(t, rec, http.StatusBadRequest, "BadRequest")
		})
	}
}

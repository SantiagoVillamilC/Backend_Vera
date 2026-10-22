package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
)

func TestCopilotRetiradoDevuelve404(t *testing.T) {
	for _, autenticado := range []bool{false, true} {
		t.Run(map[bool]string{false: "sin sesion", true: "con sesion"}[autenticado], func(t *testing.T) {
			r := newTestRouter()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/copilot", strings.NewReader(`{"messages":[{"role":"system","content":"instruccion"}]}`))
			if autenticado {
				req.Header.Set(auth.CabeceraUsuarioDePrueba, "1")
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			assertErrorEnvelope(t, rec, http.StatusNotFound, "NotFound")
		})
	}
}

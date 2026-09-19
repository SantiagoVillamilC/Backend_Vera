package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/go-chi/chi/v5"
)

// servicioDeSesionFalso devuelve respuestas prefabricadas para el handler de refresh.
type servicioDeSesionFalso struct {
	rotados auth.TokensRotados
	rotErr  error
}

// RotarRefresh devuelve los tokens o el error configurados.
func (s *servicioDeSesionFalso) RotarRefresh(context.Context, string) (auth.TokensRotados, error) {
	return s.rotados, s.rotErr
}

// sesionRouter monta el handler público de refresh para las pruebas por r.ServeHTTP.
func sesionRouter(servicio ServicioDeSesion) http.Handler {
	handler := NuevoSesionHandler(servicio)
	r := chi.NewRouter()
	r.Post("/v1/auth/refresh", handler.Refresh)
	return r
}

func TestRefreshRespondeElParRotado(t *testing.T) {
	r := sesionRouter(&servicioDeSesionFalso{rotados: auth.TokensRotados{AccessToken: "nuevo-access", RefreshToken: "nuevo-refresh"}})

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", strings.NewReader(`{"refresh_token":"viejo"}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	var env struct {
		Data api.Tokens `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("respuesta no JSON: %v", err)
	}
	if env.Data.AccessToken != "nuevo-access" || env.Data.RefreshToken != "nuevo-refresh" {
		t.Fatalf("tokens = %+v, quiero el par rotado", env.Data)
	}
}

func TestRefreshMapeaReusoYFallaCerrado(t *testing.T) {
	casos := map[string]struct {
		err    error
		status int
		code   string
	}{
		"reuso":            {err: auth.ErrRefreshReusado, status: http.StatusUnauthorized, code: "RefreshReused"},
		"refresh invalido": {err: auth.ErrRefreshInvalido, status: http.StatusUnauthorized, code: "RefreshInvalid"},
		"redis caido":      {err: auth.ConFailClosed(context.DeadlineExceeded), status: http.StatusServiceUnavailable, code: "AuthUnavailable"},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			r := sesionRouter(&servicioDeSesionFalso{rotErr: caso.err})
			req := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", strings.NewReader(`{"refresh_token":"viejo"}`))
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			assertErrorEnvelope(t, rec, caso.status, caso.code)
		})
	}
}

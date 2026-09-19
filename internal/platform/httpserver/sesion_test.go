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
	"github.com/google/uuid"
)

// servicioDeSesionFalso devuelve respuestas prefabricadas para los handlers.
type servicioDeSesionFalso struct {
	emitida  auth.SesionEmitida
	rotados  auth.TokensRotados
	verifErr error
	rotErr   error
}

// VerificarOtp devuelve la sesión o el error configurados.
func (s *servicioDeSesionFalso) VerificarOtp(context.Context, uuid.UUID, string, string) (auth.SesionEmitida, error) {
	return s.emitida, s.verifErr
}

// RotarRefresh devuelve los tokens o el error configurados.
func (s *servicioDeSesionFalso) RotarRefresh(context.Context, string) (auth.TokensRotados, error) {
	return s.rotados, s.rotErr
}

// sesionRouter monta los dos handlers públicos para las pruebas por r.ServeHTTP.
func sesionRouter(servicio ServicioDeSesion) http.Handler {
	handler := NuevoSesionHandler(servicio)
	r := chi.NewRouter()
	r.Post("/v1/auth/login/verify-otp", handler.VerifyOtp)
	r.Post("/v1/auth/refresh", handler.Refresh)
	return r
}

func TestVerifyOtpRespondeElParYElUsuario(t *testing.T) {
	parte := uuid.New()
	servicio := &servicioDeSesionFalso{emitida: auth.SesionEmitida{
		AccessToken:  "access",
		RefreshToken: "refresh",
		Usuario: auth.DatosUsuario{
			ID:             parte,
			TipoCuenta:     "persona",
			Correo:         "persona@centinela.test",
			NombreCompleto: "Ana Pérez",
			ContextoActivo: "usuario",
		},
	}}
	r := sesionRouter(servicio)

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login/verify-otp",
		strings.NewReader(`{"challenge_id":"`+uuid.NewString()+`","codigo":"123456"}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	var env struct {
		Data api.Sesion `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("respuesta no JSON: %v", err)
	}
	if env.Data.AccessToken != "access" || env.Data.RefreshToken != "refresh" {
		t.Fatalf("par = %+v, quiero access/refresh", env.Data)
	}
	if env.Data.Usuario.Id != parte || env.Data.Usuario.TipoCuenta != api.UsuarioTipoCuentaPersona {
		t.Errorf("usuario = %+v, quiero la persona %s", env.Data.Usuario, parte)
	}
	if env.Data.Usuario.RolActivo != nil {
		t.Errorf("rol_activo = %v, quiero null en contexto usuario", *env.Data.Usuario.RolActivo)
	}
	if env.Data.Usuario.NombreCompleto == nil || *env.Data.Usuario.NombreCompleto != "Ana Pérez" {
		t.Errorf("nombre_completo = %v, quiero Ana Pérez", env.Data.Usuario.NombreCompleto)
	}
}

func TestVerifyOtpMapeaElErrorDeAuth(t *testing.T) {
	r := sesionRouter(&servicioDeSesionFalso{verifErr: auth.ErrCodigoInvalido})

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login/verify-otp",
		strings.NewReader(`{"challenge_id":"`+uuid.NewString()+`","codigo":"123456"}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusBadRequest, "OtpInvalid")
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

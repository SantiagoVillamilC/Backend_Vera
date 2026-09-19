package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// servicioDeDispositivosFalso devuelve respuestas prefabricadas para los seis handlers.
type servicioDeDispositivosFalso struct {
	sesiones       []auth.Sesion
	contextos      []auth.ContextoDeParte
	activo         auth.ContextoActivo
	listaErr       error
	cerrarErr      error
	cerrarTodasErr error
	revocarErr     error
	contextosErr   error
	activoErr      error
}

// ListarDispositivos devuelve las sesiones o el error configurados.
func (s *servicioDeDispositivosFalso) ListarDispositivos(context.Context, uuid.UUID) ([]auth.Sesion, error) {
	return s.sesiones, s.listaErr
}

// CerrarSesionActual devuelve el error configurado.
func (s *servicioDeDispositivosFalso) CerrarSesionActual(context.Context, uuid.UUID, uuid.UUID) error {
	return s.cerrarErr
}

// CerrarTodasLasSesiones devuelve el error configurado.
func (s *servicioDeDispositivosFalso) CerrarTodasLasSesiones(context.Context, uuid.UUID) error {
	return s.cerrarTodasErr
}

// RevocarDispositivo devuelve el error configurado.
func (s *servicioDeDispositivosFalso) RevocarDispositivo(context.Context, uuid.UUID, uuid.UUID) error {
	return s.revocarErr
}

// ListarContextos devuelve los contextos o el error configurados.
func (s *servicioDeDispositivosFalso) ListarContextos(context.Context, uuid.UUID) ([]auth.ContextoDeParte, error) {
	return s.contextos, s.contextosErr
}

// ActivarContexto devuelve el contexto activo o el error configurados.
func (s *servicioDeDispositivosFalso) ActivarContexto(context.Context, uuid.UUID, uuid.UUID, string) (auth.ContextoActivo, error) {
	return s.activo, s.activoErr
}

// routerDeDispositivos monta el servidorV1 dentro del grupo autenticado con el servicio falso.
func routerDeDispositivos(servicio ControlDeSesiones) http.Handler {
	return NewRouter(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{
		Identidad: auth.UsuarioDePrueba{},
		Auth:      NuevoDispositivosHandler(servicio),
	})
}

// pedirConCredencial lanza la petición con la cabecera de la identidad de prueba.
func pedirConCredencial(r http.Handler, metodo, ruta, cuerpo string) *httptest.ResponseRecorder {
	var req *http.Request
	if cuerpo == "" {
		req = httptest.NewRequest(metodo, ruta, nil)
	} else {
		req = httptest.NewRequest(metodo, ruta, strings.NewReader(cuerpo))
	}
	req.Header.Set(auth.CabeceraUsuarioDePrueba, "1")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestDispositivosSinCredencialResponde401(t *testing.T) {
	r := routerDeDispositivos(&servicioDeDispositivosFalso{})
	rutas := []struct{ metodo, ruta string }{
		{http.MethodPost, "/v1/auth/logout"},
		{http.MethodPost, "/v1/auth/logout-all"},
		{http.MethodGet, "/v1/auth/sessions"},
		{http.MethodDelete, "/v1/auth/sessions/" + uuid.NewString()},
		{http.MethodGet, "/v1/auth/contextos"},
		{http.MethodPost, "/v1/auth/contexto"},
	}
	for _, ruta := range rutas {
		t.Run(ruta.metodo+" "+ruta.ruta, func(t *testing.T) {
			req := httptest.NewRequest(ruta.metodo, ruta.ruta, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			assertErrorEnvelope(t, rec, http.StatusUnauthorized, "Unauthorized")
		})
	}
}

func TestSessionsListaVaciaRespondeArregloVacio(t *testing.T) {
	r := routerDeDispositivos(&servicioDeDispositivosFalso{sesiones: []auth.Sesion{}})

	rec := pedirConCredencial(r, http.MethodGet, "/v1/auth/sessions", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	if cuerpo := rec.Body.String(); !strings.Contains(cuerpo, `"data":[]`) {
		t.Fatalf("cuerpo = %q, quiero data como arreglo vacío, nunca null", cuerpo)
	}
}

func TestSessionsMapeaElDispositivoYSuFamilia(t *testing.T) {
	familia := uuid.New()
	creada := time.Now().UTC().Add(-time.Hour)
	servicio := &servicioDeDispositivosFalso{sesiones: []auth.Sesion{{
		ID:             uuid.New(),
		FamiliaID:      familia,
		Dispositivo:    "Navegador",
		ContextoActivo: auth.ContextoUsuario,
		CreadaEn:       creada,
		UltimoUsoEn:    creada.Add(time.Minute),
	}}}
	r := routerDeDispositivos(servicio)

	rec := pedirConCredencial(r, http.MethodGet, "/v1/auth/sessions", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	var env struct {
		Data []api.Dispositivo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("respuesta no JSON: %v", err)
	}
	if len(env.Data) != 1 || env.Data[0].Id != openapi_types.UUID(familia) || env.Data[0].Dispositivo != "Navegador" {
		t.Fatalf("dispositivos = %+v, quiero la familia %s", env.Data, familia)
	}
}

func TestDeleteSessionMalformadoResponde400ConDetails(t *testing.T) {
	r := routerDeDispositivos(&servicioDeDispositivosFalso{})

	rec := pedirConCredencial(r, http.MethodDelete, "/v1/auth/sessions/no-es-uuid", "")

	assertErrorEnvelope(t, rec, http.StatusBadRequest, "BadRequest")
	env := decodeEnvelope(t, rec)
	apiErr, _ := env["error"].(map[string]any)
	details, ok := apiErr["details"].([]any)
	if !ok || len(details) == 0 {
		t.Fatalf("quiero details con session_id, obtuve %q", rec.Body.String())
	}
	primer, _ := details[0].(map[string]any)
	if primer["field"] != "session_id" || primer["rule"] != "format" {
		t.Fatalf("detail = %v, quiero session_id/format", primer)
	}
}

func TestDeleteSessionAjenaResponde404(t *testing.T) {
	r := routerDeDispositivos(&servicioDeDispositivosFalso{revocarErr: auth.ErrSesionNoEncontrada})

	rec := pedirConCredencial(r, http.MethodDelete, "/v1/auth/sessions/"+uuid.NewString(), "")

	assertErrorEnvelope(t, rec, http.StatusNotFound, "SessionNotFound")
}

func TestDeleteSessionResponde204(t *testing.T) {
	r := routerDeDispositivos(&servicioDeDispositivosFalso{})

	rec := pedirConCredencial(r, http.MethodDelete, "/v1/auth/sessions/"+uuid.NewString(), "")

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, quiero 204 (cuerpo %q)", rec.Code, rec.Body.String())
	}
}

func TestSetContextoInvalidoResponde400(t *testing.T) {
	r := routerDeDispositivos(&servicioDeDispositivosFalso{activoErr: auth.ErrContextoInvalido})

	rec := pedirConCredencial(r, http.MethodPost, "/v1/auth/contexto", `{"contexto_id":"empresa:no-existe"}`)

	assertErrorEnvelope(t, rec, http.StatusBadRequest, "ContextInvalid")
}

func TestSetContextoRespondeAccessYContexto(t *testing.T) {
	empresa := uuid.New()
	servicio := &servicioDeDispositivosFalso{activo: auth.ContextoActivo{
		AccessToken: "access-nuevo",
		Contexto:    auth.ContextoDeParte{ID: auth.ContextoDeEmpresa(empresa), Tipo: auth.ContextoEmpresa, Nombre: "Empresa S.A.S.", NIT: "900123456", Rol: "representante_legal"},
	}}
	r := routerDeDispositivos(servicio)

	rec := pedirConCredencial(r, http.MethodPost, "/v1/auth/contexto", `{"contexto_id":"`+auth.ContextoDeEmpresa(empresa)+`"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	var env struct {
		Data api.ContextoActivo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("respuesta no JSON: %v", err)
	}
	if env.Data.AccessToken != "access-nuevo" || env.Data.Contexto.ContextoId != auth.ContextoDeEmpresa(empresa) {
		t.Fatalf("data = %+v, quiero el access con el contexto de empresa", env.Data)
	}
	if env.Data.Contexto.Rol == nil || *env.Data.Contexto.Rol != "representante_legal" {
		t.Fatalf("rol = %v, quiero representante_legal", env.Data.Contexto.Rol)
	}
}

func TestListContextosUsaRolNuloEnElPersonal(t *testing.T) {
	servicio := &servicioDeDispositivosFalso{contextos: []auth.ContextoDeParte{
		{ID: auth.ContextoUsuario, Tipo: auth.ContextoUsuario, Nombre: "Ana Pérez"},
	}}
	r := routerDeDispositivos(servicio)

	rec := pedirConCredencial(r, http.MethodGet, "/v1/auth/contextos", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	if cuerpo := rec.Body.String(); !strings.Contains(cuerpo, `"rol":null`) {
		t.Fatalf("cuerpo = %q, quiero rol null en el contexto personal", cuerpo)
	}
}

func TestLogoutConDenylistQueFallaResponde503(t *testing.T) {
	servicio := &servicioDeDispositivosFalso{cerrarErr: auth.ConFailClosed(errors.New("redis caido"))}
	r := routerDeDispositivos(servicio)

	rec := pedirConCredencial(r, http.MethodPost, "/v1/auth/logout", "")

	assertErrorEnvelope(t, rec, http.StatusServiceUnavailable, "AuthUnavailable")
}

func TestLogoutAllConDenylistQueFallaResponde503(t *testing.T) {
	servicio := &servicioDeDispositivosFalso{cerrarTodasErr: auth.ConFailClosed(errors.New("redis caido"))}
	r := routerDeDispositivos(servicio)

	rec := pedirConCredencial(r, http.MethodPost, "/v1/auth/logout-all", "")

	assertErrorEnvelope(t, rec, http.StatusServiceUnavailable, "AuthUnavailable")
}

func TestLogoutYLogoutAllResponden204(t *testing.T) {
	r := routerDeDispositivos(&servicioDeDispositivosFalso{})
	for _, ruta := range []string{"/v1/auth/logout", "/v1/auth/logout-all"} {
		t.Run(ruta, func(t *testing.T) {
			rec := pedirConCredencial(r, http.MethodPost, ruta, "")
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, quiero 204 (cuerpo %q)", rec.Code, rec.Body.String())
			}
		})
	}
}

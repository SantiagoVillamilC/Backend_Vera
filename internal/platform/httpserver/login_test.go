package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/google/uuid"
)

// iniciadorFalso devuelve la sesión y el error configurados sin tocar la base.
type iniciadorFalso struct {
	resultado auth.SesionEmitida
	err       error
	llamadas  int
}

func (i *iniciadorFalso) IniciarSesion(context.Context, string, string, string, string, string) (auth.SesionEmitida, error) {
	i.llamadas++
	return i.resultado, i.err
}

// routerConLogin arma el router público con el handler de login inyectado, o el stub si es nil.
func routerConLogin(iniciador IniciadorDeSesion) http.Handler {
	var handler LoginHandler
	if iniciador != nil {
		handler = NuevoLoginHandler(iniciador)
	}
	return NewRouter(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{
		Identidad: auth.UsuarioDePrueba{},
		Login:     handler,
	})
}

func TestLoginHandlerDesenlacesDelLoginDirecto(t *testing.T) {
	parte := uuid.New()
	casos := map[string]struct {
		cuerpo       string
		resultado    auth.SesionEmitida
		err          error
		estado       int
		codigo       string
		campoDetalle string
		reglaDetalle string
	}{
		"sin celular": {
			cuerpo:       `{"contrasena":"secreta123"}`,
			estado:       http.StatusUnprocessableEntity,
			codigo:       "ValidationError",
			campoDetalle: "celular",
			reglaDetalle: "required",
		},
		"sin contrasena": {
			cuerpo:       `{"celular":"+573001112233"}`,
			estado:       http.StatusUnprocessableEntity,
			codigo:       "ValidationError",
			campoDetalle: "contrasena",
			reglaDetalle: "required",
		},
		"celular invalido": {
			cuerpo:       `{"celular":"no-es-celular","contrasena":"secreta123"}`,
			estado:       http.StatusUnprocessableEntity,
			codigo:       "ValidationError",
			campoDetalle: "celular",
			reglaDetalle: "e164",
		},
		"credenciales invalidas": {
			cuerpo: `{"celular":"+573001112233","contrasena":"mala"}`,
			err:    auth.ErrCredencialesInvalidas,
			estado: http.StatusUnauthorized,
			codigo: "InvalidCredentials",
		},
		"empresa sin rol": {
			cuerpo: `{"celular":"+573001112233","contrasena":"secreta123","contexto":"empresa"}`,
			err:    auth.ErrSinRolDeEmpresa,
			estado: http.StatusForbidden,
			codigo: "NoCompanyRole",
		},
		"cuenta pendiente": {
			cuerpo: `{"celular":"+573001112233","contrasena":"secreta123"}`,
			err:    auth.ErrCelularSinVerificar,
			estado: http.StatusForbidden,
			codigo: "PhoneUnverified",
		},
		"cuenta activa": {
			cuerpo: `{"celular":"+573001112233","contrasena":"secreta123"}`,
			resultado: auth.SesionEmitida{
				AccessToken:  "access",
				RefreshToken: "refresh",
				Usuario:      auth.DatosUsuario{ID: parte, TipoCuenta: "persona", Correo: "persona@centinela.test", ContextoActivo: "usuario"},
			},
			estado: http.StatusOK,
		},
	}

	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			iniciador := &iniciadorFalso{resultado: caso.resultado, err: caso.err}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/auth/login", strings.NewReader(caso.cuerpo))
			rec := httptest.NewRecorder()

			routerConLogin(iniciador).ServeHTTP(rec, req)

			if rec.Code != caso.estado {
				t.Fatalf("status = %d, quiero %d (cuerpo %q)", rec.Code, caso.estado, rec.Body.String())
			}
			if caso.codigo != "" {
				assertErrorEnvelope(t, rec, caso.estado, caso.codigo)
			}
			if caso.campoDetalle != "" {
				assertDetalleDeValidacion(t, rec, caso.campoDetalle, caso.reglaDetalle)
			}
			if caso.estado == http.StatusOK {
				assertSesionDelLogin(t, rec, caso.resultado)
			}
		})
	}
}

// assertDetalleDeValidacion exige un detail con el campo y la regla pedidos.
func assertDetalleDeValidacion(t *testing.T, rec *httptest.ResponseRecorder, campo, regla string) {
	t.Helper()
	env := decodeEnvelope(t, rec)
	apiErr, _ := env["error"].(map[string]any)
	detalles, _ := apiErr["details"].([]any)
	for _, detalle := range detalles {
		campoMapa, _ := detalle.(map[string]any)
		if campoMapa["field"] == campo && campoMapa["rule"] == regla {
			return
		}
	}
	t.Fatalf("no hay detail %s/%s en %q", campo, regla, rec.Body.String())
}

// assertSesionDelLogin exige el 200 con access, refresh y usuario, y sin challenge.
func assertSesionDelLogin(t *testing.T, rec *httptest.ResponseRecorder, esperado auth.SesionEmitida) {
	t.Helper()
	cuerpo := rec.Body.String()
	if strings.Contains(cuerpo, "challenge_id") {
		t.Fatalf("el 200 de login trae challenge_id: %q", cuerpo)
	}
	var env struct {
		Data api.Sesion `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("respuesta no JSON: %v", err)
	}
	if env.Data.AccessToken != esperado.AccessToken || env.Data.RefreshToken != esperado.RefreshToken {
		t.Fatalf("par = %+v, quiero access/refresh", env.Data)
	}
	if env.Data.Usuario.Id != esperado.Usuario.ID {
		t.Fatalf("usuario = %+v, quiero %s", env.Data.Usuario, esperado.Usuario.ID)
	}
}

func TestLoginConServicioNuloResponde501(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/auth/login", strings.NewReader(`{"celular":"+573001112233","contrasena":"secreta123"}`))
	rec := httptest.NewRecorder()

	routerConLogin(nil).ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusNotImplemented, "NotImplemented")
}

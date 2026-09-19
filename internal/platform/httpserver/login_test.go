package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/google/uuid"
)

// iniciadorFalso devuelve el desenlace y el error configurados sin tocar la base.
type iniciadorFalso struct {
	resultado auth.LoginData
	err       error
	llamadas  int
}

func (i *iniciadorFalso) IniciarSesion(context.Context, string, string, string, string) (auth.LoginData, error) {
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

func TestLoginHandlerDesenlacesDelSegundoFactor(t *testing.T) {
	casos := map[string]struct {
		cuerpo       string
		resultado    auth.LoginData
		err          error
		estado       int
		codigo       string
		campoDetalle string
		reglaDetalle string
	}{
		"sin correo": {
			cuerpo:       `{"contrasena":"secreta123"}`,
			estado:       http.StatusUnprocessableEntity,
			codigo:       "ValidationError",
			campoDetalle: "correo",
			reglaDetalle: "required",
		},
		"sin contrasena": {
			cuerpo:       `{"correo":"persona@centinela.test"}`,
			estado:       http.StatusUnprocessableEntity,
			codigo:       "ValidationError",
			campoDetalle: "contrasena",
			reglaDetalle: "required",
		},
		"correo invalido": {
			cuerpo:       `{"correo":"no-es-correo","contrasena":"secreta123"}`,
			estado:       http.StatusUnprocessableEntity,
			codigo:       "ValidationError",
			campoDetalle: "correo",
			reglaDetalle: "email",
		},
		"credenciales invalidas": {
			cuerpo: `{"correo":"persona@centinela.test","contrasena":"mala"}`,
			err:    auth.ErrCredencialesInvalidas,
			estado: http.StatusUnauthorized,
			codigo: "InvalidCredentials",
		},
		"empresa sin rol": {
			cuerpo: `{"correo":"persona@centinela.test","contrasena":"secreta123","contexto":"empresa"}`,
			err:    auth.ErrSinRolDeEmpresa,
			estado: http.StatusForbidden,
			codigo: "NoCompanyRole",
		},
		"cuenta pendiente": {
			cuerpo: `{"correo":"persona@centinela.test","contrasena":"secreta123"}`,
			resultado: auth.LoginData{
				Proposito:             auth.PropositoRegistro,
				ChallengeID:           uuid.New(),
				ExpiraEnSegundos:      300,
				Mensaje:               auth.MensajeDesafioRegistro,
				RequiereSegundoFactor: true,
			},
			estado: http.StatusOK,
		},
		"cuenta activa": {
			cuerpo: `{"correo":"persona@centinela.test","contrasena":"secreta123"}`,
			resultado: auth.LoginData{
				Proposito:             auth.PropositoLogin,
				ChallengeID:           uuid.New(),
				ExpiraEnSegundos:      300,
				Mensaje:               auth.MensajeDesafioLogin,
				RequiereSegundoFactor: true,
			},
			estado: http.StatusOK,
		},
	}

	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			iniciador := &iniciadorFalso{resultado: caso.resultado, err: caso.err}
			req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(caso.cuerpo))
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
				assertLoginSinTokens(t, rec, caso.resultado)
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

// assertLoginSinTokens exige el LoginData esperado y que el 200 no traiga tokens.
func assertLoginSinTokens(t *testing.T, rec *httptest.ResponseRecorder, esperado auth.LoginData) {
	t.Helper()
	cuerpo := rec.Body.String()
	if strings.Contains(cuerpo, "access_token") || strings.Contains(cuerpo, "refresh_token") {
		t.Fatalf("el 200 trae tokens: %q", cuerpo)
	}
	env := decodeEnvelope(t, rec)
	datos, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data ausente en %q", cuerpo)
	}
	if datos["proposito"] != string(esperado.Proposito) {
		t.Fatalf("proposito = %v, quiero %q", datos["proposito"], esperado.Proposito)
	}
	if datos["requiere_segundo_factor"] != true {
		t.Fatalf("requiere_segundo_factor = %v, quiero true", datos["requiere_segundo_factor"])
	}
	if datos["challenge_id"] == nil || datos["challenge_id"] == "" {
		t.Fatalf("challenge_id ausente en %q", cuerpo)
	}
	if int(datos["expira_en_segundos"].(float64)) != esperado.ExpiraEnSegundos {
		t.Fatalf("expira_en_segundos = %v, quiero %d", datos["expira_en_segundos"], esperado.ExpiraEnSegundos)
	}
	if datos["mensaje"] != esperado.Mensaje {
		t.Fatalf("mensaje = %v, quiero %q", datos["mensaje"], esperado.Mensaje)
	}
}

func TestLoginConServicioNuloResponde501(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(`{"correo":"persona@centinela.test","contrasena":"secreta123"}`))
	rec := httptest.NewRecorder()

	routerConLogin(nil).ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusNotImplemented, "NotImplemented")
}

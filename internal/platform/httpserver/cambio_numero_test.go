package httpserver

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/google/uuid"
)

// cambioDeNumeroFalso devuelve los valores configurados y registra lo que recibe del handler.
type cambioDeNumeroFalso struct {
	desafio     auth.Desafio
	resultado   auth.ResultadoCambio
	err         error
	sesiones    []uuid.UUID
	celulares   []string
	contrasenas []string
	respaldos   []*string
	challenges  []uuid.UUID
	codigos     []string
}

// IniciarCambioDeNumero sirve el desafío o el error configurados.
func (f *cambioDeNumeroFalso) IniciarCambioDeNumero(_ context.Context, sesionID uuid.UUID, nuevoCelular, contrasenaActual string, codigoRespaldo *string, _ string) (auth.Desafio, error) {
	f.sesiones = append(f.sesiones, sesionID)
	f.celulares = append(f.celulares, nuevoCelular)
	f.contrasenas = append(f.contrasenas, contrasenaActual)
	f.respaldos = append(f.respaldos, codigoRespaldo)
	return f.desafio, f.err
}

// ConfirmarCambioDeNumero sirve el resultado o el error configurados.
func (f *cambioDeNumeroFalso) ConfirmarCambioDeNumero(_ context.Context, sesionID, challengeID uuid.UUID, codigo, _ string) (auth.ResultadoCambio, error) {
	f.sesiones = append(f.sesiones, sesionID)
	f.challenges = append(f.challenges, challengeID)
	f.codigos = append(f.codigos, codigo)
	return f.resultado, f.err
}

// routerConCambioDeNumero monta el handler del cambio en el grupo autenticado, o el stub si es nil.
func routerConCambioDeNumero(falso *cambioDeNumeroFalso) http.Handler {
	var handler PhoneChangeHandler
	if falso != nil {
		handler = NuevoCambioDeNumeroHandler(falso)
	}
	return NewRouter(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{
		Identidad:   auth.UsuarioDePrueba{},
		PhoneChange: handler,
	})
}

// peticionDeCambio arma el POST con el cuerpo dado y, opcionalmente, la identidad de prueba.
func peticionDeCambio(t *testing.T, ruta, cuerpo string, autenticada bool) *http.Request {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, ruta, strings.NewReader(cuerpo))
	req.Header.Set("Content-Type", "application/json")
	if autenticada {
		req.Header.Set(auth.CabeceraUsuarioDePrueba, "1")
	}
	return req
}

func TestCambioDeNumeroSinIdentidadResponde401(t *testing.T) {
	router := routerConCambioDeNumero(&cambioDeNumeroFalso{})
	casos := map[string]*http.Request{
		"request": peticionDeCambio(t, "/v1/auth/phone/change/request", `{"nuevo_celular":"+573001119999","contrasena_actual":"secreta"}`, false),
		"confirm": peticionDeCambio(t, "/v1/auth/phone/change/confirm", `{"challenge_id":"`+uuid.NewString()+`","codigo":"123456"}`, false),
	}
	for nombre, req := range casos {
		t.Run(nombre, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			assertErrorEnvelope(t, rec, http.StatusUnauthorized, "Unauthorized")
		})
	}
}

func TestCambioDeNumeroSinHandlerResponde501(t *testing.T) {
	router := routerConCambioDeNumero(nil)
	casos := map[string]*http.Request{
		"request": peticionDeCambio(t, "/v1/auth/phone/change/request", `{"nuevo_celular":"+573001119999","contrasena_actual":"secreta"}`, true),
		"confirm": peticionDeCambio(t, "/v1/auth/phone/change/confirm", `{"challenge_id":"`+uuid.NewString()+`","codigo":"123456"}`, true),
	}
	for nombre, req := range casos {
		t.Run(nombre, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			assertErrorEnvelope(t, rec, http.StatusNotImplemented, "NotImplemented")
		})
	}
}

func TestCambioDeNumeroRequestSinContrasenaResponde422(t *testing.T) {
	rec := httptest.NewRecorder()
	routerConCambioDeNumero(&cambioDeNumeroFalso{}).ServeHTTP(rec,
		peticionDeCambio(t, "/v1/auth/phone/change/request", `{"nuevo_celular":"+573001119999"}`, true))

	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
	assertDetalleDeValidacion(t, rec, "contrasena_actual", "required")
}

func TestCambioDeNumeroRequestCelularNoE164Responde422(t *testing.T) {
	rec := httptest.NewRecorder()
	routerConCambioDeNumero(&cambioDeNumeroFalso{}).ServeHTTP(rec,
		peticionDeCambio(t, "/v1/auth/phone/change/request", `{"nuevo_celular":"3001234","contrasena_actual":"secreta"}`, true))

	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
	assertDetalleDeValidacion(t, rec, "nuevo_celular", "e164")
}

func TestCambioDeNumeroRequestCelularInvalidoDelServicioResponde422(t *testing.T) {
	falso := &cambioDeNumeroFalso{err: auth.ErrCelularInvalido}
	rec := httptest.NewRecorder()
	routerConCambioDeNumero(falso).ServeHTTP(rec,
		peticionDeCambio(t, "/v1/auth/phone/change/request", `{"nuevo_celular":"+573","contrasena_actual":"secreta"}`, true))

	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
	assertDetalleDeValidacion(t, rec, "nuevo_celular", "e164")
}

func TestCambioDeNumeroRequestCelularIgualResponde422(t *testing.T) {
	falso := &cambioDeNumeroFalso{err: auth.ErrCelularNuevoIgual}
	rec := httptest.NewRecorder()
	routerConCambioDeNumero(falso).ServeHTTP(rec,
		peticionDeCambio(t, "/v1/auth/phone/change/request", `{"nuevo_celular":"+573001112233","contrasena_actual":"secreta"}`, true))

	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
	assertDetalleDeValidacion(t, rec, "nuevo_celular", "distinto")
}

func TestCambioDeNumeroRequestContrasenaMalaResponde401(t *testing.T) {
	falso := &cambioDeNumeroFalso{err: auth.ErrCredencialesInvalidas}
	rec := httptest.NewRecorder()
	routerConCambioDeNumero(falso).ServeHTTP(rec,
		peticionDeCambio(t, "/v1/auth/phone/change/request", `{"nuevo_celular":"+573001119999","contrasena_actual":"mala"}`, true))

	assertErrorEnvelope(t, rec, http.StatusUnauthorized, "InvalidCredentials")
}

func TestCambioDeNumeroRequestRespaldoMaloResponde401(t *testing.T) {
	falso := &cambioDeNumeroFalso{err: auth.ErrSegundoFactorRequerido}
	rec := httptest.NewRecorder()
	routerConCambioDeNumero(falso).ServeHTTP(rec,
		peticionDeCambio(t, "/v1/auth/phone/change/request", `{"nuevo_celular":"+573001119999","contrasena_actual":"secreta","codigo_respaldo":"ABCD-EFGH-IJKL-MNOP"}`, true))

	assertErrorEnvelope(t, rec, http.StatusUnauthorized, "SecondFactorRequired")
	if len(falso.respaldos) != 1 || falso.respaldos[0] == nil || *falso.respaldos[0] != "ABCD-EFGH-IJKL-MNOP" {
		t.Fatalf("respaldos = %v, quiero el código de respaldo recibido", falso.respaldos)
	}
}

func TestCambioDeNumeroRequestEnEnfriamientoResponde429(t *testing.T) {
	falso := &cambioDeNumeroFalso{err: auth.ErrCambioNumeroEnEnfriamiento}
	rec := httptest.NewRecorder()
	routerConCambioDeNumero(falso).ServeHTTP(rec,
		peticionDeCambio(t, "/v1/auth/phone/change/request", `{"nuevo_celular":"+573001119999","contrasena_actual":"secreta"}`, true))

	assertErrorEnvelope(t, rec, http.StatusTooManyRequests, "PhoneChangeCooldown")
}

func TestCambioDeNumeroCaminoARespondePropositoNuevo(t *testing.T) {
	falso := &cambioDeNumeroFalso{desafio: auth.Desafio{ID: uuid.New(), ExpiraEnSegundos: 300, Proposito: auth.PropositoCambioNumero}}
	rec := httptest.NewRecorder()
	routerConCambioDeNumero(falso).ServeHTTP(rec,
		peticionDeCambio(t, "/v1/auth/phone/change/request", `{"nuevo_celular":"+573001119999","contrasena_actual":"secreta","codigo_respaldo":"ABCD-EFGH-IJKL-MNOP"}`, true))

	assertPropositoDelDesafio(t, rec, "cambio_numero")
}

func TestCambioDeNumeroCaminoBRespondePropositoViejo(t *testing.T) {
	falso := &cambioDeNumeroFalso{desafio: auth.Desafio{ID: uuid.New(), ExpiraEnSegundos: 300, Proposito: auth.PropositoCambioNumeroViejo}}
	rec := httptest.NewRecorder()
	routerConCambioDeNumero(falso).ServeHTTP(rec,
		peticionDeCambio(t, "/v1/auth/phone/change/request", `{"nuevo_celular":"+573001119999","contrasena_actual":"secreta"}`, true))

	assertPropositoDelDesafio(t, rec, "cambio_numero_viejo")
}

func TestCambioDeNumeroConfirmOkResponde200(t *testing.T) {
	falso := &cambioDeNumeroFalso{resultado: auth.ResultadoCambio{Confirmado: true}}
	rec := httptest.NewRecorder()
	routerConCambioDeNumero(falso).ServeHTTP(rec,
		peticionDeCambio(t, "/v1/auth/phone/change/confirm", `{"challenge_id":"`+uuid.NewString()+`","codigo":"123456"}`, true))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
}

func TestCambioDeNumeroConfirmPendienteResponde200(t *testing.T) {
	falso := &cambioDeNumeroFalso{resultado: auth.ResultadoCambio{Confirmado: false}}
	rec := httptest.NewRecorder()
	routerConCambioDeNumero(falso).ServeHTTP(rec,
		peticionDeCambio(t, "/v1/auth/phone/change/confirm", `{"challenge_id":"`+uuid.NewString()+`","codigo":"123456"}`, true))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
}

func TestCambioDeNumeroRequestFailClosedResponde503(t *testing.T) {
	falso := &cambioDeNumeroFalso{err: auth.ConFailClosed(errors.New("contadores caidos"))}
	rec := httptest.NewRecorder()
	routerConCambioDeNumero(falso).ServeHTTP(rec,
		peticionDeCambio(t, "/v1/auth/phone/change/request", `{"nuevo_celular":"+573001119999","contrasena_actual":"secreta"}`, true))

	assertErrorEnvelope(t, rec, http.StatusServiceUnavailable, "AuthUnavailable")
}

func TestCambioDeNumeroConfirmFailClosedResponde503(t *testing.T) {
	falso := &cambioDeNumeroFalso{err: auth.ConFailClosed(errors.New("denylist caida"))}
	rec := httptest.NewRecorder()
	routerConCambioDeNumero(falso).ServeHTTP(rec,
		peticionDeCambio(t, "/v1/auth/phone/change/confirm", `{"challenge_id":"`+uuid.NewString()+`","codigo":"123456"}`, true))

	assertErrorEnvelope(t, rec, http.StatusServiceUnavailable, "AuthUnavailable")
}

func TestCambioDeNumeroNoLogueaSecretos(t *testing.T) {
	const (
		contrasena = "contrasena-secreta-del-cambio"
		respaldo   = "ABCD-EFGH-IJKL-MNOP"
	)

	var buf bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(manejadorEnMemoria{lineas: &buf}))
	defer slog.SetDefault(original)

	falso := &cambioDeNumeroFalso{desafio: auth.Desafio{ID: uuid.New(), ExpiraEnSegundos: 300, Proposito: auth.PropositoCambioNumero}}
	router := routerConCambioDeNumero(falso)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, peticionDeCambio(t, "/v1/auth/phone/change/request",
		`{"nuevo_celular":"+573001119999","contrasena_actual":"`+contrasena+`","codigo_respaldo":"`+respaldo+`"}`, true))

	registrado := buf.String()
	for nombre, secreto := range map[string]string{"contrasena": contrasena, "codigo respaldo": respaldo} {
		if strings.Contains(registrado, secreto) {
			t.Fatalf("el log contiene el %s: %q", nombre, registrado)
		}
	}
}

// assertPropositoDelDesafio verifica el propósito del desafío en el envelope de éxito.
func assertPropositoDelDesafio(t *testing.T, rec *httptest.ResponseRecorder, quiero string) {
	t.Helper()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	env := decodeEnvelope(t, rec)
	datos, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data ausente en %q", rec.Body.String())
	}
	if datos["proposito"] != quiero {
		t.Fatalf("proposito = %v, quiero %q", datos["proposito"], quiero)
	}
}

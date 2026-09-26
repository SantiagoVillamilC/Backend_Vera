package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/google/uuid"
)

// recuperadorFalso devuelve el desafío o el error configurados y genera un id nuevo si falta.
type recuperadorFalso struct {
	desafio auth.Desafio
	err     error
	correos []string
}

func (r *recuperadorFalso) SolicitarRecuperacion(_ context.Context, correo, _ string) (auth.Desafio, error) {
	r.correos = append(r.correos, correo)
	desafio := r.desafio
	if desafio.ID == uuid.Nil {
		desafio.ID = uuid.New()
	}
	return desafio, r.err
}

// routerConRecuperacion arma el router con el handler de recuperación inyectado.
func routerConRecuperacion(handler RecuperacionHandler) http.Handler {
	return NewRouter(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{
		Identidad:    auth.UsuarioDePrueba{},
		Recuperacion: handler,
	})
}

// peticionDeRecuperacion arma el POST con el correo indicado.
func peticionDeRecuperacion(t *testing.T, correo string) *http.Request {
	t.Helper()

	body, err := json.Marshal(map[string]string{"correo": correo})
	if err != nil {
		t.Fatalf("serializar el cuerpo: %v", err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/auth/recovery/request", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// sinUUID reemplaza los uuid del cuerpo para comparar la forma de dos respuestas.
func sinUUID(cuerpo []byte) string {
	return regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`).ReplaceAllString(string(cuerpo), "UUID")
}

func TestSolicitarRecuperacionRespondeDesafio202(t *testing.T) {
	id := uuid.New()
	falso := &recuperadorFalso{desafio: auth.Desafio{ID: id, ExpiraEnSegundos: 300, Proposito: auth.PropositoRecuperacion}}
	rec := httptest.NewRecorder()

	routerConRecuperacion(NuevoRecuperacionHandler(falso)).ServeHTTP(rec, peticionDeRecuperacion(t, "persona@centinela.test"))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, quiero 202 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	env := decodeEnvelope(t, rec)
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data ausente o con forma rara en %q", rec.Body.String())
	}
	if got := data["challenge_id"]; got != id.String() {
		t.Errorf("challenge_id = %v, quiero %s", got, id)
	}
	if got := data["expira_en_segundos"]; got != float64(300) {
		t.Errorf("expira_en_segundos = %v, quiero 300", got)
	}
	if got := data["proposito"]; got != string(auth.PropositoRecuperacion) {
		t.Errorf("proposito = %v, quiero %s", got, auth.PropositoRecuperacion)
	}
	if len(falso.correos) != 1 || falso.correos[0] != "persona@centinela.test" {
		t.Errorf("correos = %v, quiero el correo recibido", falso.correos)
	}
}

func TestSolicitarRecuperacionExistenteEInexistenteTienenLaMismaForma(t *testing.T) {
	falso := &recuperadorFalso{desafio: auth.Desafio{ExpiraEnSegundos: 300, Proposito: auth.PropositoRecuperacion}}
	r := routerConRecuperacion(NuevoRecuperacionHandler(falso))

	existente := httptest.NewRecorder()
	r.ServeHTTP(existente, peticionDeRecuperacion(t, "existe@centinela.test"))
	inexistente := httptest.NewRecorder()
	r.ServeHTTP(inexistente, peticionDeRecuperacion(t, "no-existe@centinela.test"))

	if existente.Code != http.StatusAccepted || inexistente.Code != http.StatusAccepted {
		t.Fatalf("status = %d/%d, quiero 202/202", existente.Code, inexistente.Code)
	}
	if sinUUID(existente.Body.Bytes()) != sinUUID(inexistente.Body.Bytes()) {
		t.Errorf("cuerpos distintos:\nexistente  = %s\ninexistente = %s",
			sinUUID(existente.Body.Bytes()), sinUUID(inexistente.Body.Bytes()))
	}
}

func TestSolicitarRecuperacionCorreoInvalidoResponde422(t *testing.T) {
	rec := httptest.NewRecorder()

	routerConRecuperacion(NuevoRecuperacionHandler(&recuperadorFalso{})).ServeHTTP(
		rec, peticionDeRecuperacion(t, "no-es-un-correo"))

	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
	env := decodeEnvelope(t, rec)
	apiErr, _ := env["error"].(map[string]any)
	details, ok := apiErr["details"].([]any)
	if !ok || len(details) == 0 {
		t.Fatalf("quiero details con el campo correo, obtuve %q", rec.Body.String())
	}
	detalle, _ := details[0].(map[string]any)
	if detalle["field"] != "correo" || detalle["rule"] != "email" {
		t.Errorf("detail = %v, quiero correo/email", detalle)
	}
}

func TestSolicitarRecuperacionMapeaErrores(t *testing.T) {
	casos := map[string]struct {
		err    error
		status int
		code   string
	}{
		"tope agregado":     {err: auth.ErrDemasiadosEnvios, status: http.StatusTooManyRequests, code: "TooManyRequests"},
		"intentos agotados": {err: auth.ErrDemasiadosIntentos, status: http.StatusTooManyRequests, code: "OtpTooManyAttempts"},
		"fail closed":       {err: auth.ConFailClosed(errors.New("redis caído")), status: http.StatusServiceUnavailable, code: "AuthUnavailable"},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			rec := httptest.NewRecorder()

			routerConRecuperacion(NuevoRecuperacionHandler(&recuperadorFalso{err: caso.err})).ServeHTTP(
				rec, peticionDeRecuperacion(t, "persona@centinela.test"))

			assertErrorEnvelope(t, rec, caso.status, caso.code)
		})
	}
}

func TestSolicitarRecuperacionSinHandlerResponde501(t *testing.T) {
	rec := httptest.NewRecorder()

	routerConRecuperacion(nil).ServeHTTP(rec, peticionDeRecuperacion(t, "persona@centinela.test"))

	assertErrorEnvelope(t, rec, http.StatusNotImplemented, "NotImplemented")
}

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/google/uuid"
)

// reenviadorFalso devuelve el desafío o el error configurados sin tocar la base.
type reenviadorFalso struct {
	desafio auth.Desafio
	err     error
}

// ReenviarDesafio implementa la interfaz que consume el handler.
func (r *reenviadorFalso) ReenviarDesafio(context.Context, uuid.UUID, string) (auth.Desafio, error) {
	return r.desafio, r.err
}

// contadoresCaidos hace fallar el puerto Contadores para probar el fail closed.
type contadoresCaidos struct{}

func (contadoresCaidos) Incrementar(context.Context, string, time.Duration) (int64, error) {
	return 0, auth.ErrAuthNoDisponible
}

func (contadoresCaidos) Bloquear(context.Context, string, time.Duration) error {
	return auth.ErrAuthNoDisponible
}

func (contadoresCaidos) Bloqueada(context.Context, string) (bool, error) {
	return false, auth.ErrAuthNoDisponible
}

// routerConReenvio arma el router con el handler de reenvío inyectado.
func routerConReenvio(handler DesafiosHandler) http.Handler {
	return NewRouter(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{
		Identidad: auth.UsuarioDePrueba{},
		Desafios:  handler,
	})
}

// peticionDeReenvio arma el POST con el challenge_id indicado.
func peticionDeReenvio(t *testing.T, challengeID uuid.UUID) *http.Request {
	t.Helper()

	body, err := json.Marshal(map[string]string{"challenge_id": challengeID.String()})
	if err != nil {
		t.Fatalf("serializar el cuerpo: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/otp/resend", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestResendOtpRespondeElDesafioConLaVigencia(t *testing.T) {
	challengeID := uuid.New()
	falso := &reenviadorFalso{desafio: auth.Desafio{ID: challengeID, ExpiraEnSegundos: 300, Proposito: auth.PropositoLogin}}
	rec := httptest.NewRecorder()

	routerConReenvio(NuevoOTPHandler(falso)).ServeHTTP(rec, peticionDeReenvio(t, challengeID))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	env := decodeEnvelope(t, rec)
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data ausente o con forma rara en %q", rec.Body.String())
	}
	if got := data["challenge_id"]; got != challengeID.String() {
		t.Errorf("challenge_id = %v, quiero %s", got, challengeID)
	}
	if got := data["expira_en_segundos"]; got != float64(300) {
		t.Errorf("expira_en_segundos = %v, quiero 300", got)
	}
	if got := data["proposito"]; got != string(auth.PropositoLogin) {
		t.Errorf("proposito = %v, quiero %s", got, auth.PropositoLogin)
	}
}

func TestResendOtpMapeaElErrorAlEnvelope(t *testing.T) {
	casos := map[string]struct {
		err    error
		status int
		code   string
	}{
		"desafio inexistente": {err: auth.ErrDesafioInvalido, status: http.StatusBadRequest, code: "ChallengeInvalid"},
		"en enfriamiento":     {err: auth.ErrEnfriamientoReenvio, status: http.StatusTooManyRequests, code: "OtpResendCooldown"},
		"tope de reenvios":    {err: auth.ErrDemasiadosReenvios, status: http.StatusTooManyRequests, code: "OtpTooManyResends"},
		"tope agregado":       {err: auth.ErrDemasiadosEnvios, status: http.StatusTooManyRequests, code: "TooManyRequests"},
		"fail closed":         {err: auth.ConFailClosed(context.DeadlineExceeded), status: http.StatusServiceUnavailable, code: "AuthUnavailable"},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			falso := &reenviadorFalso{err: caso.err}
			rec := httptest.NewRecorder()

			routerConReenvio(NuevoOTPHandler(falso)).ServeHTTP(rec, peticionDeReenvio(t, uuid.New()))

			assertErrorEnvelope(t, rec, caso.status, caso.code)
		})
	}
}

func TestResendOtpCuadradoConContadoresCaidosResponde503(t *testing.T) {
	claves, err := auth.NuevasClaves(bytes.Repeat([]byte{0x11}, 32))
	if err != nil {
		t.Fatalf("NuevasClaves: %v", err)
	}
	gestor, err := auth.NuevoGestorDesafios(nil, nil, nil, nil, bytes.Repeat([]byte{0x22}, 32),
		auth.ConRacionamiento(contadoresCaidos{}, claves))
	if err != nil {
		t.Fatalf("NuevoGestorDesafios: %v", err)
	}
	rec := httptest.NewRecorder()

	routerConReenvio(NuevoOTPHandler(gestor)).ServeHTTP(rec, peticionDeReenvio(t, uuid.New()))

	assertErrorEnvelope(t, rec, http.StatusServiceUnavailable, "AuthUnavailable")
}

func TestResendOtpSinChallengeIdResponde422(t *testing.T) {
	falso := &reenviadorFalso{desafio: auth.Desafio{ExpiraEnSegundos: 300, Proposito: auth.PropositoLogin}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/otp/resend", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")

	routerConReenvio(NuevoOTPHandler(falso)).ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
}

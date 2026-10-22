package httpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

type vozVeraPrueba func(context.Context, uuid.UUID, *copilot.AudioIA, uuid.UUID) (copilot.RespuestaTexto, error)

func (f vozVeraPrueba) EnviarVoz(ctx context.Context, conv uuid.UUID, a *copilot.AudioIA, clave uuid.UUID) (copilot.RespuestaTexto, error) {
	return f(ctx, conv, a, clave)
}
func cuerpoVozPrueba(t *testing.T, nombre string) string {
	t.Helper()
	b, err := os.ReadFile("../../copilot/testdata/audio/" + nombre)
	if err != nil {
		t.Fatal(err)
	}
	formato := "wav"
	if strings.HasSuffix(nombre, "m4a") {
		formato = "m4a"
	}
	body, _ := json.Marshal(api.EnviarAudioVeraRequest{Audio: base64.StdEncoding.EncodeToString(b), Formato: api.EnviarAudioVeraRequestFormato(formato)})
	return string(body)
}
func TestVeraVozValidacionAntesDeUsoYProveedor(t *testing.T) {
	for _, body := range []string{`{}`, `{"audio":"%%%","formato":"wav"}`, `{"audio":"YWJj","formato":"mp3"}`, `{"audio":"YWJj","formato":"wav"}`, `{"audio":"","formato":"wav"}`, `{"audio":"YWJj","formato":"wav","historial":[]}`, `{"audio":"YWJj","formato":"wav","audio":"otra"}`, `{"audio":"` + strings.Repeat("A", 960001) + `","formato":"wav"}`, cuerpoVozPrueba(t, "largo.m4a")} {
		uso := &usoVeraPrueba{}
		s := servidorV1{Vera: DependenciasVera{Consentimiento: copilot.NuevoControlConsentimiento(consentimientoUsoPrueba{true}, nil), Uso: uso, Voz: vozVeraPrueba(func(context.Context, uuid.UUID, *copilot.AudioIA, uuid.UUID) (copilot.RespuestaTexto, error) {
			t.Fatal("audio inválido llegó al servicio")
			return copilot.RespuestaTexto{}, nil
		})}}
		ctx := identidad.ConIdentidad(t.Context(), identidad.Identidad{ParteID: uuid.NewString(), UsuarioID: uuid.NewString()})
		w := httptest.NewRecorder()
		s.EnviarMensajeVozVera(w, httptest.NewRequestWithContext(ctx, "POST", "/", strings.NewReader(body)), api.EnviarMensajeVozVeraParams{})
		if w.Code != 422 || uso.llamadas != 0 || !strings.Contains(w.Body.String(), "ValidationError") {
			t.Fatalf("%d %s uso=%d", w.Code, w.Body, uso.llamadas)
		}
	}
}
func TestVeraVozRouterRespuestaYBorradoEnMemoria(t *testing.T) {
	id := identidad.Identidad{ParteID: uuid.NewString(), UsuarioID: uuid.NewString()}
	clave := uuid.New()
	llamadas := 0
	var retenido []byte
	voz := vozVeraPrueba(func(ctx context.Context, conv uuid.UUID, a *copilot.AudioIA, k uuid.UUID) (copilot.RespuestaTexto, error) {
		llamadas++
		actual, _ := identidad.DeContexto(ctx)
		deadline, ok := ctx.Deadline()
		if actual != id || conv != uuid.Nil || k != clave || a.MIME != "audio/m4a" || len(a.Datos) == 0 || !ok || time.Until(deadline) > 45*time.Second {
			t.Fatal("entrada no autorizada o sin plazo")
		}
		retenido = a.Datos
		return copilot.RespuestaTexto{ConversacionID: uuid.New(), MensajeUsuarioID: uuid.New(), MensajeID: uuid.New(), Texto: "Saldo consultado.", Transcripcion: "Consultar saldo", CreadoEn: time.Now()}, nil
	})
	router := NewRouterConVera(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{Identidad: identidadDeContextoFijo{id: id}}, DependenciasVera{Voz: voz, Consentimiento: copilot.NuevoControlConsentimiento(consentimientoUsoPrueba{true}, nil), Uso: &usoVeraPrueba{}})
	r := httptest.NewRequestWithContext(t.Context(), "POST", "/v1/vera/mensajes/voz", strings.NewReader(cuerpoVozPrueba(t, "tono.m4a")))
	r.Header.Set("Idempotency-Key", clave.String())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 200 || llamadas != 1 || !strings.Contains(w.Body.String(), `"transcripcion":"Consultar saldo"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for _, b := range retenido {
		if b != 0 {
			t.Fatal("buffer de audio no se limpió")
		}
	}
}
func TestVeraVozErroresYAutorizacion(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{copilot.ErrAudioNoEntendido, 422, "AudioNotUnderstood"}, {errors.New("detalle privado"), 503, "AssistantUnavailable"}, {context.DeadlineExceeded, 504, "Timeout"}, {copilot.ErrClaveTextoEnConflicto, 409, "IdempotencyConflict"}, {copilot.ErrConversacionTextoNoEncontrada, 404, "NotFound"}} {
		w := httptest.NewRecorder()
		errorVozVera(w, tc.err)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) || strings.Contains(w.Body.String(), "privado") {
			t.Fatal(w.Code, w.Body)
		}
	}
	router := NewRouterConVera(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{Identidad: auth.UsuarioDePrueba{}}, DependenciasVera{})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/vera/mensajes/voz", strings.NewReader(`{}`)))
	if w.Code != 401 {
		t.Fatal(w.Code, w.Body)
	}
	s := servidorV1{Vera: DependenciasVera{Consentimiento: copilot.NuevoControlConsentimiento(consentimientoUsoPrueba{false}, nil)}}
	ctx := identidad.ConIdentidad(t.Context(), identidad.Identidad{ParteID: uuid.NewString(), UsuarioID: uuid.NewString()})
	w = httptest.NewRecorder()
	s.EnviarMensajeVozVera(w, httptest.NewRequestWithContext(ctx, "POST", "/", strings.NewReader(`{}`)), api.EnviarMensajeVozVeraParams{})
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
}

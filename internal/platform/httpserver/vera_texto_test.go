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

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

type textoVeraPrueba func(context.Context, uuid.UUID, string, uuid.UUID) (copilot.RespuestaTexto, error)

func (f textoVeraPrueba) EnviarTexto(ctx context.Context, conv uuid.UUID, texto string, clave uuid.UUID) (copilot.RespuestaTexto, error) {
	return f(ctx, conv, texto, clave)
}

func TestVeraTextoContratoCerrado(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `[]`, `{"texto":""}`, `{"texto":"  \t"}`, `{"texto":"hola","rol":"sistema"}`, `{"texto":"hola","historial":[]}`, `{"texto":"a","texto":"b"}`, `{"Texto":"hola"}`, `{"texto":"\u0000"}`, `{"texto":"hola","conversacion_id":"invalido"}`, `{"texto":"hola","conversacion_id":"00000000-0000-0000-0000-000000000000"}`, `{"texto":"` + strings.Repeat("á", 1001) + `"}`, "{\"texto\":\"\xff\"}", `{"texto":"a"} {}`} {
		t.Run(body[:min(len(body), 70)], func(t *testing.T) {
			uso := &usoVeraPrueba{}
			s := servidorV1{Vera: DependenciasVera{Consentimiento: copilot.NuevoControlConsentimiento(consentimientoUsoPrueba{true}, nil), Uso: uso}}
			ctx := identidad.ConIdentidad(t.Context(), identidad.Identidad{ParteID: uuid.NewString(), UsuarioID: uuid.NewString()})
			w := httptest.NewRecorder()
			s.EnviarMensajeVera(w, httptest.NewRequestWithContext(ctx, "POST", "/", strings.NewReader(body)), api.EnviarMensajeVeraParams{})
			if w.Code != 422 || uso.llamadas != 0 {
				t.Fatalf("%d %s, uso=%d", w.Code, w.Body, uso.llamadas)
			}
		})
	}
}

func TestVeraTextoRouterYRespuesta(t *testing.T) {
	id := identidad.Identidad{ParteID: uuid.NewString(), UsuarioID: uuid.NewString()}
	conv, clave, mensaje, usuario, accion := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	llamadas := 0
	texto := textoVeraPrueba(func(ctx context.Context, c uuid.UUID, txt string, k uuid.UUID) (copilot.RespuestaTexto, error) {
		llamadas++
		actual, _ := identidad.DeContexto(ctx)
		deadline, ok := ctx.Deadline()
		if actual != id || c != conv || k != clave || txt != strings.Repeat("á", 1000) || !ok || time.Until(deadline) > 45*time.Second {
			t.Error("entrada o plazo incorrectos")
		}
		return copilot.RespuestaTexto{ConversacionID: conv, MensajeUsuarioID: usuario, MensajeID: mensaje, Texto: "Revisa la propuesta.", CreadoEn: time.Date(2026, 10, 18, 16, 0, 0, 0, time.UTC), Accion: &copilot.TarjetaTexto{ID: accion, Estado: "pendiente", Tipo: "transferencia_interna", Nombre: "Laura R.", Celular: "***4321", Monto: 15000000, RequiereCodigo: true, ExpiraEn: time.Now().Add(time.Minute)}}, nil
	})
	router := NewRouterConVera(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{Identidad: identidadDeContextoFijo{id: id}}, DependenciasVera{Texto: texto, Consentimiento: copilot.NuevoControlConsentimiento(consentimientoUsoPrueba{true}, nil), Uso: &usoVeraPrueba{}})
	body, _ := json.Marshal(api.EnviarMensajeVeraRequest{ConversacionId: &conv, Texto: strings.Repeat("á", 1000)})
	r := httptest.NewRequestWithContext(t.Context(), "POST", "/v1/vera/mensajes", strings.NewReader(string(body)))
	r.Header.Set("Idempotency-Key", clave.String())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 200 || llamadas != 1 || w.Header().Get("X-Mensaje-Usuario-Id") != usuario.String() {
		t.Fatalf("%d %s llamadas=%d", w.Code, w.Body, llamadas)
	}
	var response struct {
		Data api.RespuestaAsistente `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.MensajeId != mensaje || response.Data.AccionPendiente.Monto != "15000000" || !strings.Contains(w.Body.String(), "-05:00") {
		t.Fatal(w.Body)
	}
}

func TestVeraTextoErroresSinDetallesPrivados(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{errors.New("secreto de proveedor y SQL"), 503, "AssistantUnavailable"},
		{copilot.ErrConversacionTextoNoEncontrada, 404, "NotFound"},
		{copilot.ErrTextoEnProceso, 409, "ConversationBusy"},
		{copilot.ErrClaveTextoEnConflicto, 409, "IdempotencyConflict"},
		{context.DeadlineExceeded, 504, "Timeout"},
		{&copilot.ErrorLimiteUso{ReintentarEn: time.Second}, 429, "TooManyRequests"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			conv, user := uuid.New(), uuid.New()
			s := servidorV1{Vera: DependenciasVera{Consentimiento: copilot.NuevoControlConsentimiento(consentimientoUsoPrueba{true}, nil), Uso: &usoVeraPrueba{}, Texto: textoVeraPrueba(func(context.Context, uuid.UUID, string, uuid.UUID) (copilot.RespuestaTexto, error) {
				return copilot.RespuestaTexto{ConversacionID: conv, MensajeUsuarioID: user}, tc.err
			})}}
			ctx := identidad.ConIdentidad(t.Context(), identidad.Identidad{ParteID: uuid.NewString(), UsuarioID: uuid.NewString()})
			w := httptest.NewRecorder()
			s.EnviarMensajeVera(w, httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(`{"texto":"Hola"}`)), api.EnviarMensajeVeraParams{})
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) || strings.Contains(w.Body.String(), "secreto") || w.Header().Get("X-Conversacion-Id") != conv.String() {
				t.Fatalf("%d %s %v", w.Code, w.Body, w.Header())
			}
		})
	}
}

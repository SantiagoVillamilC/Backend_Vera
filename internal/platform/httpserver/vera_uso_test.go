package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

type usoVeraPrueba struct {
	err      error
	canal    copilot.CanalAsistente
	llamadas int
}

func (u *usoVeraPrueba) Admitir(_ context.Context, canal copilot.CanalAsistente) error {
	u.canal = canal
	u.llamadas++
	return u.err
}

type consentimientoUsoPrueba struct{ aceptado bool }

func (c consentimientoUsoPrueba) ConsentimientoAsistenteVigente(context.Context, uuid.UUID) (bool, error) {
	return c.aceptado, nil
}

func TestVeraUsoHTTP(t *testing.T) {
	for _, caso := range []struct {
		nombre string
		err    error
		status int
		retry  string
	}{
		{"admitido", nil, 501, ""},
		{"horario", &copilot.ErrorLimiteUso{ReintentarEn: 1500 * time.Millisecond}, 429, "2"},
		{"redis", errors.New("secreto del almacenamiento"), 503, ""},
	} {
		t.Run(caso.nombre, func(t *testing.T) {
			for _, voz := range []bool{false, true} {
				uso := &usoVeraPrueba{err: caso.err}
				srv := servidorV1{Vera: DependenciasVera{Consentimiento: copilot.NuevoControlConsentimiento(consentimientoUsoPrueba{true}, nil), Uso: uso}}
				ctx := identidad.ConIdentidad(t.Context(), identidad.Identidad{ParteID: uuid.NewString(), UsuarioID: uuid.NewString()})
				r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(`{"texto":"Hola"}`))
				w := httptest.NewRecorder()
				canal := copilot.CanalTexto
				if voz {
					canal = copilot.CanalVoz
					srv.EnviarMensajeVozVera(w, r)
				} else {
					srv.EnviarMensajeVera(w, r, api.EnviarMensajeVeraParams{})
				}
				status := caso.status
				if !voz && status == 501 {
					status = 503
				}
				if w.Code != status || w.Header().Get("Retry-After") != caso.retry || uso.canal != canal || uso.llamadas != 1 {
					t.Fatalf("respuesta: %d %s, uso=%+v", w.Code, w.Body, uso)
				}
				if strings.Contains(w.Body.String(), "secreto") {
					t.Fatal("detalle interno expuesto")
				}
			}
		})
	}
}

func TestVeraConsentimientoPrecedeUso(t *testing.T) {
	uso := &usoVeraPrueba{}
	srv := servidorV1{Vera: DependenciasVera{Consentimiento: copilot.NuevoControlConsentimiento(consentimientoUsoPrueba{false}, nil), Uso: uso}}
	ctx := identidad.ConIdentidad(t.Context(), identidad.Identidad{ParteID: uuid.NewString(), UsuarioID: uuid.NewString()})
	w := httptest.NewRecorder()
	srv.EnviarMensajeVera(w, httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(`{"texto":"Hola"}`)), api.EnviarMensajeVeraParams{})
	if w.Code != 403 || uso.llamadas != 0 {
		t.Fatalf("consentimiento: %d, llamadas=%d", w.Code, uso.llamadas)
	}
	srv.Vera.Consentimiento = copilot.NuevoControlConsentimiento(consentimientoUsoPrueba{true}, nil)
	srv.Vera.Uso = nil
	w = httptest.NewRecorder()
	srv.EnviarMensajeVera(w, httptest.NewRequestWithContext(ctx, http.MethodPost, "/", strings.NewReader(`{"texto":"Hola"}`)), api.EnviarMensajeVeraParams{})
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}

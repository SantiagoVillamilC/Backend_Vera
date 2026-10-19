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

type historialVeraPrueba struct {
	llamadas int
	consulta copilot.ConsultaHistorial
	id       uuid.UUID
	err      error
}

func (h *historialVeraPrueba) ListarConversaciones(_ context.Context, p copilot.ConsultaHistorial) (copilot.PaginaHistorial[copilot.ConversacionHistorial], error) {
	h.llamadas++
	h.consulta = p
	siguiente := "cursor-opaco"
	return copilot.PaginaHistorial[copilot.ConversacionHistorial]{Elementos: []copilot.ConversacionHistorial{{ID: uuid.New(), Titulo: "Mi conversación", CreadaEn: time.Now(), UltimaActividad: time.Now()}}, Limite: p.Limite, Siguiente: &siguiente}, h.err
}
func (h *historialVeraPrueba) ListarMensajes(_ context.Context, id uuid.UUID, p copilot.ConsultaHistorial) (copilot.PaginaHistorial[copilot.MensajeHistorial], error) {
	h.llamadas++
	h.id = id
	h.consulta = p
	return copilot.PaginaHistorial[copilot.MensajeHistorial]{Elementos: []copilot.MensajeHistorial{{ID: uuid.New(), Rol: "vera", Canal: "voz", Texto: "Revisa tu propuesta", CreadoEn: time.Now(), Accion: &copilot.TarjetaTexto{ID: uuid.New(), Estado: "cancelada", Tipo: "transferencia_llave", Llave: "***", Monto: 9007199254740993, ExpiraEn: time.Now()}}}, Limite: p.Limite}, h.err
}
func (h *historialVeraPrueba) BorrarConversacion(_ context.Context, id uuid.UUID) error {
	h.llamadas++
	h.id = id
	return h.err
}
func (h *historialVeraPrueba) BorrarConversaciones(context.Context) error { h.llamadas++; return h.err }

func TestVeraHistorialRouterContratoYSinConsentimiento(t *testing.T) {
	id := uuid.New()
	h := &historialVeraPrueba{}
	r := NewRouterConVera(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{Identidad: identidadDeContextoFijo{id: identidad.Identidad{ParteID: uuid.NewString(), UsuarioID: uuid.NewString()}}}, DependenciasVera{Historial: h})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", "/v1/vera/conversaciones?limit=2&cursor=opaco", nil))
	var conv api.ConversacionesVeraResponse
	if err := json.Unmarshal(w.Body.Bytes(), &conv); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(conv.Data) != 1 || !conv.Meta.HasMore || conv.Meta.NextCursor == nil || *conv.Meta.NextCursor != "cursor-opaco" || h.consulta.Limite != 2 || *h.consulta.Cursor != "opaco" || !strings.Contains(w.Body.String(), "-05:00") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", "/v1/vera/conversaciones/"+id.String()+"/mensajes", nil))
	var mensajes api.MensajesVeraResponse
	if err := json.Unmarshal(w.Body.Bytes(), &mensajes); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || h.id != id || h.consulta.Limite != 50 || mensajes.Meta.HasMore || mensajes.Meta.NextCursor != nil || mensajes.Data[0].AccionPendiente.Monto != "9007199254740993" || mensajes.Data[0].AccionPendiente.Estado != "cancelada" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/v1/vera/conversaciones/" + id.String(), "/v1/vera/conversaciones"} {
		w = httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "DELETE", path, nil))
		if w.Code != 204 || w.Body.Len() != 0 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	for _, limite := range []string{"0", "101", "-1", "abc", "999999999999999999999"} {
		antes := h.llamadas
		w = httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", "/v1/vera/conversaciones?limit="+limite, nil))
		if w.Code != 400 || antes != h.llamadas {
			t.Fatalf("límite=%s: %d %s", limite, w.Code, w.Body)
		}
	}
}

func TestVeraHistorialErroresYAutenticacion(t *testing.T) {
	id := uuid.New()
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{copilot.ErrConversacionTextoNoEncontrada, 404, "NotFound"}, {copilot.ErrPaginaHistorial, 400, "BadRequest"},
		{errors.New("postgres://credencial-privada"), 503, "AssistantUnavailable"}, {context.DeadlineExceeded, 504, "Timeout"},
	} {
		h := &historialVeraPrueba{err: tc.err}
		s := servidorV1{Vera: DependenciasVera{Historial: h}}
		w := httptest.NewRecorder()
		s.BorrarConversacionVera(w, httptest.NewRequestWithContext(t.Context(), "DELETE", "/", nil), id)
		assertErrorEnvelope(t, w, tc.status, tc.code)
		if strings.Contains(w.Body.String(), "credencial-privada") {
			t.Fatal("filtró causa privada")
		}
	}
	r := newTestRouter()
	for _, tc := range []struct{ metodo, path string }{
		{"GET", "/v1/vera/conversaciones"}, {"GET", "/v1/vera/conversaciones/" + id.String() + "/mensajes"},
		{"DELETE", "/v1/vera/conversaciones"}, {"DELETE", "/v1/vera/conversaciones/" + id.String()},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), tc.metodo, tc.path, nil))
		assertErrorEnvelope(t, w, http.StatusUnauthorized, "Unauthorized")
		if !esRutaDeDinero(httptest.NewRequestWithContext(t.Context(), tc.metodo, tc.path, nil), RutasDeDinero) {
			t.Fatalf("ruta sin cierre ante denylist caída: %s", tc.path)
		}
	}
}

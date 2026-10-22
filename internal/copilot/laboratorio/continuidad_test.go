package laboratorio

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/evaluacion"
)

type proveedorContinuidadFunc func(context.Context, copilot.SolicitudIA) (copilot.RespuestaIA, error)

func (f proveedorContinuidadFunc) Generar(ctx context.Context, s copilot.SolicitudIA) (copilot.RespuestaIA, error) {
	return f(ctx, s)
}

func TestHTTPVeinteTurnosYBloqueoAudioIndependiente(t *testing.T) {
	n := 0
	p := proveedorContinuidadFunc(func(_ context.Context, s copilot.SolicitudIA) (copilot.RespuestaIA, error) {
		if s.SoloTranscripcion {
			return copilot.RespuestaIA{}, &copilot.ErrorBloqueo{Motivo: copilot.BloqueoSeguridad}
		}
		n++
		if n%2 == 0 {
			return copilot.RespuestaIA{}, &copilot.ErrorBloqueo{Motivo: copilot.BloqueoSeguridad}
		}
		return copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Texto: "Hola, podemos continuar con tu billetera."}}}}, nil
	})
	h, err := Nuevo(evaluacion.ConfigReal{Modelo: "prueba", Repeticiones: 1, MaxSolicitudes: 120, NivelGratuitoConfirmado: true}, p, "token-prueba", "localhost:8787", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost:8787/api/preguntar", strings.NewReader("Hola"))
		r.Header.Set("X-Vera-Token", "token-prueba")
		r.Header.Set("Content-Type", "text/plain")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var sobre struct {
			Success bool
			Data    turno
		}
		if json.Unmarshal(w.Body.Bytes(), &sobre) != nil || w.Code != 200 || !sobre.Success || sobre.Data.Estado.Turnos != i+1 || sobre.Data.RespuestaDelBackend != (i%2 == 1) {
			t.Fatal(i, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost:8787/api/transcribir", bytes.NewReader(wavPrueba()))
	r.Header.Set("X-Vera-Token", "token-prueba")
	r.Header.Set("Content-Type", "audio/wav")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "TranscriptionBlocked") {
		t.Fatal("confundió transcripción con conversación", w.Body.String())
	}
	if n != 20 {
		t.Fatal("el audio ejecutó el chat o se reintentó el bloqueo", n)
	}
}

package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

func TestVozFlujoEntregaAudioAntesDelFin(t *testing.T) {
	recibido := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		if json.NewDecoder(r.Body).Decode(&b) != nil || b["stream"] != true || b["model"] != ModeloVoz {
			t.Error("solicitud de streaming inválida")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"event_type\":\"step.delta\",\"delta\":{\"type\":\"audio\",\"data\":\"AAABAA==\",\"mime_type\":\"audio/l16\",\"sample_rate\":24000}}\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
			return
		}
		select {
		case <-recibido:
		case <-r.Context().Done():
			return
		}
		_, _ = fmt.Fprint(w, "data: {\"type\":\"interaction.completed\"}\n\n")
	}))
	defer srv.Close()
	v, _ := NuevaVoz("clave-ficticia")
	v.endpoint = srv.URL
	v.cliente = srv.Client()
	b, err := v.SintetizarFlujo(t.Context(), "Hola", "Aoede", func(pcm []byte) error {
		if !bytes.Equal(pcm, []byte{0, 0, 1, 0}) {
			t.Error("PCM alterado")
		}
		close(recibido)
		return nil
	})
	if err != nil || len(b) != 48 || string(b[:4]) != "RIFF" || !bytes.Equal(b[44:], []byte{0, 0, 1, 0}) {
		t.Fatalf("WAV final inválido: %v", err)
	}
}

func TestVozFlujoRechazaCortesFormatosYCancelacion(t *testing.T) {
	for _, tc := range []struct{ nombre, datos string }{
		{"corte", `data: {"event_type":"step.delta","delta":{"type":"audio","data":"AAA="}}` + "\n\n"},
		{"formato", `data: {"event_type":"step.delta","delta":{"type":"audio","data":"AAA=","mime_type":"audio/mp3"}}` + "\n\n"},
		{"json", "data: {\n\n"},
		{"sin audio", "data: {\"type\":\"interaction.completed\"}\n\n"},
	} {
		t.Run(tc.nombre, func(t *testing.T) {
			if _, err := leerFlujoVoz(t.Context(), strings.NewReader(tc.datos), func([]byte) error { return nil }); err == nil {
				t.Fatal("aceptó flujo incompleto")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := leerFlujoVoz(ctx, strings.NewReader("data: {}\n\n"), func([]byte) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	errDestino := errors.New("destino cerrado")
	if _, err := leerFlujoVoz(t.Context(), strings.NewReader("data: {\"event_type\":\"step.delta\",\"delta\":{\"type\":\"audio\",\"data\":\"AAA=\"}}\n\n"), func([]byte) error { return errDestino }); !errors.Is(err, errDestino) {
		t.Fatal(err)
	}
	if _, err := leerFlujoVoz(t.Context(), strings.NewReader("data: {\"type\":\"error\"}\n\n"), func([]byte) error { return nil }); !errors.Is(err, copilot.ErrNoDisponibleIA) {
		t.Fatal(err)
	}
}

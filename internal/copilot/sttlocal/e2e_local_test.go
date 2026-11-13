//go:build e2elocal

package sttlocal

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

func TestE2ELocalTranscribir(t *testing.T) {
	if os.Getenv("VERA_STT_E2E") != "1" {
		t.Skip("VERA_STT_E2E distinto de 1")
	}
	url := os.Getenv("VERA_STT_E2E_URL")
	token := os.Getenv("VERA_STT_E2E_TOKEN")
	wav := os.Getenv("VERA_STT_E2E_WAV")
	if url == "" || wav == "" {
		t.Fatal("faltan VERA_STT_E2E_URL o VERA_STT_E2E_WAV")
	}
	if len(token) < 32 || strings.Contains(token, "whisper") || strings.TrimSpace(token) != token {
		t.Fatal("VERA_STT_E2E_TOKEN no cumple el minimo de 32 caracteres")
	}
	audio, err := os.ReadFile(wav)
	if err != nil {
		t.Fatalf("no se pudo leer el WAV: %v", err)
	}
	c, err := Nuevo(url, token, 90*time.Second)
	if err != nil {
		t.Fatalf("Nuevo rechazo la URL: %v", err)
	}
	inicio := time.Now()
	texto, err := c.Transcribir(t.Context(), copilot.AudioIA{MIME: "audio/wav", Datos: audio})
	if err != nil {
		t.Fatalf("Transcribir fallo: %v", err)
	}
	transcurrido := time.Since(inicio)
	if texto == "" {
		t.Fatal("transcripcion vacia")
	}
	t.Logf("clip=%s latency_ms=%d transcript=%q", wav, transcurrido.Milliseconds(), texto)
}

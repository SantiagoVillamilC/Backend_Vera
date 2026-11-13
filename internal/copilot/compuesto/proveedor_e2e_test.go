//go:build e2elocal

package compuesto_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/compuesto"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/ollama"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/sttlocal"
)

func TestE2ELocalCompuesto(t *testing.T) {
	if os.Getenv("VERA_STT_E2E") != "1" {
		t.Skip("VERA_STT_E2E distinto de 1")
	}
	url := os.Getenv("VERA_STT_E2E_URL")
	token := os.Getenv("VERA_STT_E2E_TOKEN")
	wav := os.Getenv("VERA_STT_E2E_WAV")
	ollamaURL := os.Getenv("VERA_OLLAMA_URL")
	modelo := os.Getenv("VERA_OLLAMA_MODELO")
	if url == "" || wav == "" || ollamaURL == "" || modelo == "" {
		t.Fatal("faltan VERA_STT_E2E_URL, VERA_STT_E2E_WAV, VERA_OLLAMA_URL o VERA_OLLAMA_MODELO")
	}
	if len(token) < 32 || strings.Contains(token, "whisper") || strings.TrimSpace(token) != token {
		t.Fatal("VERA_STT_E2E_TOKEN no cumple el minimo de 32 caracteres")
	}
	audio, err := os.ReadFile(wav)
	if err != nil {
		t.Fatalf("no se pudo leer el WAV: %v", err)
	}
	transcriptor, err := sttlocal.Nuevo(url, token, 90*time.Second)
	if err != nil {
		t.Fatalf("sttlocal.Nuevo rechazo la URL: %v", err)
	}
	texto, err := ollama.Nuevo(ollama.Config{URL: ollamaURL, Modelo: modelo, Timeout: 45 * time.Second, Temperatura: 0}, nil)
	if err != nil {
		t.Fatalf("ollama.Nuevo rechazo la configuracion: %v", err)
	}
	p, err := compuesto.Nuevo(texto, transcriptor)
	if err != nil {
		t.Fatalf("compuesto.Nuevo rechazo las dependencias: %v", err)
	}

	inicio := time.Now()
	respuesta, err := p.Generar(t.Context(), copilot.SolicitudIA{
		SoloTranscripcion: true,
		Instrucciones:     "Sólo transcripción JSON.",
		Historial:         []copilot.TurnoIA{{Rol: "usuario", Partes: []copilot.ParteIA{{Audio: &copilot.AudioIA{MIME: "audio/wav", Datos: audio}}}}},
	})
	if err != nil {
		t.Fatalf("transcripcion fallo: %v", err)
	}
	salida := textoUnico(t, respuesta)
	var transcripcion struct {
		Transcripcion string `json:"transcripcion"`
	}
	if err := json.Unmarshal([]byte(salida), &transcripcion); err != nil {
		t.Fatalf("salida no es JSON: %v", err)
	}
	if transcripcion.Transcripcion == "" {
		t.Fatal("transcripcion vacia")
	}
	t.Logf("clip=%s latency_ms=%d transcript=%q", wav, time.Since(inicio).Milliseconds(), transcripcion.Transcripcion)

	inicio = time.Now()
	respuesta, err = p.Generar(t.Context(), copilot.SolicitudIA{
		Instrucciones: "Responde sólo con la palabra ok.",
		Historial:     []copilot.TurnoIA{{Rol: "usuario", Partes: []copilot.ParteIA{{Texto: "Responde solo: ok"}}}},
	})
	if err != nil {
		t.Fatalf("turno de texto fallo: %v", err)
	}
	chat := textoUnico(t, respuesta)
	if chat == "" {
		t.Fatal("turno del asistente vacio")
	}
	t.Logf("modelo=%s chat_latency_ms=%d chat=%q", modelo, time.Since(inicio).Milliseconds(), chat)
}

func textoUnico(t *testing.T, r copilot.RespuestaIA) string {
	t.Helper()
	if r.Turno.Rol != "asistente" {
		t.Fatalf("rol inesperado: %q", r.Turno.Rol)
	}
	for _, parte := range r.Turno.Partes {
		if parte.Texto != "" && parte.Audio == nil && parte.Llamada == nil && parte.Resultado == nil {
			return parte.Texto
		}
	}
	t.Fatalf("turno sin parte de texto: %+v", r.Turno)
	return ""
}

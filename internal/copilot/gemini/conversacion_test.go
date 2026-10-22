package gemini

import (
	"testing"

	"google.golang.org/genai"
)

func TestConversacionNaturalConservaFiltrosSinUmbralMedio(t *testing.T) {
	_, cfg, err := traducirSolicitud(solicitudTexto(), 0.2)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.SafetySettings) != 4 {
		t.Fatal("faltan filtros explícitos")
	}
	for _, s := range cfg.SafetySettings {
		if s.Threshold != genai.HarmBlockThresholdBlockOnlyHigh {
			t.Fatalf("umbral demasiado restrictivo para conversación: %v", s.Threshold)
		}
	}
}

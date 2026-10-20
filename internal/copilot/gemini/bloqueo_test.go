package gemini

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"google.golang.org/genai"
)

func TestBloqueoConservaOrigenSinContenidoPrivado(t *testing.T) {
	for _, tc := range []struct {
		nombre, cuerpo string
		motivo         copilot.MotivoBloqueo
	}{
		{"entrada", `{"promptFeedback":{"blockReason":"SAFETY"}}`, copilot.BloqueoEntrada},
		{"otro", `{"promptFeedback":{"blockReason":"OTHER"}}`, copilot.BloqueoOtro},
		{"seguridad", `{"candidates":[{"finishReason":"SAFETY"}]}`, copilot.BloqueoSeguridad},
		{"recitacion", `{"candidates":[{"finishReason":"RECITATION"}]}`, copilot.BloqueoRecitacion},
		{"datos", `{"candidates":[{"finishReason":"SPII"}]}`, copilot.BloqueoDatos},
		{"rating", `{"candidates":[{"finishReason":"STOP","safetyRatings":[{"category":"HARM_CATEGORY_HARASSMENT","blocked":true}],"content":{"parts":[{"text":"contenido privado"}]}}]}`, copilot.BloqueoSeguridad},
	} {
		t.Run(tc.nombre, func(t *testing.T) {
			var raw genai.GenerateContentResponse
			if err := json.Unmarshal([]byte(tc.cuerpo), &raw); err != nil {
				t.Fatal(err)
			}
			res, err := traducirRespuesta(&raw)
			var b *copilot.ErrorBloqueo
			if !errors.Is(err, copilot.ErrBloqueoIA) || !errors.As(err, &b) || b.Motivo != tc.motivo || len(res.Turno.Partes) != 0 {
				t.Fatalf("bloqueo incorrecto: %v", err)
			}
			if strings.Contains(err.Error(), "contenido privado") {
				t.Fatal("filtró contenido")
			}
		})
	}
}

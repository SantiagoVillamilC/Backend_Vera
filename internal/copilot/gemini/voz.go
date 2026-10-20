package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"google.golang.org/genai"
)

// ModeloVoz usa la oferta TTS con Free Tier publicada por Google; no cambia el modelo del chat.
const ModeloVoz = "gemini-3.8-flash-tts"

// Voz llama a Interactions por REST para separar texto literal y estilo de interpretación.
// No registra credenciales ni texto, no reintenta y no conserva interacciones en Google.
type Voz struct {
	clave    string
	cliente  *http.Client
	endpoint string
}

// NuevaVoz reutiliza la clave de Gemini sin realizar solicitudes al construir el adaptador.
func NuevaVoz(clave string) (*Voz, error) {
	if strings.TrimSpace(clave) == "" {
		return nil, copilot.ErrCredencialesIA
	}
	return &Voz{clave: clave, cliente: &http.Client{Timeout: 45 * time.Second}, endpoint: "https://generativelanguage.googleapis.com/v1beta/interactions"}, nil
}

// Sintetizar devuelve WAV mono de 24 kHz generado a partir del texto final de Vera.
func (v *Voz) Sintetizar(ctx context.Context, texto, voz string) ([]byte, error) {
	if !utf8.ValidString(texto) || strings.TrimSpace(texto) == "" || len(texto) > 8192 || (voz != "Aoede" && voz != "Kore" && voz != "Puck") {
		return nil, copilot.ErrSolicitudIA
	}
	cuerpo, err := json.Marshal(map[string]any{
		"model": ModeloVoz, "store": false,
		"input": []any{map[string]any{"type": "user_input", "content": []any{map[string]any{
			"type": "text", "text": texto, "annotations": []any{map[string]any{"type": "speech_metadata", "style": "Español de Colombia, conversación cercana y natural. Voz cálida, tranquila, ritmo fluido y pausas breves. Como una persona respondiendo, sin tono de locutora ni exageración. Pronuncia claramente las cifras y COP como pesos colombianos."}},
		}}}},
		"response_format":   map[string]any{"type": "audio", "mime_type": "audio/wav", "sample_rate": 24000},
		"generation_config": map[string]any{"speech_config": []any{map[string]any{"voice": voz}}},
	})
	if err != nil {
		return nil, copilot.ErrSolicitudIA
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.endpoint, bytes.NewReader(cuerpo))
	if err != nil {
		return nil, copilot.ErrSolicitudIA
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", v.clave)
	r, err := v.cliente.Do(req)
	if err != nil {
		_, seguro := clasificar(err)
		return nil, seguro
	}
	defer func() { _ = r.Body.Close() }()
	if r.StatusCode != http.StatusOK {
		_, seguro := clasificar(genai.APIError{Code: r.StatusCode})
		return nil, seguro
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, 8*1024*1024+1))
	if err != nil || len(b) > 8*1024*1024 {
		return nil, copilot.ErrRespuestaIA
	}
	var respuesta struct {
		Steps []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				MIME string `json:"mime_type"`
				Data []byte `json:"data"`
			} `json:"content"`
		} `json:"steps"`
	}
	if json.Unmarshal(b, &respuesta) != nil {
		return nil, copilot.ErrRespuestaIA
	}
	for i := len(respuesta.Steps) - 1; i >= 0; i-- {
		paso := respuesta.Steps[i]
		if paso.Type != "model_output" {
			continue
		}
		for j := len(paso.Content) - 1; j >= 0; j-- {
			a := paso.Content[j]
			if a.Type == "audio" && a.MIME == "audio/wav" && len(a.Data) >= 44 && len(a.Data) <= 4*1024*1024 && string(a.Data[:4]) == "RIFF" && string(a.Data[8:12]) == "WAVE" {
				return a.Data, nil
			}
		}
	}
	return nil, copilot.ErrRespuestaIA
}

var _ copilot.SintetizadorVoz = (*Voz)(nil)

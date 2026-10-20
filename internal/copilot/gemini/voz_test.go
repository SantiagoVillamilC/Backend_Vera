package gemini

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

func TestVozEnviaTextoLiteralYRecibeWAV(t *testing.T) {
	wav := make([]byte, 46)
	copy(wav, "RIFF")
	copy(wav[8:], "WAVE")
	const texto = "Tienes 120000.00 COP disponibles."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "clave-ficticia" || r.Method != http.MethodPost {
			t.Error("credencial o método incorrecto")
		}
		var entrada struct {
			Model string `json:"model"`
			Store bool   `json:"store"`
			Input []struct {
				Content []struct {
					Text        string `json:"text"`
					Annotations []struct {
						Style string `json:"style"`
					} `json:"annotations"`
				} `json:"content"`
			} `json:"input"`
			Config struct {
				Speech []struct {
					Voice string `json:"voice"`
				} `json:"speech_config"`
			} `json:"generation_config"`
		}
		if json.NewDecoder(r.Body).Decode(&entrada) != nil {
			t.Error("JSON inválido")
			return
		}
		if entrada.Model != ModeloVoz || entrada.Store || len(entrada.Input) != 1 || len(entrada.Input[0].Content) != 1 || entrada.Input[0].Content[0].Text != texto || len(entrada.Input[0].Content[0].Annotations) != 1 || len(entrada.Config.Speech) != 1 || entrada.Config.Speech[0].Voice != "Aoede" {
			t.Errorf("contrato de voz incorrecto: %+v", entrada)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"steps": []any{map[string]any{"type": "model_output", "content": []any{map[string]any{"type": "audio", "mime_type": "audio/wav", "data": wav}}}}})
	}))
	defer srv.Close()
	v, _ := NuevaVoz("clave-ficticia")
	v.cliente = srv.Client()
	v.endpoint = srv.URL
	b, err := v.Sintetizar(t.Context(), texto, "Aoede")
	if err != nil || !bytes.Equal(b, wav) {
		t.Fatalf("audio incorrecto: %v", err)
	}
}

func TestVozNoReintentaYRechazaSalidaInvalida(t *testing.T) {
	for _, tc := range []struct {
		nombre   string
		status   int
		cuerpo   string
		esperado error
	}{
		{"cuota", 429, `{"error":"dato privado"}`, copilot.ErrLimiteIA},
		{"credenciales", 403, `{}`, copilot.ErrCredencialesIA},
		{"sin audio", 200, `{"steps":[]}`, copilot.ErrRespuestaIA},
		{"json inválido", 200, `no es JSON`, copilot.ErrRespuestaIA},
	} {
		t.Run(tc.nombre, func(t *testing.T) {
			var llamadas atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				llamadas.Add(1)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.cuerpo))
			}))
			defer srv.Close()
			v, _ := NuevaVoz("clave-ficticia")
			v.cliente = srv.Client()
			v.endpoint = srv.URL
			if _, err := v.Sintetizar(t.Context(), "Hola", "Aoede"); !errors.Is(err, tc.esperado) || llamadas.Load() != 1 {
				t.Fatalf("error=%v llamadas=%d", err, llamadas.Load())
			}
			if _, err := v.Sintetizar(t.Context(), "Hola", "voz-ajena"); !errors.Is(err, copilot.ErrSolicitudIA) || llamadas.Load() != 1 {
				t.Fatal("la voz inválida consumió una solicitud")
			}
		})
	}
}

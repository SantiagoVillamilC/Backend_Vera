package gemini

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

func TestContratoTranscripcionAudioInlineSinFunciones(t *testing.T) {
	for _, formato := range []string{"wav", "m4a"} {
		t.Run(formato, func(t *testing.T) {
			b, err := os.ReadFile("../testdata/audio/tono." + formato)
			if err != nil {
				t.Fatal(err)
			}
			a, err := copilot.ValidarAudio(base64.StdEncoding.EncodeToString(b), formato)
			if err != nil {
				t.Fatal(err)
			}
			p := proveedorPrueba(t, func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Contents []struct {
						Parts []struct {
							InlineData struct {
								MIME string `json:"mimeType"`
								Data []byte `json:"data"`
							} `json:"inlineData"`
						} `json:"parts"`
					} `json:"contents"`
					Config struct {
						MIME   string          `json:"responseMimeType"`
						Schema json.RawMessage `json:"responseJsonSchema"`
					} `json:"generationConfig"`
					Tools json.RawMessage `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Config.MIME != "application/json" || len(body.Config.Schema) == 0 || len(body.Tools) > 0 || len(body.Contents) != 1 || len(body.Contents[0].Parts) != 1 || body.Contents[0].Parts[0].InlineData.MIME != a.MIME || !bytes.Equal(body.Contents[0].Parts[0].InlineData.Data, b) {
					t.Error("se alteró audio o se habilitaron funciones")
				}
				_, _ = fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"{\"transcripcion\":\"Consultar saldo\"}"}]},"finishReason":"STOP"}]}`)
			})
			r, err := p.Generar(t.Context(), copilot.SolicitudIA{SoloTranscripcion: true, Instrucciones: "Sólo transcripción JSON.", Historial: []copilot.TurnoIA{{Rol: "usuario", Partes: []copilot.ParteIA{{Audio: a}}}}})
			if err != nil || r.Turno.Partes[0].Texto != `{"transcripcion":"Consultar saldo"}` {
				t.Fatalf("%+v %v", r, err)
			}
		})
	}
}

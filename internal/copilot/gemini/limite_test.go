package gemini

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

func TestPerfilInteractivoOmiteTemperaturaForzada(t *testing.T) {
	p := proveedorPrueba(t, func(w http.ResponseWriter, r *http.Request) {
		var entrada map[string]any
		if json.NewDecoder(r.Body).Decode(&entrada) != nil {
			t.Error("JSON inválido")
		}
		generacion := entrada["generationConfig"].(map[string]any)
		if _, existe := generacion["temperature"]; existe {
			t.Error("forzó temperatura en el perfil interactivo")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respuestaTexto))
	})
	p.cfg.TemperaturaPredeterminada = true
	if _, err := p.Generar(t.Context(), solicitudTexto()); err != nil {
		t.Fatal(err)
	}
}

func TestCuotaClasificaDimensionYEsperaSinMensajesDelSDK(t *testing.T) {
	for _, tc := range []struct {
		metrica string
		clase   copilot.ClaseLimite
	}{
		{"generate_requests_per_model_per_minute", copilot.LimiteMinuto},
		{"GenerateRequestsPerDayPerProjectPerModel-FreeTier", copilot.LimiteDia},
		{"desconocida", copilot.LimiteDesconocido},
	} {
		t.Run(tc.metrica, func(t *testing.T) {
			err := limiteProveedor([]map[string]any{
				{"@type": "type.googleapis.com/google.rpc.QuotaFailure", "violations": []any{map[string]any{"quotaMetric": tc.metrica, "description": "dato privado"}}},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "3.5s"},
			})
			var e *copilot.ErrorLimiteIA
			if !errors.Is(err, copilot.ErrLimiteIA) || !errors.As(err, &e) || e.Clase != tc.clase || e.ReintentarEn != 3500*time.Millisecond {
				t.Fatal(err, e)
			}
			if err.Error() != string(copilot.ErrLimiteIA) {
				t.Fatal("expuso detalles privados")
			}
		})
	}
	err := limiteProveedor([]map[string]any{{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "999999h"}})
	var e *copilot.ErrorLimiteIA
	_ = errors.As(err, &e)
	if e.ReintentarEn != time.Minute {
		t.Fatal("aceptó espera no acotada")
	}
}

package gemini

import (
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

func limiteProveedor(detalles []map[string]any) error {
	e := &copilot.ErrorLimiteIA{Clase: copilot.LimiteDesconocido, ReintentarEn: time.Minute}
	for _, d := range detalles {
		tipo, _ := d["@type"].(string)
		if tipo == "type.googleapis.com/google.rpc.RetryInfo" {
			s, _ := d["retryDelay"].(string)
			if espera, err := time.ParseDuration(s); err == nil && espera > 0 && espera <= 24*time.Hour {
				e.ReintentarEn = espera
			}
		}
		if tipo != "type.googleapis.com/google.rpc.QuotaFailure" {
			continue
		}
		violaciones, _ := d["violations"].([]any)
		for _, v := range violaciones {
			m, _ := v.(map[string]any)
			metrica, _ := m["quotaMetric"].(string)
			id, _ := m["quotaId"].(string)
			texto := strings.ToLower(metrica + " " + id)
			if strings.Contains(texto, "per_day") || strings.Contains(texto, "perday") {
				e.Clase = copilot.LimiteDia
			}
			if e.Clase != copilot.LimiteDia && (strings.Contains(texto, "per_minute") || strings.Contains(texto, "perminute")) {
				e.Clase = copilot.LimiteMinuto
			}
		}
	}
	return e
}

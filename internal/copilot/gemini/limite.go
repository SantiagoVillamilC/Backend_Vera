package gemini

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

// Interactions puede omitir google.rpc.RetryInfo y comunicar la espera sólo
// mediante HTTP. No inferimos la dimensión diaria a partir de la duración.
func limiteConCabecera(detalles []map[string]any, cabecera string, ahora time.Time) error {
	err := limiteProveedor(detalles)
	var e *copilot.ErrorLimiteIA
	if !errors.As(err, &e) {
		return err
	}
	var espera time.Duration
	if segundos, fallo := strconv.Atoi(cabecera); fallo == nil && segundos > 0 && segundos <= 86400 {
		espera = time.Duration(segundos) * time.Second
	} else if fecha, fallo := http.ParseTime(cabecera); fallo == nil {
		espera = fecha.Sub(ahora)
	}
	if espera > 0 && espera <= 24*time.Hour {
		e.ReintentarEn = espera
	}
	return e
}

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

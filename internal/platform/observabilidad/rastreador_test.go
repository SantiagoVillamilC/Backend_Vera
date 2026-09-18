package observabilidad

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
)

// HU-NF-08, criterio 24: la captura estructurada lleva operación, canal y los campos que ubican la diferencia.
func TestRastreadorSlogEmiteErrorConOperacionCanalYCampos(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	capturar(slog.New(slog.NewJSONHandler(&buf, nil)), t.Context(),
		errors.New("descuadre sin resolver"),
		nucleo.ContextoError{
			Operacion: "conciliar",
			Canal:     nucleo.CanalSoloLog,
			Campos: []nucleo.Campo{
				{Clave: "descuadre_id", Valor: "d-1"},
				{Clave: "tipo", Valor: "monto_distinto"},
				{Clave: "monto_interno", Valor: "100000"},
				{Clave: "monto_externo", Valor: "99999"},
				{Clave: "ventana_desde", Valor: "2026-09-15T10:00:00Z"},
				{Clave: "detectado_en", Valor: "2026-09-15T10:06:30Z"},
			},
		})

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("el relleno no emitió una línea JSON: %v (%q)", err, buf.String())
	}
	if rec["level"] != "ERROR" {
		t.Errorf("level = %v, quiero ERROR", rec["level"])
	}
	if rec["operacion"] != "conciliar" {
		t.Errorf("operacion = %v, quiero conciliar", rec["operacion"])
	}
	if rec["canal"] != nucleo.CanalSoloLog {
		t.Errorf("canal = %v, quiero %q", rec["canal"], nucleo.CanalSoloLog)
	}
	contexto, ok := rec["contexto"].(map[string]any)
	if !ok {
		t.Fatalf("contexto = %v, quiero un grupo de campos", rec["contexto"])
	}
	for clave, quiero := range map[string]string{
		"descuadre_id":  "d-1",
		"tipo":          "monto_distinto",
		"monto_interno": "100000",
		"monto_externo": "99999",
		"ventana_desde": "2026-09-15T10:00:00Z",
		"detectado_en":  "2026-09-15T10:06:30Z",
	} {
		if contexto[clave] != quiero {
			t.Errorf("contexto[%q] = %v, quiero %q", clave, contexto[clave], quiero)
		}
	}
	if rec["err"] != "descuadre sin resolver" {
		t.Errorf("err = %v, quiero descuadre sin resolver", rec["err"])
	}
}

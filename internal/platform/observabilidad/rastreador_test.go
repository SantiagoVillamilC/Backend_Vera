package observabilidad

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
)

func TestRastreadorSlogEmiteErrorConOperacionYCampos(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	capturar(slog.New(slog.NewJSONHandler(&buf, nil)), t.Context(),
		errors.New("descuadre sin resolver"),
		nucleo.ContextoError{
			Operacion: "Conciliar",
			Campos:    []nucleo.Campo{{Clave: "descuadre_id", Valor: "d-1"}},
		})

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("el relleno no emitió una línea JSON: %v (%q)", err, buf.String())
	}
	if rec["level"] != "ERROR" {
		t.Errorf("level = %v, quiero ERROR", rec["level"])
	}
	if rec["operacion"] != "Conciliar" {
		t.Errorf("operacion = %v, quiero Conciliar", rec["operacion"])
	}
	contexto, ok := rec["contexto"].(map[string]any)
	if !ok || contexto["descuadre_id"] != "d-1" {
		t.Errorf("contexto = %v, quiero descuadre_id=d-1", rec["contexto"])
	}
	if rec["err"] != "descuadre sin resolver" {
		t.Errorf("err = %v, quiero descuadre sin resolver", rec["err"])
	}
}

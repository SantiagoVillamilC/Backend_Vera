package main

import (
	"strings"
	"testing"
)

func TestConfigLimitesVera(t *testing.T) {
	nombres := []string{"VERA_LIMITE_TEXTO_HORA", "VERA_LIMITE_TEXTO_DIA", "VERA_LIMITE_VOZ_HORA", "VERA_TOKENS_DIARIOS", "VERA_TOKENS_RESERVA_LLAMADA"}
	for _, n := range nombres {
		t.Setenv(n, "")
	}
	l, err := cargarLimitesVera()
	if err != nil || l.TextoHora != 30 || l.TextoDia != 150 || l.VozHora != 10 || l.TokensDiarios != 100000 || l.ReservaTokens != 4096 {
		t.Fatalf("defaults: %+v, %v", l, err)
	}
	for _, nombre := range nombres {
		for _, valor := range []string{"0", "-1", "NaN", "1.5", "1000000000001"} {
			t.Run(nombre+valor, func(t *testing.T) {
				t.Setenv(nombre, valor)
				_, err := cargarLimitesVera()
				if err == nil || !strings.Contains(err.Error(), nombre) {
					t.Fatal(err)
				}
			})
		}
	}
	t.Setenv("VERA_TOKENS_DIARIOS", "5")
	if _, err := cargarLimitesVera(); err == nil {
		t.Fatal("reserva superior al presupuesto")
	}
	t.Setenv("VERA_TOKENS_RESERVA_LLAMADA", "2")
	if _, err := cargarLimitesVera(); err != nil {
		t.Fatal(err)
	}
	_, errs := cargarConfigVera()
	if len(errs) != 0 {
		t.Fatal(errs)
	}
}

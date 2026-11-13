package main

import (
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/avisos"
)

func TestCupoDesdeEntorno(t *testing.T) {
	t.Parallel()
	if cupo, err := cupoDesdeEntorno(""); err != nil || cupo != avisos.CupoMensualPorDefecto {
		t.Fatalf("vacío dio (%d, %v), quiero (%d, nil)", cupo, err, avisos.CupoMensualPorDefecto)
	}
	if cupo, err := cupoDesdeEntorno("5"); err != nil || cupo != 5 {
		t.Fatalf("5 dio (%d, %v), quiero (5, nil)", cupo, err)
	}
	for _, valor := range []string{"0", "-1", "tres", "5.5", " 5"} {
		t.Run(valor, func(t *testing.T) {
			t.Parallel()
			if _, err := cupoDesdeEntorno(valor); err == nil || !strings.Contains(err.Error(), "AVISOS_CUPO") {
				t.Fatalf("%q dio err = %v, quiero un error de AVISOS_CUPO", valor, err)
			}
		})
	}
}

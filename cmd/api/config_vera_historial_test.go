package main

import (
	"strings"
	"testing"
)

func TestConfigRetencionVera(t *testing.T) {
	for _, valor := range []string{"", "1", "7", "36500", "0", "-1", "36501", "no", "1.5", "999999999999999999999"} {
		t.Run(valor, func(t *testing.T) {
			t.Setenv("VERA_RETENCION_DIAS", valor)
			c, errs := cargarConfigVera()
			fallo := false
			for _, err := range errs {
				fallo = fallo || strings.Contains(err.Error(), "VERA_RETENCION_DIAS")
			}
			valido := valor == "" || valor == "1" || valor == "7" || valor == "36500"
			if fallo == valido {
				t.Fatalf("config=%+v errores=%v", c, errs)
			}
			if valor == "" && c.RetencionDias != 30 {
				t.Fatalf("defecto=%d", c.RetencionDias)
			}
		})
	}
}

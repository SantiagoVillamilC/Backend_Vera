package main

import (
	"strings"
	"testing"
)

func TestConfigVeraPreparacion(t *testing.T) {
	t.Setenv("VERA_UMBRAL_CODIGO", "")
	t.Setenv("VERA_LLAVES_HABILITADAS", "")
	c, errs := cargarConfigVera()
	if len(errs) > 0 || c.Preparacion.UmbralCodigo != 50000000 || c.Preparacion.LlavesHabilitadas {
		t.Fatalf("defaults: %+v %v", c.Preparacion, errs)
	}
	for _, texto := range []string{"0", "-1", "500.000", "5e5", "1.5", "92233720368547759"} {
		t.Run(texto, func(t *testing.T) {
			t.Setenv("VERA_UMBRAL_CODIGO", texto)
			_, errs := cargarConfigVera()
			var encontrado bool
			for _, err := range errs {
				encontrado = encontrado || strings.Contains(err.Error(), "VERA_UMBRAL_CODIGO")
			}
			if !encontrado {
				t.Fatal("aceptó umbral inválido")
			}
		})
	}
	t.Setenv("VERA_UMBRAL_CODIGO", "100000")
	t.Setenv("VERA_LLAVES_HABILITADAS", "true")
	c, errs = cargarConfigVera()
	if len(errs) > 0 || c.Preparacion.UmbralCodigo != 10000000 || !c.Preparacion.LlavesHabilitadas {
		t.Fatalf("personalizada: %+v %v", c.Preparacion, errs)
	}
	t.Setenv("VERA_LLAVES_HABILITADAS", "talvez")
	_, errs = cargarConfigVera()
	var encontrado bool
	for _, err := range errs {
		encontrado = encontrado || strings.Contains(err.Error(), "VERA_LLAVES_HABILITADAS")
	}
	if !encontrado {
		t.Fatal("aceptó bandera inválida")
	}
}

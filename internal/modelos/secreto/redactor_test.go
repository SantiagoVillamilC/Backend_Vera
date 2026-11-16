package secreto

import (
	"strings"
	"testing"
)

func TestLimpiarOcultaSecretosYControl(t *testing.T) {
	r := Nuevo("rpa_CLAVE_SECRETA_123", "otro-secreto-largo", "corto")
	casos := []struct{ nombre, entrada, esperado string }{
		{"oculta la clave", "fallo con rpa_CLAVE_SECRETA_123 en la peticion", "fallo con *** en la peticion"},
		{"oculta varias veces", "otro-secreto-largo y otro-secreto-largo", "*** y ***"},
		{"ignora valores demasiado cortos", "algo corto aqui", "algo corto aqui"},
		{"quita caracteres de control", "linea\x1b[31m roja\r\nsiguiente", "linea[31m roja  siguiente"},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := r.Limpiar(c.entrada); got != c.esperado {
				t.Fatalf("Limpiar(%q) = %q, esperado %q", c.entrada, got, c.esperado)
			}
		})
	}
}

func TestRecortarRespetaRunas(t *testing.T) {
	if got := Recortar("abcdef", 3); got != "abc..." {
		t.Fatalf("got %q", got)
	}
	if got := Recortar("ñandú", 2); got != "ña..." {
		t.Fatalf("got %q", got)
	}
	if got := Recortar("ab", 5); got != "ab" {
		t.Fatalf("got %q", got)
	}
}

func TestRedactorVacioNoRompe(t *testing.T) {
	if got := Nuevo().Limpiar("hola"); got != "hola" {
		t.Fatal(got)
	}
	if strings.Contains(Nuevo("").Limpiar("x"), "***") {
		t.Fatal("un secreto vacio no debe reemplazar nada")
	}
}

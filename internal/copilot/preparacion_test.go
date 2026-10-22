package copilot

import (
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
)

func TestLlavePreparacionNoAdivinaTiposNumericos(t *testing.T) {
	for texto, tipo := range map[string]string{"@laura": nucleo.TipoLlaveAlfanumerico, "laura@example.com": nucleo.TipoLlaveCorreo, "+573001234567": nucleo.TipoLlaveCelular, "celular:3001234567": nucleo.TipoLlaveCelular, "documento:1234567": nucleo.TipoLlaveDocumento, "comercio:ABC12345": nucleo.TipoLlaveComercio} {
		llave, err := llaveParaPreparacion(texto)
		if err != nil || llave.Tipo != tipo {
			t.Fatalf("%q: %+v %v", texto, llave, err)
		}
	}
	for _, texto := range []string{"3001234567", "1234567", "invalido:ABC12345", "@", "celular:555", "+57300", " ", "comercio:???"} {
		if llave, err := llaveParaPreparacion(texto); err == nil {
			t.Fatalf("adivinó %q: %+v", texto, llave)
		}
	}
}

func TestCelularEnmascaradoConservaUltimosDigitos(t *testing.T) {
	for _, celular := range []string{"+573001234567", "*******567"} {
		if salida := celularParaModelo(celular); salida != "*******567" {
			t.Fatalf("%q -> %q", celular, salida)
		}
	}
	if salida := celularParaModelo("*******abc"); salida != "***" {
		t.Fatal(salida)
	}
}

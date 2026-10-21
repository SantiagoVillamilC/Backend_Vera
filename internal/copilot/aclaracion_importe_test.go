package copilot

import "testing"

func TestImporteAisladoNoInfiereEscalaNiConfundeSeleccion(t *testing.T) {
	for texto, aclarar := range map[string]bool{"Pues cincuenta.": true, "Eh, cincuenta": true, "treinta": true, "50 mil pesos": false, "cincuenta mil": false, "1": false, "la primera": false, "¿Qué significa saldo?": false} {
		t.Run(texto, func(t *testing.T) {
			h := []TurnoIA{{Rol: "usuario", Partes: []ParteIA{{Texto: texto}}}}
			if (aclaracionImporteAislado(h) != "") != aclarar {
				t.Fatal("aclaración incorrecta")
			}
		})
	}
}

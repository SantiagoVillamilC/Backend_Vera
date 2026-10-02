package auth

import "testing"

// HU-NF-06 C15.1: persona muestra el nombre de pila y solo una inicial del apellido.
func TestHU_NF_06_C15_1_NombreEnmascarado(t *testing.T) {
	casos := map[string]string{
		"Laura Ramirez":                 "Laura R.",
		"  Laura   María   Rodríguez  ": "Laura R.",
		"Laura María Rodríguez Pérez":   "Laura R.",
		"Ángela Ñúñez":                  "Ángela Ñ.",
		"Laura":                         "Laura",
		"   ":                           "",
	}
	for entrada, esperado := range casos {
		if got := nombreEnmascarado(entrada); got != esperado {
			t.Errorf("nombreEnmascarado(%q) = %q, quiero %q", entrada, got, esperado)
		}
	}
}

package auth

import "testing"

// HU-NF-06 C15.1: persona muestra el nombre de pila y solo una inicial del apellido.
func TestHU_NF_06_C15_1_NombreEnmascarado(t *testing.T) {
	casos := map[string]string{
		"Laura Ramirez": "Laura R.",
		// Valor esperado cambió de "Laura R." a "Laura M." por regla para 3 o más componentes,
		// a la espera de que Santiago confirme si el segundo componente es apellido o segundo nombre (S5)
		"  Laura   María   Rodríguez  ": "Laura M.",
		"Laura Rodríguez Pérez":         "Laura R.",
		"Juan Carlos Rodríguez Pérez":   "Juan R.",
		"Laura María Rodríguez Pérez":   "Laura R.",
		"Laura Rodríguez":               "Laura R.",
		"Ana Ñandú Óscar":               "Ana Ñ.",
		"Álvaro Núñez Peña López Ruiz":  "Álvaro L.",
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

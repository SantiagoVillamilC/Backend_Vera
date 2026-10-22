package nucleo

import "testing"

func TestParsearPesosEnteros(t *testing.T) {
	for texto, esperado := range map[string]Centavos{"1": 100, "500000": 50000000, "92233720368547758": 9223372036854775800} {
		monto, err := ParsearPesosEnteros(texto)
		if err != nil || monto != esperado {
			t.Fatalf("%q: %d %v", texto, monto, err)
		}
	}
	for _, texto := range []string{"", "0", "-1", "+1", " 1", "1 ", "01", "1.5", "1,000", "1e3", "92233720368547759", "9999999999999999999999999999999999999", "cincuenta"} {
		if monto, err := ParsearPesosEnteros(texto); err == nil {
			t.Fatalf("aceptó %q: %d", texto, monto)
		}
	}
}

package inteligencia

import "testing"

func TestDireccionDeNeto(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		neto     int64
		esperado string
	}{
		"neto negativo es entrada": {neto: -500000, esperado: "entrada"},
		"neto positivo es salida":  {neto: 500000, esperado: "salida"},
		"neto cero es salida":      {neto: 0, esperado: "salida"},
		"negativo mínimo":          {neto: -1, esperado: "entrada"},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			if obtenido := direccionDeNeto(caso.neto); obtenido != caso.esperado {
				t.Errorf("direccionDeNeto(%d) = %q, want %q", caso.neto, obtenido, caso.esperado)
			}
		})
	}
}

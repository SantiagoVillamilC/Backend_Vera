package inteligencia

import "testing"

// lexicoDePrueba arma un léxico pequeño con una palabra genérica en conflicto.
func lexicoDePrueba() []ParLexico {
	return []ParLexico{
		{Palabra: "CENTRAL", Categoria: CategoriaComisiones},
		{Palabra: "DROGUERIA", Categoria: CategoriaSalud},
		{Palabra: "PANADERIA", Categoria: CategoriaAlimentosMercado},
		{Palabra: "NOMINA", Categoria: SinCategoria},
	}
}

// TestCoincidenciasDelLexicoSeparaConflictoDeAusencia fija la lectura que usa la medición.
func TestCoincidenciasDelLexicoSeparaConflictoDeAusencia(t *testing.T) {
	t.Parallel()
	lexico := lexicoDePrueba()
	casos := map[string]int{
		"DROGUERIA CENTRAL":      2,
		"PANADERIA LA ESPERANZA": 1,
		"TIENDA SIN OFICIO":      0,
		"PANADERIA PANADERIA":    1,
		"NOMINA DE DICIEMBRE":    0,
	}
	for nombre, quiero := range casos {
		coincidencias := CoincidenciasDelLexico(lexico, nombre)
		if len(coincidencias) != quiero {
			t.Fatalf("%q: %d coincidencias, quiero %d: %+v", nombre, len(coincidencias), quiero, coincidencias)
		}
	}
	if CoincidenciasDelLexico(lexico, "") != nil {
		t.Fatal("un nombre vacío no debería devolver coincidencias")
	}
	conflicto := CoincidenciasDelLexico(lexico, "DROGUERIA CENTRAL")
	if conflicto[0].Palabra != "CENTRAL" || conflicto[1].Palabra != "DROGUERIA" {
		t.Fatalf("el orden no es estable: %+v", conflicto)
	}
}

// TestPorLexicoNoCambiaConLaExtraccion mantiene la decisión de producción idéntica.
func TestPorLexicoNoCambiaConLaExtraccion(t *testing.T) {
	t.Parallel()
	lexico := lexicoDePrueba()
	if categoria, decidida := porLexico(lexico, "PANADERIA LA ESPERANZA"); !decidida ||
		categoria != CategoriaAlimentosMercado {
		t.Fatalf("una sola categoría: %q %v", categoria, decidida)
	}
	if _, decidida := porLexico(lexico, "DROGUERIA CENTRAL"); decidida {
		t.Fatal("dos categorías no deberían decidir")
	}
	if _, decidida := porLexico(lexico, "TIENDA SIN OFICIO"); decidida {
		t.Fatal("sin palabra no debería decidir")
	}
}

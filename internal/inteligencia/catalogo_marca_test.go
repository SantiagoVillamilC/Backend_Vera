package inteligencia

import "testing"

func TestConjuntoVisibleRechazaLaListaVacia(t *testing.T) {
	t.Parallel()

	if _, err := conjuntoVisible(nil); err == nil {
		t.Fatal("conjuntoVisible aceptó una lista sin categorías visibles")
	}
}

func TestConjuntoVisibleArmaElConjunto(t *testing.T) {
	t.Parallel()

	validas, err := conjuntoVisible([]string{string(CategoriaSalud), string(CategoriaCompras)})
	if err != nil {
		t.Fatalf("conjuntoVisible: %v", err)
	}
	if _, existe := validas[string(CategoriaSalud)]; !existe {
		t.Fatalf("conjunto = %v, want %q", validas, CategoriaSalud)
	}
	if _, existe := validas[string(SinCategoria)]; existe {
		t.Fatalf("conjunto = %v, want que %q no entre", validas, SinCategoria)
	}
}

func TestLasCategoriasVisiblesSonLasDelCatalogoFijo(t *testing.T) {
	t.Parallel()

	if len(categoriasVisibles) != 14 {
		t.Fatalf("categorías visibles = %d, want 14", len(categoriasVisibles))
	}
	vistas := make(map[Categoria]bool, len(categoriasVisibles))
	for _, codigo := range categoriasVisibles {
		if vistas[codigo] {
			t.Errorf("código visible repetido: %q", codigo)
		}
		vistas[codigo] = true
		if _, existe := BuscarCategoria(codigo); !existe {
			t.Errorf("%q no está en el catálogo fijo", codigo)
		}
		if codigo == SinCategoria {
			t.Errorf("%q no puede estar entre las visibles", SinCategoria)
		}
	}
}

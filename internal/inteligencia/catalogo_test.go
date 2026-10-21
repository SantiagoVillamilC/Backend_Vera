package inteligencia

import "testing"

func TestCatalogoFijoEstaCompletoYOrdenado(t *testing.T) {
	catalogo := CatalogoFijo()
	if len(catalogo) != 15 {
		t.Fatalf("len(CatalogoFijo()) = %d, want 15", len(catalogo))
	}
	codigos := make(map[Categoria]bool, len(catalogo))
	nombres := make(map[string]bool, len(catalogo))
	grupos := map[string]int{}
	for indice, categoria := range catalogo {
		if categoria.Orden != int16(indice+1) {
			t.Errorf("categoría %q tiene orden %d, want %d", categoria.Codigo, categoria.Orden, indice+1)
		}
		if codigos[categoria.Codigo] {
			t.Errorf("código repetido: %q", categoria.Codigo)
		}
		if nombres[categoria.Nombre] {
			t.Errorf("nombre repetido: %q", categoria.Nombre)
		}
		if categoria.Nombre == "" {
			t.Errorf("categoría %q sin nombre visible", categoria.Codigo)
		}
		codigos[categoria.Codigo] = true
		nombres[categoria.Nombre] = true
		grupos[categoria.Grupo]++
	}
	if grupos["ingreso"] != 4 || grupos["gasto"] != 10 || grupos["ninguno"] != 1 {
		t.Errorf("grupos = %v, want 4 ingreso, 10 gasto y 1 ninguno", grupos)
	}
}

func TestSinCategoriaNoEsVisible(t *testing.T) {
	categoria, existe := BuscarCategoria(SinCategoria)
	if !existe {
		t.Fatalf("%q no está en el catálogo", SinCategoria)
	}
	if categoria.Grupo != "ninguno" {
		t.Errorf("grupo de %q = %q, want ninguno", SinCategoria, categoria.Grupo)
	}
	if EsCategoriaVisible(SinCategoria) {
		t.Errorf("EsCategoriaVisible(%q) = true, want false", SinCategoria)
	}
	if !EsCategoria(SinCategoria) {
		t.Errorf("EsCategoria(%q) = false, want true", SinCategoria)
	}
}

func TestCategoriaInventadaNoExiste(t *testing.T) {
	if _, existe := BuscarCategoria("categoria_inventada"); existe {
		t.Error("BuscarCategoria aceptó una categoría fuera del catálogo")
	}
	if EsCategoria("categoria_inventada") {
		t.Error("EsCategoria aceptó una categoría fuera del catálogo")
	}
}

func TestCatalogoFijoDevuelveUnaCopia(t *testing.T) {
	catalogo := CatalogoFijo()
	catalogo[0].Nombre = "alterado"
	if CatalogoFijo()[0].Nombre == "alterado" {
		t.Error("CatalogoFijo expone el arreglo interno")
	}
}

package inteligencia

import (
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
)

func TestCategoriaQueAlcanzaK(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre    string
		votos     []repositorio.ContarVotosPorCategoriaRow
		k         int
		categoria Categoria
		alcanza   bool
	}{
		{"dos usuarios iguales promueven", []repositorio.ContarVotosPorCategoriaRow{{Categoria: "compras", Votos: 2}}, 2, CategoriaCompras, true},
		{"uno solo no promueve", []repositorio.ContarVotosPorCategoriaRow{{Categoria: "compras", Votos: 1}}, 2, "", false},
		{"empate a uno no promueve", []repositorio.ContarVotosPorCategoriaRow{{Categoria: "compras", Votos: 1}, {Categoria: "salud", Votos: 1}}, 2, "", false},
		{"gana la que tiene más", []repositorio.ContarVotosPorCategoriaRow{{Categoria: "salud", Votos: 3}, {Categoria: "compras", Votos: 1}}, 2, CategoriaSalud, true},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			categoria, alcanza := categoriaQueAlcanzaK(caso.votos, caso.k)
			if categoria != caso.categoria || alcanza != caso.alcanza {
				t.Fatalf("categoría/alcanza = %q/%v, quiero %q/%v", categoria, alcanza, caso.categoria, caso.alcanza)
			}
		})
	}
}

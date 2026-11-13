package inteligencia

import "testing"

// TestEscalonesDelLoteRespetaElOrdenDelContrato enciende varios escalones a la vez y comprueba que
// gane el primero del contrato: MCC sobre catálogo y catálogo sobre léxico.
func TestEscalonesDelLoteRespetaElOrdenDelContrato(t *testing.T) {
	t.Parallel()

	lexico, err := CargarLexicoV2()
	if err != nil {
		t.Fatalf("CargarLexicoV2: %v", err)
	}
	catalogo := CatalogoCurado{Nombres: map[string]Categoria{
		"TIENDA-DEL-BARRIO":  CategoriaSalud,
		"DROGUERIA-NORTE-12": CategoriaCompras,
	}}
	escalones := escalonesDelLote(catalogo, &Modelo{}, lexico.Pares)
	for _, escalon := range []int{EscalonMCC, EscalonCatalogo, EscalonLexico, EscalonClasificador} {
		if !escalones.Contiene(escalon) {
			t.Fatalf("escalones = %v, quiero el escalón %d encendido", escalones, escalon)
		}
	}
	recursos := Recursos{
		CatalogoNombres: catalogo.Nombres,
		Lexico:          lexico.Pares,
	}

	casos := []struct {
		nombre    string
		entrada   Entrada
		escalon   int
		origen    string
		categoria Categoria
	}{
		{
			nombre: "el MCC gana al catálogo",
			entrada: Entrada{
				Canal:       CanalQR,
				Nombre:      "Tienda del Barrio",
				MCC:         "5411",
				TipoDestino: DestinoComercio,
			},
			escalon:   EscalonMCC,
			origen:    OrigenMCC,
			categoria: CategoriaAlimentosMercado,
		},
		{
			nombre: "el catálogo gana al léxico",
			entrada: Entrada{
				Canal:       CanalQR,
				Nombre:      "Droguería Norte 12",
				TipoDestino: DestinoComercio,
			},
			escalon:   EscalonCatalogo,
			origen:    OrigenCatalogo,
			categoria: CategoriaCompras,
		},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			decision := Decidir(caso.entrada, recursos, escalones)
			if decision.Escalon != caso.escalon || decision.Origen != caso.origen ||
				decision.Categoria != caso.categoria {
				t.Fatalf("decisión = %+v, want escalón %d, origen %q y categoría %q",
					decision, caso.escalon, caso.origen, caso.categoria)
			}
		})
	}
}

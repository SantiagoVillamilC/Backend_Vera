package inteligencia

import "testing"

func TestSinCatalogoElEscalonCuatroQuedaApagado(t *testing.T) {
	t.Parallel()

	vacio := CatalogoCurado{}
	if vacio.HayEntradas() {
		t.Fatal("un catálogo sin mapas dice tener entradas")
	}
	if escalonesDelLote(vacio, nil, nil).Contiene(EscalonCatalogo) {
		t.Fatal("sin catálogo el lote encendió el escalón 4")
	}
}

func TestConCatalogoElEscalonCuatroSeEnciende(t *testing.T) {
	t.Parallel()

	catalogo := CatalogoCurado{Nombres: map[string]Categoria{"DROGUERIA-NORTE-SAS": CategoriaSalud}}
	escalones := escalonesDelLote(catalogo, nil, nil)
	if !escalones.Contiene(EscalonCatalogo) {
		t.Fatal("con catálogo el lote no encendió el escalón 4")
	}
	decision := Decidir(Entrada{
		Canal:       CanalQR,
		Nombre:      "Droguería Norte S.A.S.",
		TipoDestino: DestinoComercio,
	}, Recursos{CatalogoNombres: catalogo.Nombres}, escalones)
	if decision.Origen != OrigenCatalogo || decision.Categoria != CategoriaSalud {
		t.Fatalf("decisión = %+v, want origen %q y categoría %q", decision, OrigenCatalogo,
			CategoriaSalud)
	}
}

func TestUnaLlaveEnElCatalogoDecideSinNombre(t *testing.T) {
	t.Parallel()

	catalogo := CatalogoCurado{Llaves: map[string]Categoria{"@panaderia": CategoriaAlimentosMercado}}
	decision := Decidir(Entrada{
		Canal:       CanalQR,
		Llave:       "@panaderia",
		TipoDestino: DestinoComercio,
	}, Recursos{CatalogoLlaves: catalogo.Llaves}, escalonesDelLote(catalogo, nil, nil))
	if decision.Origen != OrigenCatalogo || decision.Categoria != CategoriaAlimentosMercado {
		t.Fatalf("decisión = %+v, want origen %q y categoría %q", decision, OrigenCatalogo,
			CategoriaAlimentosMercado)
	}
}

// El catálogo se cargó con nombres cortados a 25 puntos de código, así que la cascada tiene que
// buscar con ese mismo recorte: un nombre largo que el evaluador encuentra también lo encuentra acá.
func TestElCatalogoSeBuscaConElRecorteDelEvaluador(t *testing.T) {
	t.Parallel()

	catalogo := CatalogoCurado{Nombres: map[string]Categoria{
		"ALMACEN-FACOL-102-ALQUERI": CategoriaCompras,
		"CITY-PARKING-SAS":          CategoriaTransporte,
	}}
	escalones := escalonesDelLote(catalogo, nil, nil)
	casos := []struct {
		nombre string
		querer Categoria
	}{
		{"Almacén Facol 102 Alquería", CategoriaCompras},
		{"city parking s.a.s.", CategoriaTransporte},
	}
	for _, caso := range casos {
		decision := Decidir(Entrada{
			Canal:       CanalQR,
			Nombre:      caso.nombre,
			TipoDestino: DestinoComercio,
		}, Recursos{CatalogoNombres: catalogo.Nombres}, escalones)
		if decision.Origen != OrigenCatalogo || decision.Categoria != caso.querer {
			t.Fatalf("decisión de %q = %+v, want origen %q y categoría %q", caso.nombre, decision,
				OrigenCatalogo, caso.querer)
		}
	}
}

func TestUnNombreFueraDelCatalogoCaeAlEscalonSiguiente(t *testing.T) {
	t.Parallel()

	catalogo := CatalogoCurado{Nombres: map[string]Categoria{"ALMACEN-EXITO-CENTRO": CategoriaAlimentosMercado}}
	decision := Decidir(Entrada{
		Canal:       CanalQR,
		Nombre:      "Ferretería El Tornillo",
		TipoDestino: DestinoComercio,
	}, Recursos{CatalogoNombres: catalogo.Nombres}, escalonesDelLote(catalogo, nil, nil))
	if decision.Origen == OrigenCatalogo {
		t.Fatalf("decisión = %+v, want que un nombre ausente no salga con origen %q", decision,
			OrigenCatalogo)
	}
	if decision.Origen != OrigenSinCategoria || decision.Escalon != EscalonSinCategoria {
		t.Fatalf("decisión = %+v, want el escalón %d con origen %q", decision, EscalonSinCategoria,
			OrigenSinCategoria)
	}
}

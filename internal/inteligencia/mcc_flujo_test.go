package inteligencia

import "testing"

func TestResolverContraparteLlevaElMCCDelAsiento(t *testing.T) {
	t.Parallel()

	apuntes := []Apunte{
		{
			EntryID:  1,
			CuentaID: "pasivo:liquidacion_pendiente:riel",
			Monto:    1000,
			MCC:      "5411",
		},
	}
	contraparte := ResolverContraparte(apuntes, "usuario", nil)
	if contraparte.MCC != "5411" {
		t.Fatalf("MCC = %q, quiero 5411", contraparte.MCC)
	}
	if contraparte.Tipo != DestinoComercio {
		t.Fatalf("tipo = %q, quiero comercio", contraparte.Tipo)
	}
}

func TestResolverContraparteIgnoraElMCCDeUnaPersona(t *testing.T) {
	t.Parallel()

	apuntes := []Apunte{
		{EntryID: 1, CuentaID: "pasivo:usuario:otro:disponible", Monto: 1000, MCC: "5411"},
	}
	contraparte := ResolverContraparte(apuntes, "usuario", nil)
	if contraparte.MCC != "" {
		t.Fatalf("MCC = %q, quiero vacío para una persona", contraparte.MCC)
	}
}

func TestEscalonesDelLoteEnciendeMCC(t *testing.T) {
	t.Parallel()

	if !escalonesDelLote(CatalogoCurado{}, nil, nil).Contiene(EscalonMCC) {
		t.Fatal("el lote no enciende el escalón MCC")
	}
}

func TestDecidirPorMCCGanaAlCatalogoYAlLexico(t *testing.T) {
	t.Parallel()

	entrada := Entrada{
		Canal:       CanalQR,
		Llave:       "llave-comercio",
		Nombre:      "TIENDA DEL BARRIO",
		MCC:         "5411",
		TipoDestino: DestinoComercio,
	}
	recursos := Recursos{
		CatalogoLlaves:  map[string]Categoria{"llave-comercio": CategoriaCompras},
		CatalogoNombres: map[string]Categoria{"TIENDA-DEL-BARRIO": CategoriaCompras},
		Lexico:          []ParLexico{{Palabra: "TIENDA", Categoria: CategoriaCompras}},
	}
	decision := Decidir(entrada, recursos, TodosLosEscalones())
	if decision.Escalon != EscalonMCC || decision.Origen != OrigenMCC || decision.Categoria != CategoriaAlimentosMercado {
		t.Fatalf("decisión = %+v", decision)
	}
}

func TestDecidirConMCCGenericoSigueAlSiguienteEscalon(t *testing.T) {
	t.Parallel()

	entrada := Entrada{
		Canal:       CanalQR,
		Llave:       "llave-comercio",
		Nombre:      "TIENDA DEL BARRIO",
		MCC:         "5999",
		TipoDestino: DestinoComercio,
	}
	recursos := Recursos{
		CatalogoLlaves: map[string]Categoria{"llave-comercio": CategoriaCompras},
	}
	decision := Decidir(entrada, recursos, TodosLosEscalones())
	if decision.Origen != OrigenCatalogo || decision.Escalon != EscalonCatalogo {
		t.Fatalf("decisión = %+v, quiero que siga al catálogo", decision)
	}
}

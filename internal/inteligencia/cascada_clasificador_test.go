package inteligencia

import "testing"

func entradaDeComercio(nombre string) Entrada {
	return Entrada{
		Canal:       CanalQR,
		Llave:       "llave-comercio",
		Nombre:      nombre,
		TipoDestino: DestinoComercio,
	}
}

func TestDecidirConClasificadorDevuelveEscalonSeis(t *testing.T) {
	modelo := modeloClasificador(t, idfDeTexto("ALFA"),
		map[string]map[string]string{"compras": centroideConCoseno(t, "ALFA", 0.9)}, nil)
	decision := Decidir(entradaDeComercio("ALFA"), Recursos{Clasificador: modelo}, PrimerCorteConClasificador())
	if decision.Escalon != EscalonClasificador || decision.Categoria != CategoriaCompras ||
		decision.Origen != OrigenTFIDF || decision.PendienteLLM {
		t.Fatalf("decisión = %+v", decision)
	}
}

func TestDecidirPrefiereLaCacheAlClasificador(t *testing.T) {
	modelo := modeloClasificador(t, idfDeTexto("ALFA"), map[string]map[string]string{
		"compras": centroideConCoseno(t, "ALFA", 0.9),
		"salud":   {"w:OTRO": "1"},
	}, nil)
	recursos := Recursos{
		CacheLLM:     map[string]Categoria{"ALFA|llave-comercio": CategoriaSalud},
		Clasificador: modelo,
	}
	decision := Decidir(entradaDeComercio("ALFA"), recursos, PrimerCorteConClasificador())
	if decision.Categoria != CategoriaSalud || decision.Origen != OrigenLLM || decision.Escalon != EscalonClasificador {
		t.Fatalf("decisión = %+v", decision)
	}
}

func TestDecidirSinClasificadorSiguePendiente(t *testing.T) {
	decision := Decidir(entradaDeComercio("ALFA"), Recursos{}, PrimerCorteConClasificador())
	if decision.Escalon != EscalonClasificador || !decision.PendienteLLM || decision.Categoria != "" {
		t.Fatalf("decisión = %+v", decision)
	}
}

func TestDecidirConAbstencionDelClasificadorQuedaPendiente(t *testing.T) {
	modelo := modeloClasificador(t, map[string]string{"w:DELTA": "1"},
		map[string]map[string]string{"compras": {"w:OTRO": "1"}}, nil)
	decision := Decidir(entradaDeComercio("DELTA"), Recursos{Clasificador: modelo}, PrimerCorteConClasificador())
	if decision.Categoria != "" || decision.Origen != "" || !decision.PendienteLLM {
		t.Fatalf("decisión = %+v", decision)
	}
}

func TestDecidirNoUsaElClasificadorParaPersonaNiLlaveNiNombreVacio(t *testing.T) {
	modelo := modeloClasificador(t, idfDeTexto("ALFA"),
		map[string]map[string]string{"compras": centroideConCoseno(t, "ALFA", 0.9)}, nil)
	casos := map[string]Entrada{
		"persona":     {Canal: CanalLlave, Llave: "llave-persona", Nombre: "ALFA", TipoDestino: DestinoPersona},
		"canal llave": {Canal: CanalLlave, Llave: "llave-comercio", Nombre: "ALFA", TipoDestino: DestinoComercio},
		"sin nombre":  {Canal: CanalQR, Llave: "llave-comercio", TipoDestino: DestinoComercio},
	}
	for nombre, entrada := range casos {
		t.Run(nombre, func(t *testing.T) {
			decision := Decidir(entrada, Recursos{Clasificador: modelo}, PrimerCorteConClasificador())
			if decision.Escalon == EscalonClasificador || decision.Origen == OrigenTFIDF {
				t.Fatalf("decisión = %+v", decision)
			}
		})
	}
}

func TestPrimerCorteApagaElClasificador(t *testing.T) {
	modelo := modeloClasificador(t, idfDeTexto("ALFA"),
		map[string]map[string]string{"compras": centroideConCoseno(t, "ALFA", 0.9)}, nil)
	decision := Decidir(entradaDeComercio("ALFA"), Recursos{Clasificador: modelo}, PrimerCorte())
	if decision.Escalon != EscalonSinCategoria || decision.Categoria != "" {
		t.Fatalf("decisión = %+v", decision)
	}
}

func TestDecidirPasaElNombreNormalizadoAlClasificador(t *testing.T) {
	normalizado := NormalizarNombre("Farmacia La  ñ")
	modelo := modeloClasificador(t, idfDeTexto(normalizado),
		map[string]map[string]string{"salud": centroideConCoseno(t, normalizado, 0.9)}, nil)
	decision := Decidir(entradaDeComercio("Farmacia La  ñ"), Recursos{Clasificador: modelo}, PrimerCorteConClasificador())
	if decision.Categoria != CategoriaSalud || decision.Origen != OrigenTFIDF {
		t.Fatalf("decisión = %+v con el nombre normalizado %q", decision, normalizado)
	}
}

func TestDecidirPasaLaCategoriaDelMCCAlClasificador(t *testing.T) {
	normalizado := NormalizarNombre("ECO ALFA")
	centroide := centroideConCoseno(t, normalizado, 0.9)
	idf := idfDeTexto(normalizado)
	entrada := entradaDeComercio("ECO ALFA")
	entrada.MCC = "5411"
	modelo := modeloClasificador(t, idf, map[string]map[string]string{"compras": centroide}, nil)
	sinMCC := Decidir(entradaDeComercio("ECO ALFA"), Recursos{Clasificador: modelo}, PrimerCorteConClasificador())
	conMCC := Decidir(entrada, Recursos{Clasificador: modelo}, PrimerCorteConClasificador())
	if sinMCC.Origen != OrigenTFIDF || conMCC.Origen != OrigenTFIDF {
		t.Fatalf("sin MCC = %+v, con MCC = %+v", sinMCC, conMCC)
	}
}

func TestOrigenEsCoherenteConElEscalon(t *testing.T) {
	documento := cargarVectores(t)
	porEscalon := map[int]string{
		EscalonMemoria:      OrigenMemoria,
		EscalonInterna:      OrigenInterna,
		EscalonMCC:          OrigenMCC,
		EscalonCatalogo:     OrigenCatalogo,
		EscalonLexico:       OrigenLexico,
		EscalonSinCategoria: OrigenSinCategoria,
	}
	for _, caso := range documento.Casos {
		decision := Decidir(entradaDelCaso(caso), recursosDelCaso(caso), escalonesDelCaso(caso))
		if decision.Escalon == EscalonClasificador {
			if decision.Categoria == "" {
				if decision.Origen != "" {
					t.Fatalf("caso %q: abstención con origen %q", caso.ID, decision.Origen)
				}
				continue
			}
			if decision.Origen != OrigenLLM {
				t.Fatalf("caso %q: origen %q en el escalón 6, want %q", caso.ID, decision.Origen, OrigenLLM)
			}
			continue
		}
		if esperado := porEscalon[decision.Escalon]; decision.Origen != esperado {
			t.Fatalf("caso %q: escalón %d con origen %q, want %q",
				caso.ID, decision.Escalon, decision.Origen, esperado)
		}
	}
}

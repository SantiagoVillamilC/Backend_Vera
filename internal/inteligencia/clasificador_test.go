package inteligencia

import (
	"math"
	"testing"
)

func idfDeTexto(nombre string) map[string]string {
	valores := map[string]string{}
	for _, par := range caracteristicasDeTexto(nombre) {
		valores[par.clave] = "1"
	}
	return valores
}

func pesosDeNombre(nombre string) map[string]string {
	valores := map[string]string{}
	for _, par := range caracteristicasDeTexto(nombre) {
		valores[par.clave] = "1"
	}
	return valores
}

// centroideConCoseno arma un centroide con el subconjunto mínimo de características del nombre que
// alcanza el coseno pedido, para que un caso fuerte no dependa de cuántas características tiene.
func centroideConCoseno(t *testing.T, nombre string, objetivo float64) map[string]string {
	t.Helper()
	caracteristicas := caracteristicasDeTexto(nombre)
	total := 0.0
	for _, par := range caracteristicas {
		total += par.valor * par.valor
	}
	centroide := map[string]string{}
	parcial := 0.0
	for _, par := range caracteristicas {
		centroide[par.clave] = "1"
		parcial += par.valor * par.valor
		if math.Sqrt(parcial)/math.Sqrt(total) >= objetivo {
			break
		}
	}
	if math.Sqrt(parcial)/math.Sqrt(total) < objetivo {
		t.Fatalf("no se pudo alcanzar el coseno %v con %q", objetivo, nombre)
	}
	return centroide
}

func modeloClasificador(t *testing.T, idf map[string]string, centroides map[string]map[string]string,
	lexico map[string]string) *Modelo {
	t.Helper()
	modelo, err := CargarModelo(artefactoDePrueba(t, VersionArtefacto, idf, umbralesDePrueba(), centroides, lexico))
	if err != nil {
		t.Fatalf("cargar el modelo de prueba: %v", err)
	}
	return modelo
}

func TestPredecirCosenoFuerteDecide(t *testing.T) {
	modelo := modeloClasificador(t, idfDeTexto("ALFA"),
		map[string]map[string]string{"compras": centroideConCoseno(t, "ALFA", 0.9)}, nil)
	categoria, decidio := Predecir(modelo, "ALFA", "")
	if !decidióOEsperada(decidio, "compras", categoria) {
		t.Fatalf("decisión = %q (%v), want compras", categoria, decidio)
	}
}

func decidióOEsperada(decidio bool, esperada Categoria, obtenida Categoria) bool {
	return decidio && obtenida == esperada
}

func TestPredecirCosenoFuerteNoSeSobreescribeConElLexico(t *testing.T) {
	centroides := map[string]map[string]string{
		"compras": centroideConCoseno(t, "FARMACIA ALFA", 0.9),
		"salud":   {"w:OTRO": "1"},
	}
	modelo := modeloClasificador(t, idfDeTexto("FARMACIA ALFA"), centroides, map[string]string{"FARMACIA": "salud"})
	categoria, decidio := Predecir(modelo, "FARMACIA ALFA", "")
	if !decidióOEsperada(decidio, "compras", categoria) {
		t.Fatalf("decisión = %q (%v), want compras por coseno fuerte", categoria, decidio)
	}
}

func TestPredecirCosenoDebilUsaElLexico(t *testing.T) {
	centroides := map[string]map[string]string{
		"compras": {"w:ALFA": "1"},
		"salud":   {"w:OTRO": "1"},
	}
	modelo := modeloClasificador(t, idfDeTexto("DROGUERIA ALFA"), centroides, map[string]string{"DROGUERIA": "salud"})
	categoria, decidio := Predecir(modelo, "DROGUERIA ALFA", "")
	if !decidióOEsperada(decidio, "salud", categoria) {
		t.Fatalf("decisión = %q (%v), want salud por léxico", categoria, decidio)
	}
}

func TestPredecirCosenoDebilSinLexicoDecidePorCoseno(t *testing.T) {
	centroides := map[string]map[string]string{"compras": {"w:ALFA": "1"}}
	modelo := modeloClasificador(t, idfDeTexto("BRAVO ALFA"), centroides, nil)
	categoria, decidio := Predecir(modelo, "BRAVO ALFA", "")
	if !decidióOEsperada(decidio, "compras", categoria) {
		t.Fatalf("decisión = %q (%v), want compras", categoria, decidio)
	}
}

func TestPredecirSeAbstiene(t *testing.T) {
	casos := map[string]struct {
		modelo *Modelo
		nombre string
	}{
		"coseno bajo":    {modeloClasificador(t, map[string]string{"w:DELTA": "1"}, map[string]map[string]string{"compras": {"w:OTRO": "1"}}, nil), "DELTA"},
		"nombre vacio":   {modeloClasificador(t, map[string]string{"w:ALFA": "1"}, map[string]map[string]string{"compras": {"w:ALFA": "1"}}, nil), ""},
		"solo genericos": {modeloClasificador(t, map[string]string{"w:TIENDA": "1"}, map[string]map[string]string{"compras": {"w:TIENDA": "1"}}, nil), "TIENDA SAS"},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			categoria, decidio := Predecir(caso.modelo, caso.nombre, "")
			if decidio || categoria != "" {
				t.Fatalf("decisión = %q (%v), want abstención", categoria, decidio)
			}
		})
	}
}

func TestPredecirDesempataPorOrdenAlfabetico(t *testing.T) {
	pesos := centroideConCoseno(t, "GOLF", 0.9)
	modelo := modeloClasificador(t, idfDeTexto("GOLF"), map[string]map[string]string{
		"compras":                 pesos,
		"alimentos_mercado":       pesos,
		"restaurantes_domicilios": pesos,
	}, nil)
	for range 100 {
		categoria, decidio := Predecir(modelo, "GOLF", "")
		if !decidio || categoria != CategoriaAlimentosMercado {
			t.Fatalf("decisión = %q (%v), want alimentos_mercado", categoria, decidio)
		}
	}
}

func TestPredecirNoInventaCategoriasFueraDelCatalogo(t *testing.T) {
	modelo := modeloClasificador(t, map[string]string{"w:ALFA": "1"},
		map[string]map[string]string{"compras": {"w:ALFA": "1"}}, nil)
	for _, categoria := range modelo.Categorias() {
		if !EsCategoriaVisible(categoria) {
			t.Fatalf("el modelo trae la categoría no visible %q", categoria)
		}
	}
}

func TestCuantizacionYTolerancia(t *testing.T) {
	casos := []struct {
		puntaje float64
		umbral  float64
		espera  bool
	}{
		{0.0999999, 0.1, true},
		{0.0999999999, 0.1, true},
		{0.1000000001, 0.1, true},
		{0.1000001, 0.1, true},
		{0.0999, 0.1, false},
		{0, 0.1, false},
		{0.4999999, 0.5, true},
		{0.4999, 0.5, false},
		{1, 1, true},
	}
	for _, caso := range casos {
		if obtenido := alcanza(caso.puntaje, caso.umbral); obtenido != caso.espera {
			t.Fatalf("alcanza(%v, %v) = %v, want %v", caso.puntaje, caso.umbral, obtenido, caso.espera)
		}
	}
	if math.Abs(cuantizar(0.123456789)-0.123457) > 1e-12 {
		t.Fatalf("cuantizar = %v", cuantizar(0.123456789))
	}
}

func TestPredecirUsaElTokenDeMCC(t *testing.T) {
	delNombre := pesosDeNombre("ECO ALFA")
	delToken := pesosDeNombre("mcc:salud")
	idf := map[string]string{}
	for clave := range delNombre {
		idf[clave] = "1"
	}
	for clave := range delToken {
		idf[clave] = "1"
	}
	modelo := modeloClasificador(t, idf, map[string]map[string]string{
		"compras": delNombre,
		"salud":   delToken,
	}, nil)
	sinMCC, _ := Predecir(modelo, "ECO ALFA", "")
	conMCC, _ := Predecir(modelo, "ECO ALFA", "salud")
	if sinMCC != CategoriaCompras {
		t.Fatalf("sin MCC = %q, want compras", sinMCC)
	}
	if conMCC != CategoriaSalud {
		t.Fatalf("con MCC = %q, want salud", conMCC)
	}
}

func TestMCCCategoriaDeSoloAceptaLaTabla(t *testing.T) {
	casos := map[string]string{
		"5411": "alimentos_mercado",
		"5812": "restaurantes_domicilios",
		"5310": "",
		"":     "",
		"999":  "",
		"abcd": "",
	}
	for mcc, esperada := range casos {
		if obtenida := mccCategoriaDe(mcc); obtenida != esperada {
			t.Fatalf("mccCategoriaDe(%q) = %q, want %q", mcc, obtenida, esperada)
		}
	}
}

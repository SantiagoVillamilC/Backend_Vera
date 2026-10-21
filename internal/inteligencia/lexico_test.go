package inteligencia

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

// TestCargarLexicoV2VerificaLaHuella fija el artefacto: si el CSV cambia sin cambiar la constante, la
// carga falla cerrada.
func TestCargarLexicoV2VerificaLaHuella(t *testing.T) {
	t.Parallel()
	lexico, err := CargarLexicoV2()
	if err != nil {
		t.Fatalf("CargarLexicoV2: %v", err)
	}
	if lexico.Version != VersionLexicoV2 || lexico.Huella != HuellaLexicoV2 {
		t.Fatalf("versión %q huella %q", lexico.Version, lexico.Huella)
	}
	if len(lexico.Pares) != 588 {
		t.Fatalf("pares = %d, quiero 588", len(lexico.Pares))
	}
	if lexico.Pares[0].Palabra == "" || lexico.Pares[0].Categoria == "" {
		t.Fatalf("primer par vacío: %+v", lexico.Pares[0])
	}
}

// TestLexicoV2NoTraeLasCatorceGenericas es la razón de existir de la versión nueva.
func TestLexicoV2NoTraeLasCatorceGenericas(t *testing.T) {
	t.Parallel()
	lexico, err := CargarLexicoV2()
	if err != nil {
		t.Fatalf("CargarLexicoV2: %v", err)
	}
	retiradas := []string{
		"AIRE", "ESTUDIO", "ESTACION", "ARTE", "ZONA", "EMPRESARIAL", "COMPRA",
		"MIXTO", "JARDIN", "DISEÑO", "AUTOSERVICIO", "CASA", "GIMNASIO", "MISCELANEA",
	}
	presentes := map[string]bool{}
	for _, par := range lexico.Pares {
		presentes[par.Palabra] = true
	}
	for _, palabra := range retiradas {
		if presentes[palabra] {
			t.Fatalf("%q debía estar fuera del léxico v2", palabra)
		}
	}
	if !presentes["DROGUERIA"] {
		t.Fatal("el léxico v2 debe conservar las palabras con oficio")
	}
}

// TestCargarLexicoV2FallaSiLaHuellaNoCoincide simula un archivo movido a mano.
func TestCargarLexicoV2FallaSiLaHuellaNoCoincide(t *testing.T) {
	t.Parallel()
	suma := hex.EncodeToString(func() []byte { s := sha256.Sum256(lexicoV2CSV); return s[:] }())
	if suma != HuellaLexicoV2 {
		t.Fatalf("la huella embebida cambió: %s", suma)
	}
	// El camino de fallo se prueba sobre la función de verificación, sin tocar el archivo.
	if err := verificarHuella("otra-huella"); !errors.Is(err, ErrLexicoInvalido) {
		t.Fatalf("error = %v, quiero ErrLexicoInvalido", err)
	}
}

// TestInterpretarModoLexico cierra la bandera que enciende el escalón 5.
func TestInterpretarModoLexico(t *testing.T) {
	t.Parallel()
	casos := map[string]struct {
		valor     string
		esperado  ModoLexico
		quieroErr bool
	}{
		"vacío":      {valor: "", esperado: ModoLexicoApagado},
		"apagado":    {valor: "apagado", esperado: ModoLexicoApagado},
		"mayúsculas": {valor: "V2", esperado: ModoLexicoV2},
		"espacios":   {valor: " v2 ", esperado: ModoLexicoV2},
		"inventado":  {valor: "v3", quieroErr: true},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			modo, err := InterpretarModoLexico(caso.valor)
			if (err != nil) != caso.quieroErr {
				t.Fatalf("error = %v, quiero error %v", err, caso.quieroErr)
			}
			if !caso.quieroErr && modo != caso.esperado {
				t.Fatalf("modo = %q, quiero %q", modo, caso.esperado)
			}
		})
	}
}

// TestEscalonesDelLoteComponeLosDosEscalones ordena la precedencia del contrato.
func TestEscalonesDelLoteComponeLosDosEscalones(t *testing.T) {
	t.Parallel()
	lexico, err := CargarLexicoV2()
	if err != nil {
		t.Fatalf("CargarLexicoV2: %v", err)
	}
	sinNada := escalonesDelLote(nil, nil)
	if len(sinNada) != 2 || sinNada.Contiene(EscalonLexico) || sinNada.Contiene(EscalonClasificador) {
		t.Fatalf("sin nada = %v, quiero solo memoria e interna", sinNada)
	}
	soloLexico := escalonesDelLote(nil, lexico.Pares)
	if !soloLexico.Contiene(EscalonLexico) || soloLexico.Contiene(EscalonClasificador) {
		t.Fatalf("solo léxico = %v", soloLexico)
	}
	ambos := escalonesDelLote(&Modelo{}, lexico.Pares)
	if !ambos.Contiene(EscalonLexico) || !ambos.Contiene(EscalonClasificador) {
		t.Fatalf("ambos = %v", ambos)
	}
}

// TestLexicoDecideAntesQueElClasificador fija el orden del contrato: el escalón 5 gana al 6.
func TestLexicoDecideAntesQueElClasificador(t *testing.T) {
	t.Parallel()
	lexico, err := CargarLexicoV2()
	if err != nil {
		t.Fatalf("CargarLexicoV2: %v", err)
	}
	modelo := &Modelo{Version: VersionArtefacto, Huella: HuellaArtefactoTFIDF,
		UmbralCoseno: 0.2, UmbralCosenoFuerte: 0.8, UmbralJW: 0.9,
		Centroides: map[Categoria]map[string]float64{},
		IDF:        map[string]float64{},
		Lexico:     map[string]Categoria{}}

	decision := Decidir(entradaDeComercio("DROGUERIA NORTE 12"), Recursos{
		Lexico: lexico.Pares, Clasificador: modelo,
	}, escalonesDelLote(modelo, lexico.Pares))

	if decision.Origen != OrigenLexico {
		t.Fatalf("origen = %q, quiero %q", decision.Origen, OrigenLexico)
	}
	if decision.Categoria != CategoriaSalud {
		t.Fatalf("categoría = %q, quiero %q", decision.Categoria, CategoriaSalud)
	}
}

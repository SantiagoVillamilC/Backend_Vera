package main

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
)

// ZWilson es el valor crítico de la normal estándar para un intervalo del 95 por ciento.
const ZWilson = 1.96

// Fila es un nombre del conjunto de oro con su etiqueta y, si se conoce, su parte y su estrato.
type Fila struct {
	ID            string
	Nombre        string
	Oro           string
	Parte         string
	Estrato       string
	ParecePersona bool
	MCC           string
}

// DecisionDeOro es lo que el clasificador decidió sobre una fila. La categoría vacía significa que no
// mostró nada, y abstuvo distingue la abstención del clasificador de un nombre que nunca le llegó.
type DecisionDeOro struct {
	Categoria string
	Origen    string
	Abstuvo   bool
	Lexico    []Coincidencia
}

// Coincidencia es una palabra del léxico que el nombre activa, con la categoría que el léxico le da.
// Se llena solo en los modos que encienden el escalón 5 y es nil cuando no se midió.
type Coincidencia struct {
	Palabra   string `json:"palabra"`
	Categoria string `json:"categoria"`
}

// Decisor es el punto de inyección: en producción decide la cascada real y en las pruebas un doble.
type Decisor func(Fila) DecisionDeOro

// Conteo es el resumen de una corrida para un subconjunto de filas.
type Conteo struct {
	Filas           int     `json:"filas"`
	ConEtiqueta     int     `json:"con_etiqueta"`
	SinEtiqueta     int     `json:"sin_etiqueta"`
	Mostradas       int     `json:"mostradas"`
	Correctas       int     `json:"correctas"`
	Abstenciones    int     `json:"abstenciones"`
	SinDecision     int     `json:"sin_decision"`
	PersonaMostrada int     `json:"persona_mostrada"`
	Precision       float64 `json:"precision"`
	WilsonBajo      float64 `json:"wilson_bajo"`
	WilsonAlto      float64 `json:"wilson_alto"`
	Cobertura       float64 `json:"cobertura"`
}

// FilaDeCategoria es el detalle por categoría de oro.
type FilaDeCategoria struct {
	Categoria   string  `json:"categoria"`
	ConEtiqueta int     `json:"con_etiqueta"`
	Mostradas   int     `json:"mostradas"`
	Correctas   int     `json:"correctas"`
	Precision   float64 `json:"precision"`
}

// ErrorDeCategoria es un par (oro, predicha) que se repite y explica los fallos.
type ErrorDeCategoria struct {
	Oro      string `json:"oro"`
	Predicha string `json:"predicha"`
	Veces    int    `json:"veces"`
}

// DesgloseLexico separa por qué el léxico no decide: no encontró ninguna palabra o encontró palabras
// que apuntan a categorías distintas. Solo se llena en los modos que encienden el escalón 5.
type DesgloseLexico struct {
	FilasConEtiqueta      int                   `json:"filas_con_etiqueta"`
	UnaCategoria          int                   `json:"una_categoria"`
	Conflictos            int                   `json:"conflictos"`
	SinPalabra            int                   `json:"sin_palabra"`
	UnaCategoriaCorrecta  int                   `json:"una_categoria_correcta"`
	ConflictosConAcierto  int                   `json:"conflictos_con_acierto"`
	ConflictosPorPalabras []ConflictoDePalabras `json:"conflictos_por_palabras"`
}

// ConflictoDePalabras es un conjunto de palabras que aparecen juntas y apuntan a categorías distintas.
type ConflictoDePalabras struct {
	Palabras  string `json:"palabras"`
	Veces     int    `json:"veces"`
	Correctas int    `json:"correctas"`
}

// Informe es el resultado completo de una corrida de medición.
type Informe struct {
	Modo           string             `json:"modo"`
	Umbral         float64            `json:"umbral"`
	Cumple         bool               `json:"cumple"`
	CSV            string             `json:"csv"`
	CSVSha256      string             `json:"csv_sha256"`
	Parte          string             `json:"parte,omitempty"`
	Artefacto      ArtefactoMedido    `json:"artefacto"`
	CatalogoCSV    string             `json:"catalogo_csv,omitempty"`
	CatalogoSha256 string             `json:"catalogo_sha256,omitempty"`
	LexicoCSV      string             `json:"lexico_csv,omitempty"`
	LexicoSha256   string             `json:"lexico_sha256,omitempty"`
	Lexico         *DesgloseLexico    `json:"lexico,omitempty"`
	Total          Conteo             `json:"total"`
	PorParte       []FilaDeParte      `json:"por_parte"`
	PorEstrato     []FilaDeParte      `json:"por_estrato"`
	PorCategoria   []FilaDeCategoria  `json:"por_categoria"`
	Errores        []ErrorDeCategoria `json:"errores"`
}

// FilaDeParte es el resumen por parte o por estrato, que se reportan sin meta.
type FilaDeParte struct {
	Nombre string `json:"nombre"`
	Conteo
	Lexico *DesgloseLexico `json:"lexico,omitempty"`
}

// ArtefactoMedido identifica el modelo con el que se midió, para que el número sea reproducible.
type ArtefactoMedido struct {
	Version string `json:"version"`
	Huella  string `json:"huella"`
	Sha256  string `json:"sha256"`
}

// Evaluar mide un modo sobre las filas y devuelve el informe completo.
func Evaluar(modo string, filas []Fila, decidir Decisor, umbral float64) (Informe, error) {
	if len(filas) == 0 {
		return Informe{}, errors.New("evaluar: el conjunto está vacío")
	}
	if decidir == nil {
		return Informe{}, errors.New("evaluar: falta el decisor")
	}
	decisiones := make([]DecisionDeOro, len(filas))
	for i, fila := range filas {
		if err := validarOro(fila.Oro); err != nil {
			return Informe{}, fmt.Errorf("evaluar: fila %q: %w", fila.ID, err)
		}
		decisiones[i] = decidir(fila)
	}
	informe := Informe{
		Modo:         modo,
		Umbral:       umbral,
		Total:        resumir(filas, decisiones, ""),
		Cumple:       umbral <= 0 || resumir(filas, decisiones, "").Precision >= umbral,
		PorParte:     agrupar(filas, decisiones, func(f Fila) string { return f.Parte }),
		PorEstrato:   agrupar(filas, decisiones, func(f Fila) string { return f.Estrato }),
		PorCategoria: porCategoria(filas, decisiones),
		Errores:      errores(filas, decisiones),
		Lexico:       desgloseDelLexico(filas, decisiones),
	}
	if umbral > 0 {
		informe.Cumple = informe.Total.Precision >= umbral
	}
	return informe, nil
}

// categoriasDeGasto son las diez etiquetas del catálogo que un nombre de comercio puede recibir.
var categoriasDeGasto = map[string]struct{}{
	string(inteligencia.CategoriaAlimentosMercado):  {},
	string(inteligencia.CategoriaRestaurantes):      {},
	string(inteligencia.CategoriaTransporte):        {},
	string(inteligencia.CategoriaViviendaServicios): {},
	string(inteligencia.CategoriaSalud):             {},
	string(inteligencia.CategoriaEducacion):         {},
	string(inteligencia.CategoriaEntretenimiento):   {},
	string(inteligencia.CategoriaCompras):           {},
	string(inteligencia.CategoriaComisiones):        {},
	string(inteligencia.CategoriaOtrosGastos):       {},
}

// etiquetaSinDecidir es el valor con el que una persona declara que el nombre no le permite decidir.
const etiquetaSinDecidir = "no_se"

// validarOro falla cerrado ante una etiqueta fuera del catálogo firmado.
func validarOro(oro string) error {
	if oro == etiquetaSinDecidir {
		return nil
	}
	if _, existe := categoriasDeGasto[oro]; existe {
		return nil
	}
	return fmt.Errorf("categoría %q fuera de las diez de gasto o de %q", oro, etiquetaSinDecidir)
}

// resumir cuenta aciertos, abstenciones y cobertura sobre un subconjunto de filas.
func resumir(filas []Fila, decisiones []DecisionDeOro, filtro string) Conteo {
	conteo := Conteo{}
	for i, fila := range filas {
		if filtro != "" && fila.Parte != filtro {
			continue
		}
		conteo.Filas++
		if fila.Oro == etiquetaSinDecidir {
			conteo.SinEtiqueta++
			continue
		}
		conteo.ConEtiqueta++
		decision := decisiones[i]
		if decision.Categoria == "" {
			if decision.Abstuvo {
				conteo.Abstenciones++
			} else {
				conteo.SinDecision++
			}
			continue
		}
		conteo.Mostradas++
		if decision.Categoria == fila.Oro {
			conteo.Correctas++
		}
		if fila.ParecePersona {
			conteo.PersonaMostrada++
		}
	}
	if conteo.Mostradas > 0 {
		conteo.Precision = float64(conteo.Correctas) / float64(conteo.Mostradas)
		conteo.WilsonBajo, conteo.WilsonAlto = Wilson(conteo.Correctas, conteo.Mostradas)
	}
	if conteo.ConEtiqueta > 0 {
		conteo.Cobertura = float64(conteo.Mostradas) / float64(conteo.ConEtiqueta)
	}
	return conteo
}

// Wilson devuelve el intervalo de confianza del 95 por ciento de una proporción.
func Wilson(correctas, total int) (float64, float64) {
	if total <= 0 {
		return 0, 0
	}
	n := float64(total)
	p := float64(correctas) / n
	z2 := ZWilson * ZWilson
	centro := (p + z2/(2*n)) / (1 + z2/n)
	margen := ZWilson * math.Sqrt(p*(1-p)/n+z2/(4*n*n)) / (1 + z2/n)
	bajo := math.Max(0, centro-margen)
	alto := math.Min(1, centro+margen)
	return bajo, alto
}

// agrupar resume por parte o por estrato, en orden alfabético y sin categorías vacías.
func agrupar(filas []Fila, decisiones []DecisionDeOro, clave func(Fila) string) []FilaDeParte {
	indices := map[string][]int{}
	for i, fila := range filas {
		nombre := clave(fila)
		if nombre == "" {
			continue
		}
		indices[nombre] = append(indices[nombre], i)
	}
	nombres := make([]string, 0, len(indices))
	for nombre := range indices {
		nombres = append(nombres, nombre)
	}
	sort.Strings(nombres)
	grupos := make([]FilaDeParte, 0, len(nombres))
	for _, nombre := range nombres {
		subFilas := make([]Fila, 0, len(indices[nombre]))
		subDecisiones := make([]DecisionDeOro, 0, len(indices[nombre]))
		for _, i := range indices[nombre] {
			subFilas = append(subFilas, filas[i])
			subDecisiones = append(subDecisiones, decisiones[i])
		}
		grupos = append(grupos, FilaDeParte{Nombre: nombre, Conteo: resumir(subFilas, subDecisiones, ""),
			Lexico: desgloseDelLexico(subFilas, subDecisiones)})
	}
	return grupos
}

// porCategoria detalla aciertos por etiqueta de oro, ordenado por votos descendentes.
func porCategoria(filas []Fila, decisiones []DecisionDeOro) []FilaDeCategoria {
	conteos := map[string]*FilaDeCategoria{}
	for i, fila := range filas {
		if fila.Oro == etiquetaSinDecidir {
			continue
		}
		actual, existe := conteos[fila.Oro]
		if !existe {
			actual = &FilaDeCategoria{Categoria: fila.Oro}
			conteos[fila.Oro] = actual
		}
		actual.ConEtiqueta++
		if decisiones[i].Categoria == "" {
			continue
		}
		actual.Mostradas++
		if decisiones[i].Categoria == fila.Oro {
			actual.Correctas++
		}
	}
	filasDeCategoria := make([]FilaDeCategoria, 0, len(conteos))
	for _, actual := range conteos {
		if actual.Mostradas > 0 {
			actual.Precision = float64(actual.Correctas) / float64(actual.Mostradas)
		}
		filasDeCategoria = append(filasDeCategoria, *actual)
	}
	sort.Slice(filasDeCategoria, func(i, j int) bool {
		if filasDeCategoria[i].ConEtiqueta != filasDeCategoria[j].ConEtiqueta {
			return filasDeCategoria[i].ConEtiqueta > filasDeCategoria[j].ConEtiqueta
		}
		return filasDeCategoria[i].Categoria < filasDeCategoria[j].Categoria
	})
	return filasDeCategoria
}

// errores lista los pares de confusión más frecuentes, que es lo que explica los fallos.
func errores(filas []Fila, decisiones []DecisionDeOro) []ErrorDeCategoria {
	conteos := map[[2]string]int{}
	for i, fila := range filas {
		if fila.Oro == etiquetaSinDecidir {
			continue
		}
		predicha := decisiones[i].Categoria
		if predicha == "" || predicha == fila.Oro {
			continue
		}
		conteos[[2]string{fila.Oro, predicha}]++
	}
	lista := make([]ErrorDeCategoria, 0, len(conteos))
	for par, veces := range conteos {
		lista = append(lista, ErrorDeCategoria{Oro: par[0], Predicha: par[1], Veces: veces})
	}
	sort.Slice(lista, func(i, j int) bool {
		if lista[i].Veces != lista[j].Veces {
			return lista[i].Veces > lista[j].Veces
		}
		if lista[i].Oro != lista[j].Oro {
			return lista[i].Oro < lista[j].Oro
		}
		return lista[i].Predicha < lista[j].Predicha
	})
	if len(lista) > 10 {
		lista = lista[:10]
	}
	return lista
}

// desgloseDelLexico cuenta las tres salidas del escalón 5 sobre las filas con etiqueta y los conjuntos
// de palabras que lo dejan en conflicto. Devuelve nil cuando el modo no encendió el léxico.
func desgloseDelLexico(filas []Fila, decisiones []DecisionDeOro) *DesgloseLexico {
	activo := false
	for _, decision := range decisiones {
		if decision.Lexico != nil {
			activo = true
			break
		}
	}
	if !activo {
		return nil
	}
	desglose := &DesgloseLexico{}
	pares := make(map[string]*ConflictoDePalabras)
	for i, fila := range filas {
		if fila.Oro == etiquetaSinDecidir {
			continue
		}
		desglose.FilasConEtiqueta++
		categorias := make(map[string]struct{}, len(decisiones[i].Lexico))
		etiquetas := make([]string, 0, len(decisiones[i].Lexico))
		for _, coincidencia := range decisiones[i].Lexico {
			categorias[coincidencia.Categoria] = struct{}{}
			etiquetas = append(etiquetas, coincidencia.Palabra+"="+coincidencia.Categoria)
		}
		_, acierta := categorias[fila.Oro]
		switch len(categorias) {
		case 0:
			desglose.SinPalabra++
		case 1:
			desglose.UnaCategoria++
			if acierta {
				desglose.UnaCategoriaCorrecta++
			}
		default:
			desglose.Conflictos++
			if acierta {
				desglose.ConflictosConAcierto++
			}
			clave := strings.Join(etiquetas, " | ")
			if pares[clave] == nil {
				pares[clave] = &ConflictoDePalabras{Palabras: clave}
			}
			pares[clave].Veces++
			if acierta {
				pares[clave].Correctas++
			}
		}
	}
	for _, par := range pares {
		desglose.ConflictosPorPalabras = append(desglose.ConflictosPorPalabras, *par)
	}
	sort.Slice(desglose.ConflictosPorPalabras, func(i, j int) bool {
		a, b := desglose.ConflictosPorPalabras[i], desglose.ConflictosPorPalabras[j]
		if a.Veces != b.Veces {
			return a.Veces > b.Veces
		}
		return a.Palabras < b.Palabras
	})
	if len(desglose.ConflictosPorPalabras) > 10 {
		desglose.ConflictosPorPalabras = desglose.ConflictosPorPalabras[:10]
	}
	return desglose
}

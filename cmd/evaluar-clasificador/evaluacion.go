package main

import (
	"errors"
	"fmt"
	"math"
	"sort"

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

// Informe es el resultado completo de una corrida de medición.
type Informe struct {
	Modo         string             `json:"modo"`
	Umbral       float64            `json:"umbral"`
	Cumple       bool               `json:"cumple"`
	CSV          string             `json:"csv"`
	CSVSha256    string             `json:"csv_sha256"`
	Artefacto    ArtefactoMedido    `json:"artefacto"`
	Total        Conteo             `json:"total"`
	PorParte     []FilaDeParte      `json:"por_parte"`
	PorEstrato   []FilaDeParte      `json:"por_estrato"`
	PorCategoria []FilaDeCategoria  `json:"por_categoria"`
	Errores      []ErrorDeCategoria `json:"errores"`
}

// FilaDeParte es el resumen por parte o por estrato, que se reportan sin meta.
type FilaDeParte struct {
	Nombre string `json:"nombre"`
	Conteo
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
		grupos = append(grupos, FilaDeParte{Nombre: nombre, Conteo: resumir(subFilas, subDecisiones, "")})
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

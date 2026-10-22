package inteligencia

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
)

// Errores del artefacto del clasificador. Todos son de carga y ninguno depende de la fila.
var (
	ErrArtefactoInvalido    = errors.New("inteligencia: el artefacto del clasificador no es válido")
	ErrVersionDesconocida   = errors.New("inteligencia: versión de artefacto desconocida")
	ErrCategoriaDesconocida = errors.New("inteligencia: el artefacto trae una categoría fuera del catálogo")
	ErrHuellaDistinta       = errors.New("inteligencia: la huella del artefacto no coincide")
)

// VersionArtefacto es la única versión que esta implementación sabe leer.
const VersionArtefacto = "tfidf-2"

// LargoHuellaCorta es el prefijo del sha256 que viaja en `version_modelo`.
const LargoHuellaCorta = 12

// Modelo es el artefacto TF-IDF cargado. Los ocho primeros campos son los del contrato, en el
// mismo orden. El resto son derivados de solo lectura que se calculan una vez al cargar.
type Modelo struct {
	Version            string
	IDF                map[string]float64
	Centroides         map[Categoria]map[string]float64
	Lexico             map[string]Categoria
	UmbralCoseno       float64
	UmbralCosenoFuerte float64
	UmbralJW           float64
	Huella             string
	Prohibidas         map[Categoria]bool

	idfMaximo   float64
	categorias  []Categoria
	semillas    []string
	centroidesO map[Categoria][]caracteristica
	prohibidas  []Categoria
}

type documentoDeModelo struct {
	Version              string                       `json:"version"`
	IDF                  map[string]string            `json:"idf"`
	Centroides           map[string]map[string]string `json:"centroides"`
	Lexico               map[string]string            `json:"lexico"`
	CategoriasProhibidas *[]string                    `json:"categorias_prohibidas"`
	UmbralCoseno         string                       `json:"umbral_coseno"`
	UmbralCosenoFuerte   string                       `json:"umbral_coseno_fuerte"`
	UmbralJW             string                       `json:"umbral_jw"`
}

// CargarModelo valida los bytes del artefacto y devuelve el modelo listo para inferir.
//
// Los pesos viajan como texto decimal y se leen con strconv.ParseFloat. Un valor no finito, un
// separador de coma, una categoría fuera del catálogo visible o un umbral invertido son error: el
// clasificador no ajusta ni corrige lo que trae el artefacto.
func CargarModelo(datos []byte) (*Modelo, error) {
	var documento documentoDeModelo
	decodificador := json.NewDecoder(bytes.NewReader(datos))
	decodificador.DisallowUnknownFields()
	if err := decodificador.Decode(&documento); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrArtefactoInvalido, err)
	}
	if err := decodificador.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: hay datos después del JSON", ErrArtefactoInvalido)
	}
	if documento.Version != VersionArtefacto {
		return nil, fmt.Errorf("%w: %q", ErrVersionDesconocida, documento.Version)
	}
	if len(documento.IDF) == 0 || len(documento.Centroides) == 0 {
		return nil, fmt.Errorf("%w: sin idf o sin centroides", ErrArtefactoInvalido)
	}

	idf, err := pesosDeTexto(documento.IDF)
	if err != nil {
		return nil, fmt.Errorf("%w: idf: %w", ErrArtefactoInvalido, err)
	}
	modelo := &Modelo{
		Version:    documento.Version,
		IDF:        idf,
		Centroides: map[Categoria]map[string]float64{},
		Lexico:     map[string]Categoria{},
	}
	for codigo, pesos := range documento.Centroides {
		categoria := Categoria(codigo)
		if !EsCategoriaVisible(categoria) {
			return nil, fmt.Errorf("%w: %q", ErrCategoriaDesconocida, codigo)
		}
		convertidos, err := pesosDeTexto(pesos)
		if err != nil {
			return nil, fmt.Errorf("%w: centroide %q: %w", ErrArtefactoInvalido, codigo, err)
		}
		if len(convertidos) == 0 {
			return nil, fmt.Errorf("%w: el centroide %q está vacío", ErrArtefactoInvalido, codigo)
		}
		modelo.Centroides[categoria] = convertidos
	}

	if documento.CategoriasProhibidas == nil {
		return nil, fmt.Errorf("%w: falta categorias_prohibidas", ErrArtefactoInvalido)
	}
	prohibidas, ordenadas, err := validarProhibidas(*documento.CategoriasProhibidas, modelo.categoriasDeCentroide())
	if err != nil {
		return nil, err
	}
	modelo.Prohibidas = prohibidas
	modelo.prohibidas = ordenadas

	for palabra, codigo := range documento.Lexico {
		categoria := Categoria(codigo)
		if !EsCategoriaVisible(categoria) {
			return nil, fmt.Errorf("%w: léxico %q apunta a %q", ErrCategoriaDesconocida, palabra, codigo)
		}
		if modelo.Prohibidas[categoria] {
			return nil, fmt.Errorf("%w: el léxico %q apunta a la categoría prohibida %q",
				ErrArtefactoInvalido, palabra, codigo)
		}
		if _, existe := modelo.Centroides[categoria]; !existe {
			return nil, fmt.Errorf("%w: el léxico %q apunta a %q, que no tiene centroide",
				ErrCategoriaDesconocida, palabra, codigo)
		}
		modelo.Lexico[palabra] = categoria
	}

	if modelo.UmbralCoseno, err = umbralDeTexto(documento.UmbralCoseno); err != nil {
		return nil, fmt.Errorf("%w: umbral_coseno: %w", ErrArtefactoInvalido, err)
	}
	if modelo.UmbralCosenoFuerte, err = umbralDeTexto(documento.UmbralCosenoFuerte); err != nil {
		return nil, fmt.Errorf("%w: umbral_coseno_fuerte: %w", ErrArtefactoInvalido, err)
	}
	if modelo.UmbralJW, err = umbralDeTexto(documento.UmbralJW); err != nil {
		return nil, fmt.Errorf("%w: umbral_jw: %w", ErrArtefactoInvalido, err)
	}
	if modelo.UmbralCoseno >= modelo.UmbralCosenoFuerte {
		return nil, fmt.Errorf("%w: el umbral débil (%g) no es menor que el fuerte (%g)",
			ErrArtefactoInvalido, modelo.UmbralCoseno, modelo.UmbralCosenoFuerte)
	}

	suma := sha256.Sum256(datos)
	modelo.Huella = hex.EncodeToString(suma[:])
	modelo.ordenar()
	return modelo, nil
}

// validarProhibidas convierte la lista del artefacto en un conjunto y en el corte ordenado.
//
// La lista se escribe ordenada y sin repetidos para que el artefacto sea diffeable, así que una lista
// desordenada o con duplicados es un error de formato, no una variante válida. La categoría prohibida
// no necesita centroide: puede ser una clase que el modelo nunca va a predecir y aun así se declara.
// Si todas las categorías con centroide estuvieran prohibidas, el clasificador no podría decidir
// nada, de modo que ese caso también es error.
func validarProhibidas(lista []string, conCentroide []Categoria) (map[Categoria]bool, []Categoria, error) {
	if !sort.StringsAreSorted(lista) {
		return nil, nil, fmt.Errorf("%w: categorias_prohibidas no está ordenada", ErrArtefactoInvalido)
	}
	conjunto := make(map[Categoria]bool, len(lista))
	ordenadas := make([]Categoria, 0, len(lista))
	for _, codigo := range lista {
		categoria := Categoria(codigo)
		if categoria == "" {
			return nil, nil, fmt.Errorf("%w: categorias_prohibidas trae un código vacío", ErrArtefactoInvalido)
		}
		if !EsCategoriaVisible(categoria) {
			return nil, nil, fmt.Errorf("%w: categorías prohibidas: %q", ErrCategoriaDesconocida, codigo)
		}
		if conjunto[categoria] {
			return nil, nil, fmt.Errorf("%w: la categoría prohibida %q está repetida", ErrArtefactoInvalido, codigo)
		}
		if entrada, existe := BuscarCategoria(categoria); existe && entrada.Grupo == "ingreso" {
			return nil, nil, fmt.Errorf("%w: la categoría prohibida %q es de ingreso", ErrArtefactoInvalido, codigo)
		}
		conjunto[categoria] = true
		ordenadas = append(ordenadas, categoria)
	}
	if len(conCentroide) > 0 {
		todas := true
		for _, categoria := range conCentroide {
			if !conjunto[categoria] {
				todas = false
				break
			}
		}
		if todas {
			return nil, nil, fmt.Errorf("%w: todas las categorías con centroide están prohibidas", ErrArtefactoInvalido)
		}
	}
	return conjunto, ordenadas, nil
}

// categoriasDeCentroide devuelve, ordenadas, las categorías que el modelo puede predecir.
func (m *Modelo) categoriasDeCentroide() []Categoria {
	salida := make([]Categoria, 0, len(m.Centroides))
	for categoria := range m.Centroides {
		salida = append(salida, categoria)
	}
	sort.Slice(salida, func(i, j int) bool { return salida[i] < salida[j] })
	return salida
}

// VerificarHuella compara la huella del modelo con la esperada.
func VerificarHuella(modelo *Modelo, esperada string) error {
	if modelo == nil || esperada == "" || modelo.Huella != esperada {
		return fmt.Errorf("%w: esperada %q", ErrHuellaDistinta, esperada)
	}
	return nil
}

// VersionModelo identifica al clasificador y su artefacto en la columna `version_modelo`.
func (m *Modelo) VersionModelo() string {
	if m == nil {
		return ""
	}
	corta := m.Huella
	if len(corta) > LargoHuellaCorta {
		corta = corta[:LargoHuellaCorta]
	}
	return "tfidf:" + m.Version + ":" + corta
}

// Categorias devuelve las categorías con centroide, en orden alfabético.
func (m *Modelo) Categorias() []Categoria {
	if m == nil {
		return nil
	}
	copia := make([]Categoria, len(m.categorias))
	copy(copia, m.categorias)
	return copia
}

// ordenar fija los recorridos deterministas: nunca se decide iterando un map de Go.
func (m *Modelo) ordenar() {
	m.categorias = make([]Categoria, 0, len(m.Centroides))
	for categoria := range m.Centroides {
		m.categorias = append(m.categorias, categoria)
	}
	sort.Slice(m.categorias, func(i, j int) bool { return m.categorias[i] < m.categorias[j] })

	m.centroidesO = make(map[Categoria][]caracteristica, len(m.Centroides))
	for categoria, pesos := range m.Centroides {
		m.centroidesO[categoria] = ordenarPesos(pesos)
	}

	m.semillas = make([]string, 0, len(m.Lexico))
	for palabra := range m.Lexico {
		m.semillas = append(m.semillas, palabra)
	}
	sort.Strings(m.semillas)

	if m.prohibidas != nil {
		sort.Slice(m.prohibidas, func(i, j int) bool { return m.prohibidas[i] < m.prohibidas[j] })
	}

	if len(m.IDF) > 0 {
		m.idfMaximo = 0
		for _, valor := range m.IDF {
			if valor > m.idfMaximo {
				m.idfMaximo = valor
			}
		}
	}
}

func pesosDeTexto(valores map[string]string) (map[string]float64, error) {
	convertidos := make(map[string]float64, len(valores))
	for clave, texto := range valores {
		if clave == "" {
			return nil, errors.New("característica vacía")
		}
		valor, err := strconv.ParseFloat(texto, 64)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", clave, err)
		}
		if math.IsNaN(valor) || math.IsInf(valor, 0) {
			return nil, fmt.Errorf("%q no es un número finito", clave)
		}
		if valor < 0 {
			return nil, fmt.Errorf("%q es negativo", clave)
		}
		convertidos[clave] = valor
	}
	return convertidos, nil
}

func umbralDeTexto(texto string) (float64, error) {
	valor, err := strconv.ParseFloat(texto, 64)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(valor) || math.IsInf(valor, 0) || valor <= 0 || valor > 1 {
		return 0, fmt.Errorf("%q no está en (0, 1]", texto)
	}
	return valor, nil
}

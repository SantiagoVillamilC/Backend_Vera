package inteligencia

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Cuantizacion y Tolerancia fijan la comparación de puntajes con los umbrales, como en el contrato.
const (
	Cuantizacion = 1e-6
	Tolerancia   = 1e-9
)

// caracteristica es un peso de una característica del vector. Los vectores se recorren como cortes
// ordenados y nunca como map, porque la suma en punto flotante no es asociativa.
type caracteristica struct {
	clave string
	valor float64
}

// Predecir devuelve la categoría del comercio y si decidió. Recibe solo el nombre normalizado y la
// categoría del MCC: no hay monto, ni identidad del usuario, ni nombre de una persona.
//
// El orden es el de `analisis/clasificador_tfidf.py:predecir`. Si el mejor coseno alcanza el umbral
// fuerte, esa categoría decide y el léxico no la sobreescribe. Si es débil, el léxico puede decidir
// por Jaro-Winkler. Si el léxico no aplica, decide el coseno solo cuando alcanza el umbral débil. Si
// no, el clasificador se abstiene. Antes de devolver, la categoría ganadora pasa por `decidir`: una
// categoría prohibida por el artefacto se convierte en abstención y no en el segundo lugar.
func Predecir(modelo *Modelo, nombre, mccCategoria string) (Categoria, bool) {
	if modelo == nil || nombre == "" {
		return "", false
	}
	texto := nombre
	if mccCategoria != "" {
		texto = nombre + " mcc:" + mccCategoria
	}
	vector := vectorDeTexto(texto, modelo)

	var mejor Categoria
	mejorPuntaje := -1.0
	if len(vector) > 0 {
		for _, categoria := range modelo.categorias {
			puntaje := coseno(vector, modelo.centroidesO[categoria])
			if puntaje > mejorPuntaje {
				mejor, mejorPuntaje = categoria, puntaje
			}
		}
	}
	if mejor != "" && alcanza(mejorPuntaje, modelo.UmbralCosenoFuerte) {
		return decidir(modelo, mejor)
	}
	if categoria, encontrada := categoriaPorLexico(nombre, modelo); encontrada {
		return decidir(modelo, categoria)
	}
	if mejor == "" || !alcanza(mejorPuntaje, modelo.UmbralCoseno) {
		return "", false
	}
	return decidir(modelo, mejor)
}

// decidir aplica la lista de categorías prohibidas del artefacto antes de devolver una categoría.
// Si la ganadora está prohibida, el clasificador se abstiene en vez de entregar la decisión al
// segundo centroide, que daría otra categoría. El centroide prohibido se queda en el modelo para que
// pueda ganar y forzar la abstención.
func decidir(modelo *Modelo, categoria Categoria) (Categoria, bool) {
	if modelo == nil || categoria == "" || modelo.Prohibidas[categoria] {
		return "", false
	}
	return categoria, true
}

// categoriaPorLexico aplica el léxico del clasificador, que es otro que el de la cascada.
func categoriaPorLexico(nombre string, modelo *Modelo) (Categoria, bool) {
	palabrasDelNombre := palabras(nombre)
	if len(palabrasDelNombre) == 0 || len(modelo.semillas) == 0 {
		return "", false
	}
	votos := map[Categoria]float64{}
	for _, palabra := range palabrasDelNombre {
		for _, semilla := range modelo.semillas {
			categoria := modelo.Lexico[semilla]
			similitud := jaroWinkler(palabra, semilla)
			if !alcanza(similitud, modelo.UmbralJW) || similitud <= votos[categoria] {
				continue
			}
			votos[categoria] = similitud
		}
	}
	if len(votos) == 0 {
		return "", false
	}
	mejor := Categoria("")
	mejorVoto := 0.0
	for _, categoria := range modelo.categorias {
		voto, existe := votos[categoria]
		if !existe || voto <= mejorVoto {
			continue
		}
		mejor, mejorVoto = categoria, voto
	}
	if mejor == "" {
		return "", false
	}
	return mejor, true
}

// vectorDeTexto arma el vector TF-IDF de palabras y trigramas con tf sublineal.
func vectorDeTexto(texto string, modelo *Modelo) []caracteristica {
	conteo := caracteristicasDeTexto(texto)
	vector := make([]caracteristica, 0, len(conteo))
	for _, par := range conteo {
		idf, existe := modelo.IDF[par.clave]
		if !existe {
			idf = modelo.idfMaximo
		}
		vector = append(vector, caracteristica{clave: par.clave, valor: (1 + math.Log(par.valor)) * idf})
	}
	sort.Slice(vector, func(i, j int) bool { return vector[i].clave < vector[j].clave })
	return vector
}

// caracteristicasDeTexto combina palabras con prefijo `w:` y trigramas con prefijo `c:`, con los
// genéricos legales fuera y el texto de los trigramas sin ellos, igual que la referencia.
func caracteristicasDeTexto(texto string) []caracteristica {
	conteo := map[string]float64{}
	for _, palabra := range palabras(texto) {
		conteo["w:"+palabra]++
	}
	for _, trigrama := range trigramas(quitarGenericos(texto), 3) {
		conteo["c:"+trigrama]++
	}
	salida := make([]caracteristica, 0, len(conteo))
	for clave, valor := range conteo {
		salida = append(salida, caracteristica{clave: clave, valor: valor})
	}
	sort.Slice(salida, func(i, j int) bool { return salida[i].clave < salida[j].clave })
	return salida
}

// coseno devuelve el coseno de dos vectores dispersos ordenados, acotado a [0, 1].
func coseno(a, b []caracteristica) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var punto, normaA, normaB float64
	for _, par := range a {
		normaA += par.valor * par.valor
	}
	for _, par := range b {
		normaB += par.valor * par.valor
	}
	for i, j := 0, 0; i < len(a) && j < len(b); {
		switch {
		case a[i].clave < b[j].clave:
			i++
		case a[i].clave > b[j].clave:
			j++
		default:
			punto += a[i].valor * b[j].valor
			i++
			j++
		}
	}
	norma := math.Sqrt(normaA) * math.Sqrt(normaB)
	if norma == 0 {
		return 0
	}
	return math.Min(1, math.Max(0, punto/norma))
}

// cuantizar lleva el puntaje a 1e-6 para que una diferencia de la última cifra no cambie la decisión.
func cuantizar(valor float64) float64 {
	return math.Round(valor/Cuantizacion) * Cuantizacion
}

// alcanza compara un puntaje con un umbral después de cuantizar los dos lados.
func alcanza(puntaje, umbral float64) bool {
	return cuantizar(puntaje) >= cuantizar(umbral)-Tolerancia
}

func ordenarPesos(pesos map[string]float64) []caracteristica {
	salida := make([]caracteristica, 0, len(pesos))
	for clave, valor := range pesos {
		salida = append(salida, caracteristica{clave: clave, valor: valor})
	}
	sort.Slice(salida, func(i, j int) bool { return salida[i].clave < salida[j].clave })
	return salida
}

// ModoClasificador es el interruptor del escalón 6.
type ModoClasificador string

const (
	// ModoClasificadorApagado deja la cascada en el primer corte.
	ModoClasificadorApagado ModoClasificador = "apagado"
	// ModoClasificadorTFIDF enciende el clasificador local con el artefacto embebido.
	ModoClasificadorTFIDF ModoClasificador = "tfidf"
)

// ModoClasificadorPorDefecto conserva el primer corte cuando la variable no está definida.
const ModoClasificadorPorDefecto = ModoClasificadorApagado

// InterpretarModoClasificador valida el valor de la variable CLASIFICADOR. Un valor vacío es el
// modo por defecto y no un error, para que desplegar sin la variable no cambie el lote.
func InterpretarModoClasificador(valor string) (ModoClasificador, error) {
	switch ModoClasificador(strings.TrimSpace(strings.ToLower(valor))) {
	case "":
		return ModoClasificadorPorDefecto, nil
	case ModoClasificadorApagado:
		return ModoClasificadorApagado, nil
	case ModoClasificadorTFIDF:
		return ModoClasificadorTFIDF, nil
	default:
		return "", fmt.Errorf("inteligencia: CLASIFICADOR debe ser %q o %q, no %q",
			ModoClasificadorApagado, ModoClasificadorTFIDF, valor)
	}
}

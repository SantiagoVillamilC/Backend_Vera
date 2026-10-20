package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
)

// leerFilas parsea un CSV completo y devuelve la cabecera y las filas, tolerando comas y comillas
// dentro de los nombres, que el RUES trae.
func leerFilas(crudo []byte) ([]string, [][]string, error) {
	lector := csv.NewReader(bytes.NewReader(crudo))
	lector.FieldsPerRecord = -1
	registros, err := lector.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("parsear el CSV: %w", err)
	}
	if len(registros) == 0 {
		return nil, nil, fmt.Errorf("el CSV está vacío")
	}
	cabecera := make([]string, len(registros[0]))
	for i, nombre := range registros[0] {
		cabecera[i] = strings.TrimSpace(strings.TrimPrefix(nombre, "\ufeff"))
	}
	return cabecera, registros[1:], nil
}

// indiceDeColumnas mapea el nombre de cada columna a su posición.
func indiceDeColumnas(cabecera []string) map[string]int {
	indice := make(map[string]int, len(cabecera))
	for posicion, nombre := range cabecera {
		indice[nombre] = posicion
	}
	return indice
}

// campo devuelve el valor de una columna, o vacío si la columna no existe.
func campo(fila []string, indice map[string]int, columna string) string {
	posicion, existe := indice[columna]
	if !existe || posicion >= len(fila) {
		return ""
	}
	return strings.TrimSpace(fila[posicion])
}

// leerLexico lee el CSV del léxico con las columnas palabra y categoria y devuelve sus pares y el
// sha256 del archivo medido.
func leerLexico(ruta string) ([]inteligencia.ParLexico, string, error) {
	crudo, err := os.ReadFile(ruta) //nolint:gosec // la ruta la pasa el operador.
	if err != nil {
		return nil, "", fmt.Errorf("leer el léxico: %w", err)
	}
	cabecera, registros, err := leerFilas(crudo)
	if err != nil {
		return nil, "", err
	}
	indice := indiceDeColumnas(cabecera)
	for _, obligatoria := range []string{"palabra", "categoria"} {
		if _, existe := indice[obligatoria]; !existe {
			return nil, "", fmt.Errorf("el léxico no trae la columna %q", obligatoria)
		}
	}
	pares := make([]inteligencia.ParLexico, 0, len(registros))
	for numero, registro := range registros {
		palabra := strings.ToUpper(strings.TrimSpace(campo(registro, indice, "palabra")))
		categoria := strings.TrimSpace(campo(registro, indice, "categoria"))
		if palabra == "" {
			return nil, "", fmt.Errorf("la fila %d del léxico no trae palabra", numero+2)
		}
		pares = append(pares, inteligencia.ParLexico{Palabra: palabra,
			Categoria: inteligencia.Categoria(categoria)})
	}
	if len(pares) == 0 {
		return nil, "", fmt.Errorf("el léxico está vacío")
	}
	suma := sha256.Sum256(crudo)
	return pares, hex.EncodeToString(suma[:]), nil
}

// leerCatalogo lee el CSV del catálogo con las columnas nombre_normalizado y categoria, y devuelve
// el mapa de llaves y el sha256 del archivo medido. Falla cerrado ante una categoría que no sea de
// gasto o ante un nombre con dos categorías, porque cualquiera de las dos cosas mide otra cosa.
func leerCatalogo(ruta string) (map[string]inteligencia.Categoria, string, error) {
	crudo, err := os.ReadFile(ruta) //nolint:gosec // la ruta la pasa el operador.
	if err != nil {
		return nil, "", fmt.Errorf("leer el catálogo: %w", err)
	}
	cabecera, registros, err := leerFilas(crudo)
	if err != nil {
		return nil, "", err
	}
	indice := indiceDeColumnas(cabecera)
	for _, obligatoria := range []string{"nombre_normalizado", "categoria"} {
		if _, existe := indice[obligatoria]; !existe {
			return nil, "", fmt.Errorf("el catálogo no trae la columna %q", obligatoria)
		}
	}
	catalogo := make(map[string]inteligencia.Categoria, len(registros))
	for numero, registro := range registros {
		clave := campo(registro, indice, "nombre_normalizado")
		categoria := campo(registro, indice, "categoria")
		if clave == "" {
			return nil, "", fmt.Errorf("la fila %d del catálogo no trae nombre", numero+2)
		}
		if _, esDeGasto := categoriasDeGasto[categoria]; !esDeGasto {
			return nil, "", fmt.Errorf("la fila %d del catálogo trae la categoría %q, fuera de las diez de gasto",
				numero+2, categoria)
		}
		if previa, existe := catalogo[clave]; existe && string(previa) != categoria {
			return nil, "", fmt.Errorf("el nombre %q aparece con dos categorías: %s y %s", clave, previa, categoria)
		}
		catalogo[clave] = inteligencia.Categoria(categoria)
	}
	if len(catalogo) == 0 {
		return nil, "", fmt.Errorf("el catálogo está vacío")
	}
	suma := sha256.Sum256(crudo)
	return catalogo, hex.EncodeToString(suma[:]), nil
}

// esSi interpreta las formas en que una persona escribe un sí en la hoja.
func esSi(valor string) bool {
	switch strings.ToLower(strings.TrimSpace(valor)) {
	case "si", "sí", "s", "true", "1", "x":
		return true
	default:
		return false
	}
}

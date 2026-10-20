package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strings"
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

// esSi interpreta las formas en que una persona escribe un sí en la hoja.
func esSi(valor string) bool {
	switch strings.ToLower(strings.TrimSpace(valor)) {
	case "si", "sí", "s", "true", "1", "x":
		return true
	default:
		return false
	}
}

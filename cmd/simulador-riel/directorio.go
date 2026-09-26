package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// EntradaDirectorio es lo que el directorio resuelve para una llave: entidad y producto, nunca la cuenta.
type EntradaDirectorio struct {
	Entidad  string `json:"entidad"`
	Producto string `json:"producto"`
}

// Directorio es el catálogo de llaves del directorio simulado, indexado por "tipo:valor".
type Directorio struct {
	entradas map[string]EntradaDirectorio
}

// NuevoDirectorio copia el catálogo para no compartir el mapa de la config.
func NuevoDirectorio(entradas map[string]EntradaDirectorio) *Directorio {
	copia := make(map[string]EntradaDirectorio, len(entradas))
	for clave, entrada := range entradas {
		copia[clave] = entrada
	}
	return &Directorio{entradas: copia}
}

// Resolver devuelve la entrada registrada para la llave y si existe.
func (d *Directorio) Resolver(tipo, valor string) (EntradaDirectorio, bool) {
	entrada, ok := d.entradas[tipo+":"+valor]
	return entrada, ok
}

// leerDirectorioDelEntorno parsea el catálogo de llaves y reporta todos sus errores juntos.
func leerDirectorioDelEntorno() (map[string]EntradaDirectorio, []error) {
	crudo := strings.TrimSpace(os.Getenv("SIMULADOR_RIEL_DIRECTORIO"))
	if crudo == "" {
		return map[string]EntradaDirectorio{}, nil
	}
	var entradas map[string]EntradaDirectorio
	if err := json.Unmarshal([]byte(crudo), &entradas); err != nil {
		return nil, []error{fmt.Errorf("SIMULADOR_RIEL_DIRECTORIO must be a JSON object of \"tipo:valor\" to {entidad, producto}: %w", err)}
	}
	var fallos []error
	for clave, entrada := range entradas {
		tipo, valor, ok := strings.Cut(clave, ":")
		if !ok || strings.TrimSpace(tipo) == "" || strings.TrimSpace(valor) == "" {
			fallos = append(fallos, fmt.Errorf("SIMULADOR_RIEL_DIRECTORIO key %q must be \"tipo:valor\"", clave))
		}
		if strings.TrimSpace(entrada.Entidad) == "" || strings.TrimSpace(entrada.Producto) == "" {
			fallos = append(fallos, errors.New("SIMULADOR_RIEL_DIRECTORIO entry "+clave+" needs entidad and producto"))
		}
	}
	return entradas, fallos
}

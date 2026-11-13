package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// EntradaDirectorio es lo que el directorio resuelve para una llave: entidad y producto, nunca la cuenta.
type EntradaDirectorio struct {
	Entidad        string `json:"entidad"`
	Producto       string `json:"producto"`
	NombreComercio string `json:"nombre_comercio,omitempty"`
	MCC            string `json:"mcc,omitempty"`
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

// PorLlave devuelve la entrada del primer tipo registrado para esa llave, en orden estable, y
// prefiere la que trae datos de comercio cuando la misma llave aparece en varios tipos.
func (d *Directorio) PorLlave(valor string) (EntradaDirectorio, bool) {
	claves := make([]string, 0, len(d.entradas))
	for clave := range d.entradas {
		if _, v, ok := strings.Cut(clave, ":"); ok && v == valor {
			claves = append(claves, clave)
		}
	}
	if len(claves) == 0 {
		return EntradaDirectorio{}, false
	}
	sort.Strings(claves)
	for _, clave := range claves {
		if entrada := d.entradas[clave]; entrada.NombreComercio != "" || entrada.MCC != "" {
			return entrada, true
		}
	}
	return d.entradas[claves[0]], true
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
		if mcc := strings.TrimSpace(entrada.MCC); mcc != "" && !esMCC(mcc) {
			fallos = append(fallos, fmt.Errorf("SIMULADOR_RIEL_DIRECTORIO entry %s has mcc %q, must be four digits", clave, entrada.MCC))
		}
	}
	return entradas, fallos
}

func esMCC(codigo string) bool {
	if len(codigo) != 4 {
		return false
	}
	for _, r := range codigo {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

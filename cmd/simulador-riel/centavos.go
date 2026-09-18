package main

import (
	"errors"
	"strconv"
)

// ErrMontoInvalido reporta texto que no es un número entero de centavos.
var ErrMontoInvalido = errors.New("simulador-riel: el monto debe ser un entero de centavos como cadena")

// Centavos es un monto en unidades menores.
type Centavos int64

// Int64 devuelve las unidades menores crudas.
func (c Centavos) Int64() int64 { return int64(c) }

// ParsearCentavos es el único parser de un monto que llega como texto.
func ParsearCentavos(s string) (Centavos, error) {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, ErrMontoInvalido
	}
	return Centavos(v), nil
}

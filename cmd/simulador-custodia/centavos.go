package main

import (
	"encoding/json"
	"errors"
	"strconv"
)

// ErrMontoInvalido reporta texto que no es un número entero de centavos.
var ErrMontoInvalido = errors.New("simulador-custodia: el monto debe ser un entero de centavos como cadena")

// Centavos es un monto en unidades menores; viaja en JSON como cadena, nunca como número.
type Centavos int64

// MarshalJSON emite el monto como cadena JSON.
func (c Centavos) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(strconv.FormatInt(int64(c), 10))), nil
}

// UnmarshalJSON acepta solo una cadena JSON de centavos enteros.
func (c *Centavos) UnmarshalJSON(datos []byte) error {
	var s string
	if err := json.Unmarshal(datos, &s); err != nil {
		return ErrMontoInvalido
	}
	v, err := ParsearCentavos(s)
	if err != nil {
		return err
	}
	*c = v
	return nil
}

// ParsearCentavos es el único parser de un monto que llega como texto.
func ParsearCentavos(s string) (Centavos, error) {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, ErrMontoInvalido
	}
	return Centavos(v), nil
}

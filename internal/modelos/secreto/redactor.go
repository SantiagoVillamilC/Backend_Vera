// Package secreto oculta valores sensibles y caracteres de control antes de que un texto llegue a un
// registro, a un mensaje de error o a la salida estándar.
package secreto

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// longitudMinima evita que un valor trivial (por ejemplo "ok") oculte medio texto.
const longitudMinima = 6

// Redactor reemplaza una lista fija de secretos. Es inmutable: Nuevo devuelve una copia.
type Redactor struct{ valores []string }

// Nuevo crea un Redactor y descarta los valores demasiado cortos.
func Nuevo(valores ...string) Redactor {
	util := make([]string, 0, len(valores))
	for _, v := range valores {
		if len(v) >= longitudMinima {
			util = append(util, v)
		}
	}
	return Redactor{valores: util}
}

// Ocultar reemplaza solo los secretos y deja intacto el resto, saltos de línea incluidos. Sirve de
// última defensa sobre una salida de registro ya formateada.
func (r Redactor) Ocultar(texto string) string {
	for _, v := range r.valores {
		texto = strings.ReplaceAll(texto, v, "***")
	}
	return texto
}

// Limpiar oculta los secretos y quita los caracteres de control. Saltos de línea y tabuladores
// se vuelven espacios para que un texto externo no pueda falsificar líneas de registro.
func (r Redactor) Limpiar(texto string) string {
	for _, v := range r.valores {
		texto = strings.ReplaceAll(texto, v, "***")
	}
	return strings.Map(func(c rune) rune {
		switch {
		case c == '\n' || c == '\r' || c == '\t':
			return ' '
		case unicode.IsControl(c):
			return -1
		}
		return c
	}, texto)
}

// Recortar limita un texto a max runas y agrega puntos suspensivos si cortó algo.
func Recortar(texto string, max int) string {
	if utf8.RuneCountInString(texto) <= max {
		return texto
	}
	return string([]rune(texto)[:max]) + "..."
}

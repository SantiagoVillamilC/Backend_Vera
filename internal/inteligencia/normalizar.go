package inteligencia

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// LargoMaximoNombre es el tope de puntos de código del nombre normalizado, igual que en la
// referencia Python.
const LargoMaximoNombre = 40

// LargoClaveDeCatalogo es el tope de puntos de código con el que el evaluador corta el nombre antes
// de normalizarlo, igual que `nombre_qr` de la referencia Python. Las claves del catálogo curado
// miden eso o menos.
const LargoClaveDeCatalogo = 25

var separadoresDeNombre = regexp.MustCompile(`[\s\p{Z}_\-]+`)

var espaciosDeNombre = regexp.MustCompile(`[\s\p{Z}]+`)

// NormalizarNombre replica `analisis/pagos.py:73` y su `_un_paso`, iterado hasta el punto fijo.
func NormalizarNombre(nombre string) string {
	actual := unPasoDeNombre(nombre)
	for {
		siguiente := unPasoDeNombre(actual)
		if siguiente == actual {
			return actual
		}
		actual = siguiente
	}
}

// ClaveDeCatalogo arma la clave con la que se busca en el catálogo curado: el nombre cortado a
// LargoClaveDeCatalogo puntos de código y después normalizado, en el mismo orden que
// `clave_de_catalogo` del evaluador. Cortar antes de normalizar es lo que hace que un nombre largo
// encuentre la fila que el evaluador también encuentra.
func ClaveDeCatalogo(nombre string) string {
	return NormalizarNombre(recorteDeQR(nombre))
}

// recorteDeQR replica `nombre_qr` de la referencia Python: mayúsculas, sin tildes salvo la Ñ,
// espacios colapsados y cortado a LargoClaveDeCatalogo puntos de código.
func recorteDeQR(nombre string) string {
	texto := norm.NFC.String(nombre)
	texto = strings.ToUpper(texto)
	texto = quitarTildesSalvoEnie(texto)
	texto = espaciosDeNombre.ReplaceAllString(texto, " ")
	runas := []rune(strings.TrimSpace(texto))
	if len(runas) > LargoClaveDeCatalogo {
		runas = runas[:LargoClaveDeCatalogo]
	}
	return strings.TrimSpace(string(runas))
}

func unPasoDeNombre(nombre string) string {
	texto := norm.NFC.String(nombre)
	texto = strings.ToUpper(texto)
	texto = separadoresDeNombre.ReplaceAllString(texto, "-")
	texto = strings.Trim(texto, "-")
	texto = quitarTildesSalvoEnie(texto)
	texto = strings.Map(func(r rune) rune {
		switch r {
		case '\'', '.', '&':
			return -1
		default:
			return r
		}
	}, texto)
	runas := []rune(texto)
	if len(runas) > LargoMaximoNombre {
		runas = runas[:LargoMaximoNombre]
	}
	return string(runas)
}

func quitarTildesSalvoEnie(texto string) string {
	var salida strings.Builder
	salida.Grow(len(texto))
	for _, r := range texto {
		if r == 'Ñ' {
			salida.WriteRune(r)
			continue
		}
		for _, descompuesto := range norm.NFD.String(string(r)) {
			if unicode.Is(unicode.Mn, descompuesto) {
				continue
			}
			salida.WriteRune(descompuesto)
		}
	}
	return salida.String()
}

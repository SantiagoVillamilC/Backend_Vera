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

var separadoresDeNombre = regexp.MustCompile(`[\s\p{Z}_\-]+`)

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

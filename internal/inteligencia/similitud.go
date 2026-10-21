package inteligencia

import (
	"strings"
	"unicode"
)

// palabrasGenericas son los sufijos y genéricos legales que no aportan a la identidad del comercio.
var palabrasGenericas = map[string]struct{}{
	"SAS": {}, "LTDA": {}, "SA": {}, "SUCURSAL": {}, "TIENDA": {}, "ALMACEN": {},
}

// palabras replica `analisis/similitud.py:palabras`. El reconocimiento es sobre runas, de modo que
// una Ñ es letra y no parte de una frontera, como en la referencia con `re.UNICODE`.
func palabras(texto string) []string {
	var salida []string
	var actual []rune
	cerrar := func() {
		if len(actual) == 0 {
			return
		}
		palabra := string(actual)
		if _, generico := palabrasGenericas[palabra]; !generico {
			salida = append(salida, palabra)
		}
		actual = actual[:0]
	}
	for _, r := range strings.ToUpper(texto) {
		if esCaracterDePalabra(r) {
			actual = append(actual, r)
			continue
		}
		cerrar()
	}
	cerrar()
	return salida
}

// quitarGenericos replica `analisis/similitud.py:quitar_genericos`. Solo elimina la palabra exacta,
// así que ALMACENES o TIENDAS conservan su token, y colapsa los espacios que deja el borrado.
func quitarGenericos(texto string) string {
	runas := []rune(texto)
	salida := make([]rune, 0, len(runas))
	for i := 0; i < len(runas); {
		if !esCaracterDePalabra(runas[i]) {
			salida = append(salida, runas[i])
			i++
			continue
		}
		fin := i
		for fin < len(runas) && esCaracterDePalabra(runas[fin]) {
			fin++
		}
		if _, generico := palabrasGenericas[strings.ToUpper(string(runas[i:fin]))]; generico {
			salida = append(salida, ' ')
		} else {
			salida = append(salida, runas[i:fin]...)
		}
		i = fin
	}
	return colapsarEspacios(string(salida))
}

// trigramas replica `analisis/similitud.py:trigramas`: relleno a cada lado y repetidos.
func trigramas(texto string, n int) []string {
	if n < 1 {
		return nil
	}
	relleno := []rune(" " + texto + " ")
	salida := make([]string, 0, len(relleno))
	for i := 0; i+n <= len(relleno); i++ {
		salida = append(salida, string(relleno[i:i+n]))
	}
	return salida
}

// jaro replica `analisis/similitud.py:jaro` sobre runas.
func jaro(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 || len(rb) == 0 {
		return 0
	}
	ventana := len(ra)
	if len(rb) > ventana {
		ventana = len(rb)
	}
	ventana = ventana/2 - 1
	if ventana < 0 {
		ventana = 0
	}
	usado := make([]bool, len(rb))
	var deA, deB []rune
	for i, caracter := range ra {
		inicio := i - ventana
		if inicio < 0 {
			inicio = 0
		}
		fin := i + ventana + 1
		if fin > len(rb) {
			fin = len(rb)
		}
		for j := inicio; j < fin; j++ {
			if usado[j] || rb[j] != caracter {
				continue
			}
			usado[j] = true
			deA = append(deA, caracter)
			break
		}
	}
	for j, tomado := range usado {
		if tomado {
			deB = append(deB, rb[j])
		}
	}
	coincidencias := len(deA)
	if coincidencias == 0 {
		return 0
	}
	transposiciones := 0
	for i := range deA {
		if deA[i] != deB[i] {
			transposiciones++
		}
	}
	transposiciones /= 2
	return (float64(coincidencias)/float64(len(ra)) +
		float64(coincidencias)/float64(len(rb)) +
		float64(coincidencias-transposiciones)/float64(coincidencias)) / 3
}

// jaroWinkler replica `analisis/similitud.py:jaro_winkler` con p fijo en 0.1 y prefijo de 4.
func jaroWinkler(a, b string) float64 {
	const (
		p          = 0.1
		prefijoMax = 4
		bonifica   = 0.7
	)
	base := jaro(a, b)
	if base <= bonifica {
		return base
	}
	ra, rb := []rune(a), []rune(b)
	largo := 0
	for i := 0; i < prefijoMax && i < len(ra) && i < len(rb); i++ {
		if ra[i] != rb[i] {
			break
		}
		largo++
	}
	return base + float64(largo)*p*(1.0-base)
}

func esCaracterDePalabra(r rune) bool {
	if r == '_' {
		return false
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func colapsarEspacios(texto string) string {
	var salida strings.Builder
	salida.Grow(len(texto))
	espacio := false
	for _, r := range texto {
		if unicode.IsSpace(r) {
			espacio = true
			continue
		}
		if espacio && salida.Len() > 0 {
			salida.WriteRune(' ')
		}
		espacio = false
		salida.WriteRune(r)
	}
	return salida.String()
}

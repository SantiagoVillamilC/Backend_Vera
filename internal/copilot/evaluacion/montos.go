package evaluacion

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

var (
	importeTexto  = regexp.MustCompile(`(?i)(?:\$\s*(-?[0-9][0-9.,]*)|(-?[0-9][0-9.,]*)\s*(?:COP|pesos)\b)`)
	enteroTexto   = regexp.MustCompile(`^[0-9]+$`)
	milesColombia = regexp.MustCompile(`^[0-9]{1,3}(?:\.[0-9]{3})+$`)
)

// Sólo autoriza importes presentes en resultados de ESTE ciclo; nunca usa el guion
// ni los valores esperados para aprobar una cifra. No convierte mediante flotantes.
func montosVerificables(texto string, resultados []json.RawMessage) bool {
	permitidos := make(map[int64]bool)
	var recorrer func(any)
	recorrer = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if s, ok := x["centavos"].(string); ok {
				if n, err := strconv.ParseInt(s, 10, 64); err == nil {
					permitidos[n] = true
				}
			}
			for _, h := range x {
				recorrer(h)
			}
		case []any:
			for _, h := range x {
				recorrer(h)
			}
		}
	}
	for _, raw := range resultados {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			recorrer(v)
		}
	}
	for _, match := range importeTexto.FindAllStringSubmatch(texto, -1) {
		numero := match[1]
		if numero == "" {
			numero = match[2]
		}
		valor, ok := centavosDeTexto(strings.TrimRight(numero, "."))
		if !ok || !permitidos[valor] {
			return false
		}
	}
	return true
}

func centavosDeTexto(numero string) (int64, bool) {
	signo := ""
	if strings.HasPrefix(numero, "-") {
		signo = "-"
		numero = strings.TrimPrefix(numero, "-")
	}
	entero, decimal := numero, "00"
	if strings.Contains(numero, ",") {
		partes := strings.Split(numero, ",")
		if len(partes) != 2 {
			return 0, false
		}
		entero, decimal = partes[0], partes[1]
		if strings.Contains(entero, ".") {
			if !milesColombia.MatchString(entero) {
				return 0, false
			}
			entero = strings.ReplaceAll(entero, ".", "")
		}
	} else if strings.Contains(numero, ".") {
		partes := strings.Split(numero, ".")
		if len(partes) == 2 && (len(partes[1]) == 1 || len(partes[1]) == 2) {
			entero, decimal = partes[0], partes[1]
		} else {
			if !milesColombia.MatchString(numero) {
				return 0, false
			}
			entero = strings.ReplaceAll(numero, ".", "")
		}
	}
	if !enteroTexto.MatchString(entero) || !enteroTexto.MatchString(decimal) || len(decimal) > 2 {
		return 0, false
	}
	if len(decimal) == 1 {
		decimal += "0"
	}
	valor, err := strconv.ParseInt(signo+entero+decimal, 10, 64)
	return valor, err == nil
}

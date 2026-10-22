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
	milesComa     = regexp.MustCompile(`^[0-9]{1,3}(?:,[0-9]{3})+$`)
)

// Sólo autoriza importes presentes en resultados de ESTE ciclo; nunca usa el guion
// ni los valores esperados para aprobar una cifra. No convierte mediante flotantes.
func montosVerificables(texto string, resultados []json.RawMessage) bool {
	return diagnosticoMontos(texto, resultados) == ""
}

func diagnosticoMontos(texto string, resultados []json.RawMessage) string {
	permitidos := importesPermitidos(resultados)
	for _, match := range importeTexto.FindAllStringSubmatch(texto, -1) {
		numero := match[1]
		if numero == "" {
			numero = match[2]
		}
		valor, ok := centavosDeTexto(strings.TrimRight(numero, "."))
		if !ok {
			return "FormatoMonetarioNoAdmitido"
		}
		if !permitidos[valor] {
			return "ImporteNoRespaldado"
		}
	}
	return ""
}

func importesPermitidos(resultados []json.RawMessage) map[int64]bool {
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
	return permitidos
}

func centavosDeTexto(numero string) (int64, bool) {
	signo := ""
	if strings.HasPrefix(numero, "-") {
		signo = "-"
		numero = strings.TrimPrefix(numero, "-")
	}
	entero, decimal := numero, "00"
	coma, punto := strings.LastIndex(numero, ","), strings.LastIndex(numero, ".")
	if coma >= 0 && punto >= 0 {
		indice := max(coma, punto)
		entero, decimal = numero[:indice], numero[indice+1:]
		if coma > punto {
			if !milesColombia.MatchString(entero) {
				return 0, false
			}
			entero = strings.ReplaceAll(entero, ".", "")
		} else {
			if !milesComa.MatchString(entero) {
				return 0, false
			}
			entero = strings.ReplaceAll(entero, ",", "")
		}
	} else if indice := max(coma, punto); indice >= 0 {
		separador := string(numero[indice])
		if strings.Count(numero, separador) == 1 && len(numero)-indice-1 <= 2 {
			entero, decimal = numero[:indice], numero[indice+1:]
		} else {
			if !milesColombia.MatchString(numero) && !milesComa.MatchString(numero) {
				return 0, false
			}
			entero = strings.ReplaceAll(numero, separador, "")
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

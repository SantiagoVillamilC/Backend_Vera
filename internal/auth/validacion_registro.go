package auth

import (
	"strings"
	"unicode"

	"github.com/nyaruka/phonenumbers"
	"golang.org/x/text/unicode/norm"
)

const (
	// regionPorDefecto es la region con la que se interpreta un celular sin prefijo.
	regionPorDefecto = "CO"
	// minRunasNombre y maxRunasNombre acotan el nombre completo de una persona.
	minRunasNombre = 2
	maxRunasNombre = 100
)

// NormalizarCorreo pasa el correo a NFC y minusculas para que la unicidad sea estable.
func NormalizarCorreo(correo string) string {
	return strings.ToLower(norm.NFC.String(strings.TrimSpace(correo)))
}

// NormalizarCelular interpreta el numero en CO y lo devuelve en E.164.
func NormalizarCelular(celular string) (string, error) {
	numero, err := phonenumbers.Parse(strings.TrimSpace(celular), regionPorDefecto)
	if err != nil || !phonenumbers.IsValidNumber(numero) {
		return "", ErrCelularInvalido
	}
	return phonenumbers.Format(numero, phonenumbers.E164), nil
}

// NormalizarNombreCompleto valida el nombre con la regla minima y lo devuelve en NFC.
func NormalizarNombreCompleto(nombre string) (string, error) {
	normalizado := norm.NFC.String(strings.TrimSpace(nombre))
	if !nombreValido(normalizado) {
		return "", ErrNombreInvalido
	}
	return normalizado, nil
}

// nombreValido aplica el perfil minimo: letras y marcas, espacios simples, apostrofo o guion, un solo script.
func nombreValido(nombre string) bool {
	runas := []rune(nombre)
	if len(runas) < minRunasNombre || len(runas) > maxRunasNombre {
		return false
	}
	scripts := map[string]struct{}{}
	hayLetra := false
	anteriorEspacio, anteriorSeparador := true, true
	for _, r := range runas {
		switch {
		case r == ' ':
			if anteriorEspacio {
				return false
			}
			anteriorEspacio, anteriorSeparador = true, true
		case r == '\'' || r == '-' || r == '\u2019':
			if anteriorSeparador {
				return false
			}
			anteriorEspacio, anteriorSeparador = false, true
		case unicode.IsLetter(r) || unicode.IsMark(r):
			if unicode.IsLetter(r) {
				hayLetra = true
				scripts[scriptDeLetra(r)] = struct{}{}
			}
			anteriorEspacio, anteriorSeparador = false, false
		default:
			return false
		}
	}
	if anteriorEspacio || anteriorSeparador {
		return false
	}
	return hayLetra && len(scripts) <= 1
}

// scriptDeLetra clasifica una letra por script para rechazar la mezcla de alfabetos.
func scriptDeLetra(r rune) string {
	switch {
	case unicode.In(r, unicode.Latin):
		return "latin"
	case unicode.In(r, unicode.Greek):
		return "griego"
	case unicode.In(r, unicode.Cyrillic):
		return "cirilico"
	case unicode.In(r, unicode.Arabic):
		return "arabe"
	case unicode.In(r, unicode.Hebrew):
		return "hebreo"
	case unicode.In(r, unicode.Han):
		return "han"
	case unicode.In(r, unicode.Hiragana):
		return "hiragana"
	case unicode.In(r, unicode.Katakana):
		return "katakana"
	case unicode.In(r, unicode.Hangul):
		return "hangul"
	case unicode.In(r, unicode.Devanagari):
		return "devanagari"
	default:
		return "otro"
	}
}

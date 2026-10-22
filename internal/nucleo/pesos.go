package nucleo

import "math"

// ParsearPesosEnteros convierte pesos positivos sin separadores a centavos comprobando el rango.
func ParsearPesosEnteros(texto string) (Centavos, error) {
	if len(texto) == 0 || texto[0] < '1' || texto[0] > '9' {
		return 0, ErrMontoInvalido
	}
	for _, r := range texto {
		if r < '0' || r > '9' {
			return 0, ErrMontoInvalido
		}
	}
	pesos, err := ParsearCentavos(texto)
	if err != nil || pesos <= 0 || pesos.Int64() > math.MaxInt64/100 {
		return 0, ErrMontoInvalido
	}
	return Centavos(pesos.Int64() * 100), nil
}

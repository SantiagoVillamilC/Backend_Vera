package copilot

import "strings"

// Una escala no se deduce del saldo/límite citado en turnos anteriores.
// No interpreta dígitos aislados: pueden ser la selección de un destinatario.
func aclaracionImporteAislado(historial []TurnoIA) string {
	if len(historial) == 0 {
		return ""
	}
	ultimo := historial[len(historial)-1]
	if ultimo.Rol != "usuario" || len(ultimo.Partes) != 1 || ultimo.Partes[0].Texto == "" {
		return ""
	}
	texto := strings.ToLower(strings.Trim(ultimo.Partes[0].Texto, " \t\r\n.,!?¿¡…"))
	palabras := strings.Fields(texto)
	for len(palabras) > 1 {
		switch strings.Trim(palabras[0], ",.") {
		case "pues", "eh", "mmm", "bueno", "entonces":
			palabras = palabras[1:]
		default:
			return ""
		}
	}
	if len(palabras) != 1 {
		return ""
	}
	switch palabras[0] {
	case "veinte", "treinta", "cuarenta", "cincuenta", "sesenta", "setenta", "ochenta", "noventa", "cien":
		return "¿Te refieres a " + palabras[0] + " pesos o a " + palabras[0] + " mil pesos? ¿Quieres sólo consultar si puedes enviarlos o preparar una propuesta? No se ha preparado ni enviado dinero en este turno."
	default:
		return ""
	}
}

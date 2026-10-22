package copilot

import "strings"

// revelaInstrucciones detecta copias literales de párrafos privados antes de
// devolver o persistir el texto del modelo. No pretende detectar paráfrasis.
func revelaInstrucciones(texto string) bool {
	normalizado := strings.ToLower(strings.Join(strings.Fields(texto), " "))
	for _, parrafo := range strings.Split(instruccionesVera, "\n") {
		parrafo = strings.ToLower(strings.Join(strings.Fields(parrafo), " "))
		if len(parrafo) >= 80 && strings.Contains(normalizado, parrafo) {
			return true
		}
	}
	return false
}

// validarRolesHistorial impide que un protocolo persistido eleve datos a sistema,
// independientemente del adaptador de IA seleccionado.
func validarRolesHistorial(historial []TurnoIA) error {
	for _, t := range historial {
		if t.Rol != "usuario" && t.Rol != "asistente" && t.Rol != "herramienta" {
			return ErrSolicitudIA
		}
		if len(t.Partes) == 0 {
			return ErrSolicitudIA
		}
		for _, p := range t.Partes {
			if (p.Llamada != nil && t.Rol != "asistente") || (p.Resultado != nil && t.Rol != "herramienta") || (p.Audio != nil && t.Rol != "usuario") {
				return ErrSolicitudIA
			}
			if t.Rol == "herramienta" && (p.Resultado == nil || p.Texto != "") {
				return ErrSolicitudIA
			}
		}
	}
	return nil
}

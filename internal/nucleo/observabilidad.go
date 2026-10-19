package nucleo

import "context"

// Campo es un par clave/valor del contexto de un error.
type Campo struct {
	Clave string
	Valor string
}

// ContextoError ubica la falla: operación, canal y campos del dominio.
type ContextoError struct {
	Operacion string
	Canal     string
	Campos    []Campo
}

// CanalSoloLog declara el único destino del escalamiento: log, sin aviso a persona, ni tickets, ni SLA.
const CanalSoloLog = "solo_log"

// RastreadorErrores es el puerto del núcleo; main inyecta la implementación.
type RastreadorErrores interface {
	Capturar(ctx context.Context, err error, contexto ContextoError)
}

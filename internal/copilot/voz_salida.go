package copilot

import "context"

// SintetizadorVoz convierte una respuesta ya calculada en un WAV, sin herramientas.
type SintetizadorVoz interface {
	Sintetizar(ctx context.Context, texto, voz string) ([]byte, error)
}

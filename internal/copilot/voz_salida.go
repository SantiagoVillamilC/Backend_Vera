package copilot

import "context"

// SintetizadorVoz convierte una respuesta ya calculada en un WAV, sin herramientas.
type SintetizadorVoz interface {
	Sintetizar(ctx context.Context, texto, voz string) ([]byte, error)
}

// SintetizadorVozFlujo entrega PCM16 mono de 24 kHz conforme llega y devuelve
// un WAV completo únicamente cuando el proveedor confirma el fin de la síntesis.
type SintetizadorVozFlujo interface {
	SintetizarFlujo(ctx context.Context, texto, voz string, recibir func([]byte) error) ([]byte, error)
}

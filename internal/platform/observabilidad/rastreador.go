// Package observabilidad implementa el adaptador del puerto de rastreo de errores del núcleo.
package observabilidad

import (
	"context"
	"log/slog"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
)

// RastreadorSlog es el relleno del puerto: una línea ERROR del logger global.
type RastreadorSlog struct{}

// Capturar emite la captura por slog a nivel error.
func (RastreadorSlog) Capturar(ctx context.Context, err error, contexto nucleo.ContextoError) {
	capturar(slog.Default(), ctx, err, contexto)
}

// capturar separa la emisión para poder probarla sin el logger global.
func capturar(logger *slog.Logger, ctx context.Context, err error, contexto nucleo.ContextoError) {
	campos := make([]slog.Attr, 0, len(contexto.Campos))
	for _, c := range contexto.Campos {
		campos = append(campos, slog.String(c.Clave, c.Valor))
	}
	logger.ErrorContext(ctx, "error del nucleo",
		"operacion", contexto.Operacion,
		"canal", contexto.Canal,
		"contexto", slog.GroupValue(campos...),
		"err", err,
	)
}

var _ nucleo.RastreadorErrores = RastreadorSlog{}

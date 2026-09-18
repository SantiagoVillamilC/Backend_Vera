package nucleo

import (
	"context"
	"fmt"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/jackc/pgx/v5/pgtype"
)

// RegistrarExtractoCustodio lee el corte del simulador y guarda su fila en una transacción propia.
func (s *Servicio) RegistrarExtractoCustodio(ctx context.Context) (ExtractoCustodio, error) {
	extracto, err := s.custodio.Extracto(ctx)
	if err != nil {
		return ExtractoCustodio{}, fmt.Errorf("nucleo: leer el extracto del custodio: %w", err)
	}
	_, err = enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			return struct{}{}, q.InsertarExtractoCustodio(ctx, repositorio.InsertarExtractoCustodioParams{
				CorteEn: pgtype.Timestamptz{Time: extracto.CorteEn, Valid: true},
				Saldo:   extracto.Saldo.Int64(),
				Origen:  extracto.Origen,
			})
		})
	if err != nil {
		return ExtractoCustodio{}, fmt.Errorf("nucleo: guardar el extracto del custodio: %w", err)
	}
	return extracto, nil
}

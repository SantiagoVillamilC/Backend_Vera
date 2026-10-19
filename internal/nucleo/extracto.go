package nucleo

import (
	"context"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/jackc/pgx/v5/pgtype"
)

// RegistrarExtractoCustodio pide el corte al simulador y guarda su fila, con el corte pedido, en una transacción propia.
func (s *Servicio) RegistrarExtractoCustodio(ctx context.Context, corteEn time.Time) (ExtractoCustodio, error) {
	extracto, err := s.custodio.Extracto(ctx, corteEn)
	if err != nil {
		return ExtractoCustodio{}, fmt.Errorf("nucleo: leer el extracto del custodio: %w", err)
	}
	_, err = enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			return struct{}{}, q.InsertarExtractoCustodio(ctx, repositorio.InsertarExtractoCustodioParams{
				CorteEn: pgtype.Timestamptz{Time: corteEn, Valid: true},
				Saldo:   extracto.Saldo.Int64(),
				Origen:  extracto.Origen,
			})
		})
	if err != nil {
		return ExtractoCustodio{}, fmt.Errorf("nucleo: guardar el extracto del custodio: %w", err)
	}
	return extracto, nil
}

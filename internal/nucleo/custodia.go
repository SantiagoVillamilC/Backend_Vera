package nucleo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrExtractoDeCustodiaAusente reporta que no hay corte guardado y la corrida siguiente reintenta sin fabricar descuadre.
var ErrExtractoDeCustodiaAusente = errors.New("nucleo: no hay extracto del custodio para el corte pedido")

// ResultadoCustodia es la comparación del invariante 2 en un corte, todavía sin fila de descuadre.
type ResultadoCustodia struct {
	CorteEn      time.Time
	MontoInterno Centavos
	MontoExterno Centavos
	Diferencia   Centavos
}

// Cuadra reporta si los dos lados coinciden con igualdad exacta.
func (r ResultadoCustodia) Cuadra() bool { return r.Diferencia == 0 }

// CompararCustodia compara el extracto guardado al corte con el saldo interno de custodia hasta ese mismo corte.
func (s *Servicio) CompararCustodia(ctx context.Context, corteEn time.Time) (ResultadoCustodia, error) {
	return enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (ResultadoCustodia, error) {
			return compararCustodiaEnTx(ctx, q, corteEn)
		})
}

// compararCustodiaEnTx lee los dos lados en el Querier dado, para que una corrida compare dentro de su propia transacción.
func compararCustodiaEnTx(ctx context.Context, q *repositorio.Queries, corteEn time.Time) (ResultadoCustodia, error) {
	corte := pgtype.Timestamptz{Time: corteEn, Valid: true}
	extracto, err := q.ExtractoPorCorte(ctx, corte)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ResultadoCustodia{}, fmt.Errorf("%w: %s", ErrExtractoDeCustodiaAusente, corteEn.Format(time.RFC3339Nano))
		}
		return ResultadoCustodia{}, fmt.Errorf("nucleo: leer el extracto del custodio al corte: %w", err)
	}
	interno, err := q.SaldoDeCuentaHasta(ctx, repositorio.SaldoDeCuentaHastaParams{CuentaID: CuentaCustodiaBanco, CorteEn: corte})
	if err != nil {
		return ResultadoCustodia{}, fmt.Errorf("nucleo: sumar la custodia hasta el corte: %w", err)
	}
	diferencia, err := Centavos(extracto.Saldo).Restar(Centavos(interno))
	if err != nil {
		return ResultadoCustodia{}, err
	}
	return ResultadoCustodia{
		CorteEn:      extracto.CorteEn.Time.UTC(),
		MontoInterno: Centavos(interno),
		MontoExterno: Centavos(extracto.Saldo),
		Diferencia:   diferencia,
	}, nil
}

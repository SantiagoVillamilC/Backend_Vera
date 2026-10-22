// Package copilot contiene el asistente Vera y su persistencia aislada del núcleo financiero.
package copilot

import (
	"context"
	"fmt"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/internal/repositorio"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnTransaccionAsistente limita los privilegios al módulo y revierte el turno completo ante error.
// Las llamadas al proveedor se realizan fuera de esta transacción.
func EnTransaccionAsistente[T any](ctx context.Context, pool *pgxpool.Pool, fn func(*repositorio.Queries) (T, error)) (T, error) {
	var cero T
	tx, err := pool.Begin(ctx)
	if err != nil {
		return cero, fmt.Errorf("asistente: abrir transacción: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE rol_asistente"); err != nil {
		return cero, fmt.Errorf("asistente: cambiar rol: %w", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '5s'"); err != nil {
		return cero, fmt.Errorf("asistente: limitar espera: %w", err)
	}
	resultado, err := fn(repositorio.New(tx))
	if err != nil {
		return cero, err
	}
	if err := tx.Commit(ctx); err != nil {
		return cero, fmt.Errorf("asistente: confirmar transacción: %w", err)
	}
	return resultado, nil
}

package nucleo

import (
	"context"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RolNucleo es el rol de Postgres con el que el núcleo ejecuta toda consulta, no el rol de login del pool.
const RolNucleo = "rol_nucleo"

// sentenciaCambioDeRol baja la transacción a rol_nucleo; SET LOCAL la revierte al confirmar o revertir.
const sentenciaCambioDeRol = "SET LOCAL ROLE " + RolNucleo

// sentenciaSearchPath pone el esquema del ledger primero para que el SQL nombre las tablas sin calificar.
const sentenciaSearchPath = "SET LOCAL search_path = nucleo"

// esperaDeBloqueo es el tope global de espera por un candado en cada transacción del núcleo (capítulo 12).
const esperaDeBloqueo = 5 * time.Second

// sentenciaEsperaDeBloqueo fija el lock_timeout de la transacción a partir de esperaDeBloqueo.
var sentenciaEsperaDeBloqueo = "SET LOCAL lock_timeout = '" + esperaDeBloqueo.String() + "'"

// transaccionDeNucleo es lo mínimo que el helper necesita de una transacción abierta; la satisface pgx.Tx.
type transaccionDeNucleo interface {
	repositorio.DBTX
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// abridorDeTransacciones es lo que el helper necesita del pool; la satisface pgxpool.Pool por poolDeNucleo.
type abridorDeTransacciones interface {
	Begin(ctx context.Context) (transaccionDeNucleo, error)
}

// poolDeNucleo adapta *pgxpool.Pool a abridorDeTransacciones.
type poolDeNucleo struct {
	pool *pgxpool.Pool
}

// Begin delega en el pool y devuelve la transacción por la interfaz estrecha del núcleo.
func (p poolDeNucleo) Begin(ctx context.Context) (transaccionDeNucleo, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

var _ abridorDeTransacciones = poolDeNucleo{}

// enTransaccion es el único camino del núcleo hacia la base: fija rol, search_path y lock_timeout, y entrega el Querier ligado a la transacción.
func enTransaccion[T any](
	ctx context.Context,
	pool abridorDeTransacciones,
	fn func(ctx context.Context, tx transaccionDeNucleo, q *repositorio.Queries) (T, error),
) (T, error) {
	var cero T

	tx, err := pool.Begin(ctx)
	if err != nil {
		return cero, fmt.Errorf("nucleo: abrir transacción: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, sentenciaCambioDeRol); err != nil {
		return cero, fmt.Errorf("nucleo: %q: %w", sentenciaCambioDeRol, err)
	}
	// Dos Exec, no uno: el protocolo extendido de pgx rechaza varias sentencias en un mismo comando.
	if _, err := tx.Exec(ctx, sentenciaSearchPath); err != nil {
		return cero, fmt.Errorf("nucleo: %q: %w", sentenciaSearchPath, err)
	}
	if _, err := tx.Exec(ctx, sentenciaEsperaDeBloqueo); err != nil {
		return cero, fmt.Errorf("nucleo: %q: %w", sentenciaEsperaDeBloqueo, err)
	}

	out, err := fn(ctx, tx, repositorio.New(tx))
	if err != nil {
		return cero, err
	}

	if err := tx.Commit(ctx); err != nil {
		return cero, fmt.Errorf("nucleo: confirmar transacción: %w", err)
	}
	return out, nil
}

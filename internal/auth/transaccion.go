package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RolAuth es el rol de Postgres con el que auth ejecuta toda consulta, no el rol de login del pool.
const RolAuth = "rol_auth"

// sentenciaCambioDeRol baja la transacción a rol_auth; SET LOCAL la revierte al confirmar o revertir.
const sentenciaCambioDeRol = "SET LOCAL ROLE " + RolAuth

// esperaDeBloqueo es el tope global de espera por un candado en cada transacción de auth.
const esperaDeBloqueo = 5 * time.Second

// sentenciaEsperaDeBloqueo fija el lock_timeout de la transacción a partir de esperaDeBloqueo.
var sentenciaEsperaDeBloqueo = "SET LOCAL lock_timeout = '" + esperaDeBloqueo.String() + "'"

// transaccionDeAuth es lo mínimo que el helper necesita de una transacción abierta; la satisface pgx.Tx.
type transaccionDeAuth interface {
	repositorio.DBTX
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// abridorDeTransaccionesAuth es lo que el helper necesita del pool; la satisface pgxpool.Pool por poolDeAuth.
type abridorDeTransaccionesAuth interface {
	Begin(ctx context.Context) (transaccionDeAuth, error)
}

// poolDeAuth adapta *pgxpool.Pool a abridorDeTransaccionesAuth.
type poolDeAuth struct {
	pool *pgxpool.Pool
}

// Begin delega en el pool y devuelve la transacción por la interfaz estrecha de auth.
func (p poolDeAuth) Begin(ctx context.Context) (transaccionDeAuth, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

var _ abridorDeTransaccionesAuth = poolDeAuth{}

// enTransaccion es el único camino de auth hacia la base: fija rol y lock_timeout, y entrega el Querier ligado a la transacción.
func enTransaccion[T any](
	ctx context.Context,
	pool abridorDeTransaccionesAuth,
	fn func(ctx context.Context, tx transaccionDeAuth, q *repositorio.Queries) (T, error),
) (T, error) {
	var cero T

	tx, err := pool.Begin(ctx)
	if err != nil {
		return cero, fmt.Errorf("auth: abrir transacción: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, sentenciaCambioDeRol); err != nil {
		return cero, fmt.Errorf("auth: %q: %w", sentenciaCambioDeRol, err)
	}
	if _, err := tx.Exec(ctx, sentenciaEsperaDeBloqueo); err != nil {
		return cero, fmt.Errorf("auth: %q: %w", sentenciaEsperaDeBloqueo, err)
	}

	out, err := fn(ctx, tx, repositorio.New(tx))
	if err != nil {
		return cero, err
	}

	if err := tx.Commit(ctx); err != nil {
		return cero, fmt.Errorf("auth: confirmar transacción: %w", err)
	}
	return out, nil
}

// EnTransaccion expone el helper a los subpaquetes de auth que no ven enTransaccion, sin repetir el SET LOCAL ROLE.
func EnTransaccion[T any](
	ctx context.Context,
	pool *pgxpool.Pool,
	fn func(ctx context.Context, q *repositorio.Queries) (T, error),
) (T, error) {
	return enTransaccion(ctx, poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (T, error) {
			return fn(ctx, q)
		})
}

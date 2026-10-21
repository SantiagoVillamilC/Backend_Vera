package inteligencia

import (
	"context"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RolInteligencia es el rol de Postgres con el que el contexto ejecuta toda consulta.
const RolInteligencia = "rol_inteligencia"

const sentenciaCambioDeRol = "SET LOCAL ROLE " + RolInteligencia

const sentenciaSearchPath = "SET LOCAL search_path = inteligencia"

const esperaDeBloqueo = 5 * time.Second

var sentenciaEsperaDeBloqueo = "SET LOCAL lock_timeout = '" + esperaDeBloqueo.String() + "'"

type transaccionDeInteligencia interface {
	repositorio.DBTX
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

type abridorDeTransacciones interface {
	Begin(ctx context.Context) (transaccionDeInteligencia, error)
}

type poolDeInteligencia struct {
	pool *pgxpool.Pool
}

func (p poolDeInteligencia) Begin(ctx context.Context) (transaccionDeInteligencia, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

var _ abridorDeTransacciones = poolDeInteligencia{}

func enTransaccion[T any](
	ctx context.Context,
	pool abridorDeTransacciones,
	fn func(ctx context.Context, tx transaccionDeInteligencia, q *repositorio.Queries) (T, error),
) (T, error) {
	var cero T

	tx, err := pool.Begin(ctx)
	if err != nil {
		return cero, fmt.Errorf("inteligencia: abrir transacción: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, sentenciaCambioDeRol); err != nil {
		return cero, fmt.Errorf("inteligencia: %q: %w", sentenciaCambioDeRol, err)
	}
	if _, err := tx.Exec(ctx, sentenciaSearchPath); err != nil {
		return cero, fmt.Errorf("inteligencia: %q: %w", sentenciaSearchPath, err)
	}
	if _, err := tx.Exec(ctx, sentenciaEsperaDeBloqueo); err != nil {
		return cero, fmt.Errorf("inteligencia: %q: %w", sentenciaEsperaDeBloqueo, err)
	}

	out, err := fn(ctx, tx, repositorio.New(tx))
	if err != nil {
		return cero, err
	}
	if err := tx.Commit(ctx); err != nil {
		return cero, fmt.Errorf("inteligencia: confirmar transacción: %w", err)
	}
	return out, nil
}

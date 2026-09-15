package nucleo

import (
	"context"
	"fmt"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RolNucleo es el rol de Postgres con el que el núcleo ejecuta toda consulta. No hace login: el
// pool de la aplicación conecta con rol_api, el rol de login no dueño de APP_DATABASE_URL, y cada
// transacción baja a este rol con SET LOCAL ROLE. rol_api necesita la membresía, que otorga la
// migración 0004 con GRANT rol_nucleo TO rol_api.
const RolNucleo = "rol_nucleo"

// sentenciaCambioDeRol es el único lugar del núcleo donde se nombra el cambio de rol. Se ejecuta
// como primera sentencia de cada transacción; SET LOCAL la revierte sola al confirmar o al
// revertir, así que la conexión vuelve a rol_api cuando regresa al pool.
const sentenciaCambioDeRol = "SET LOCAL ROLE " + RolNucleo

// sentenciaSearchPath fija el esquema del ledger como primer lugar de búsqueda, para que el SQL
// del núcleo pueda nombrar `posting` sin calificar como lo hace el diseño. Va inmediatamente
// después del cambio de rol y también es SET LOCAL. No se resuelve con ALTER ROLE ... SET
// search_path: los ajustes por rol se aplican al iniciar sesión, no al SET ROLE.
const sentenciaSearchPath = "SET LOCAL search_path = nucleo"

// transaccionDeNucleo es lo que el helper necesita de una transacción abierta: lo mínimo para
// ejecutar el cambio de rol y las consultas del Querier, y para confirmar o revertir. La
// satisface pgx.Tx; el segundo internal de Go impide que nada de fuera del núcleo la nombre.
type transaccionDeNucleo interface {
	repositorio.DBTX
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// abridorDeTransacciones es lo que el helper necesita del pool: abrir una transacción. Se declara
// en el consumidor y la satisface pgxpool.Pool a través de poolDeNucleo, lo que deja probar el
// helper sin un Postgres real (golang-testing, "Mocking: hand-written fakes first").
type abridorDeTransacciones interface {
	Begin(ctx context.Context) (transaccionDeNucleo, error)
}

// poolDeNucleo adapta *pgxpool.Pool a abridorDeTransacciones. El pool real devuelve pgx.Tx, que
// ya satisface transaccionDeNucleo.
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

// enTransaccion es el único camino del núcleo hacia la base: abre una transacción sobre el único
// pool, ejecuta SET LOCAL ROLE rol_nucleo y SET LOCAL search_path = nucleo antes de cualquier
// consulta y le entrega a fn tanto la transacción como el Querier del núcleo ligado a ella. Toda
// operación del núcleo, incluidas las lecturas de saldo e historial, pasa por acá con una
// transacción corta.
//
// El Servicio no guarda un Querier ligado al pool a propósito: sin él no existe un camino para
// consultar sin haber cambiado de rol, y este helper queda como el único cuello de botella.
//
// El Rollback diferido es deliberado: después de un Commit exitoso devuelve pgx.ErrTxClosed y ese
// es el camino esperado (sqlc-patterns, "Transactions").
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

	out, err := fn(ctx, tx, repositorio.New(tx))
	if err != nil {
		return cero, err
	}

	if err := tx.Commit(ctx); err != nil {
		return cero, fmt.Errorf("nucleo: confirmar transacción: %w", err)
	}
	return out, nil
}

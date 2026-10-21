//go:build integration

package inteligencia

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var errRollbackDePrueba = errors.New("rollback de prueba")

func abrirPoolDePrueba(tb testing.TB) *pgxpool.Pool {
	tb.Helper()

	dsn := os.Getenv("APP_DATABASE_URL")
	if dsn == "" {
		tb.Skip("APP_DATABASE_URL no definida: la prueba de integración necesita un Postgres real")
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		tb.Fatalf("parsear DSN: %v", err)
	}
	cfg.MaxConns = 1
	cfg.MinIdleConns = 0

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		tb.Fatalf("abrir pool: %v", err)
	}
	tb.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		tb.Fatalf("ping: %v", err)
	}
	return pool
}

func TestCategoriasCoincidenConElCatalogoFijo(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	categorias, err := NuevoServicio(pool).Categorias(t.Context())
	if err != nil {
		t.Fatalf("Categorias: %v", err)
	}
	fijo := CatalogoFijo()
	if len(categorias) != len(fijo) {
		t.Fatalf("categorías sembradas = %d, want %d", len(categorias), len(fijo))
	}
	for indice, categoria := range categorias {
		if categoria != fijo[indice] {
			t.Errorf("categoría %d = %+v, want %+v", indice, categoria, fijo[indice])
		}
	}
}

func TestRolDeInteligenciaLeeElLedger(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	_, err := enTransaccion(ctx, poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
			var rol string
			if err := tx.QueryRow(ctx, "SELECT current_user").Scan(&rol); err != nil {
				return struct{}{}, err
			}
			if rol != RolInteligencia {
				t.Errorf("current_user = %q, want %q", rol, RolInteligencia)
			}
			var asientos int64
			if err := tx.QueryRow(ctx, "SELECT count(*) FROM nucleo.journal_entry").Scan(&asientos); err != nil {
				return struct{}{}, err
			}
			return struct{}{}, nil
		})
	if err != nil {
		t.Fatalf("el rol no puede leer el ledger: %v", err)
	}
}

func TestRolDeInteligenciaNoEscribeEnElLedger(t *testing.T) {
	pool := abrirPoolDePrueba(t)

	_, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx,
				"INSERT INTO nucleo.cuenta (id, naturaleza, clase) VALUES ('activo:prueba:inteligencia', 'activo', 'prueba')")
			return struct{}{}, err
		})
	if err == nil {
		t.Fatal("el rol de inteligencia pudo escribir en el ledger")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.InsufficientPrivilege {
		t.Fatalf("error = %v, want privilegio insuficiente", err)
	}
}

func TestRolDeInteligenciaNoEscribeElCatalogo(t *testing.T) {
	pool := abrirPoolDePrueba(t)

	_, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx,
				"INSERT INTO inteligencia.categoria (codigo, nombre, grupo, orden) VALUES ('inventada', 'Inventada', 'gasto', 99)")
			return struct{}{}, err
		})
	if err == nil {
		t.Fatal("el rol de inteligencia pudo escribir el catálogo")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.InsufficientPrivilege {
		t.Fatalf("error = %v, want privilegio insuficiente", err)
	}
}

func TestCursorSoloAvanza(t *testing.T) {
	pool := abrirPoolDePrueba(t)

	_, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) (struct{}, error) {
			antes, err := q.LeerCursor(ctx)
			if err != nil {
				return struct{}{}, err
			}
			if err := q.AvanzarCursor(ctx, antes.UltimoEntryID+5); err != nil {
				return struct{}{}, err
			}
			despues, err := q.LeerCursor(ctx)
			if err != nil {
				return struct{}{}, err
			}
			if despues.UltimoEntryID != antes.UltimoEntryID+5 {
				t.Errorf("cursor = %d, want %d", despues.UltimoEntryID, antes.UltimoEntryID+5)
			}
			if err := q.AvanzarCursor(ctx, despues.UltimoEntryID-3); err != nil {
				return struct{}{}, err
			}
			tercera, err := q.LeerCursor(ctx)
			if err != nil {
				return struct{}{}, err
			}
			if tercera.UltimoEntryID != despues.UltimoEntryID {
				t.Errorf("el cursor retrocedió a %d", tercera.UltimoEntryID)
			}
			return struct{}{}, errRollbackDePrueba
		})
	if !errors.Is(err, errRollbackDePrueba) {
		t.Fatalf("error = %v, want el rollback de prueba", err)
	}
}

//go:build integration

package nucleo

import (
	"context"
	"os"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/jackc/pgx/v5/pgxpool"
)

// abrirPoolDePrueba devuelve un pool contra el Postgres real que indique APP_DATABASE_URL, con
// una sola conexión: así la consulta posterior a la transacción reutiliza la misma conexión y
// demuestra que SET LOCAL ROLE se revirtió al confirmar. Esa es la credencial del pool de la API
// (rol_api, no dueño), así que la prueba corre con el mismo rol base que en producción.
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

func TestEnTransaccionEjecutaComoRolNucleoYRestauraElRolBase(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	var rolBase string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&rolBase); err != nil {
		t.Fatalf("leer el rol base del pool: %v", err)
	}
	if rolBase != "rol_api" {
		t.Fatalf("rol base del pool (APP_DATABASE_URL) = %q, want %q: la prueba exige el rol de login "+
			"de la API, no el dueño del esquema", rolBase, "rol_api")
	}

	var rolDentro, sesionDentro string
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx, "SELECT current_user, session_user").
				Scan(&rolDentro, &sesionDentro)
		}); err != nil {
		t.Fatalf("enTransaccion: %v", err)
	}

	if rolDentro != RolNucleo {
		t.Errorf("current_user dentro del helper = %q, want %q", rolDentro, RolNucleo)
	}
	if sesionDentro != rolBase {
		t.Errorf("session_user dentro del helper = %q, want %q (el rol base del pool)", sesionDentro, rolBase)
	}

	var rolDespues string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&rolDespues); err != nil {
		t.Fatalf("leer el rol después del COMMIT: %v", err)
	}
	if rolDespues != rolBase {
		t.Errorf("current_user después del COMMIT = %q, want %q", rolDespues, rolBase)
	}
}

// El spike de state.md: el costo del SET LOCAL ROLE por transacción contra la misma transacción
// sin el SET. Los dos benchmarks abren y confirman una transacción vacía; la diferencia es el
// único round trip que agrega el cambio de rol. No cambia el diseño: la decisión de voltear a dos
// pools la toma el supervisor con estos números.
func BenchmarkTransaccionConCambioDeRol(b *testing.B) {
	pool := abrirPoolDePrueba(b)
	ctx := context.Background()
	b.ReportAllocs()

	for b.Loop() {
		if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
			func(context.Context, transaccionDeNucleo, *repositorio.Queries) (struct{}, error) {
				return struct{}{}, nil
			}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTransaccionSinCambioDeRol(b *testing.B) {
	pool := abrirPoolDePrueba(b)
	ctx := context.Background()
	b.ReportAllocs()

	for b.Loop() {
		tx, err := pool.Begin(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

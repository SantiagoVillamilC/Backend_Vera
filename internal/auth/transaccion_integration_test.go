//go:build integration

package auth

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// abrirPoolDeAuth abre el pool contra APP_DATABASE_URL, obligatoria bajo el tag integration.
func abrirPoolDeAuth(tb testing.TB) *pgxpool.Pool {
	tb.Helper()

	dsn := os.Getenv("APP_DATABASE_URL")
	if dsn == "" {
		tb.Fatalf("APP_DATABASE_URL es obligatoria bajo el tag integration: sin Postgres real el test es un hallazgo, no un skip")
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

func TestEnTransaccionEjecutaComoRolAuthYRestauraElRolBase(t *testing.T) {
	pool := abrirPoolDeAuth(t)
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
	if _, err := enTransaccion(ctx, poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx, "SELECT current_user, session_user").
				Scan(&rolDentro, &sesionDentro)
		}); err != nil {
		t.Fatalf("enTransaccion: %v", err)
	}

	if rolDentro != RolAuth {
		t.Errorf("current_user dentro del helper = %q, want %q", rolDentro, RolAuth)
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

// TestRolAuthNoEsDuenoDelEsquema comprueba que rol_auth no puede crear tablas en auth.
func TestRolAuthNoEsDuenoDelEsquema(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	if _, err := tx.Exec(ctx, sentenciaCambioDeRol); err != nil {
		t.Fatalf("SET LOCAL ROLE %s: %v", RolAuth, err)
	}

	var rolDentro string
	if err := tx.QueryRow(ctx, "SELECT current_user").Scan(&rolDentro); err != nil {
		t.Fatalf("leer current_user tras el SET LOCAL ROLE: %v", err)
	}
	if rolDentro != RolAuth {
		t.Fatalf("current_user tras el SET LOCAL ROLE = %q, want %q", rolDentro, RolAuth)
	}

	_, errCrear := tx.Exec(ctx, "CREATE TABLE auth.prueba (id int)")
	if errCrear == nil {
		t.Fatal("CREATE TABLE auth.prueba como rol_auth no falló")
	}
	t.Logf("error literal de Postgres: %v", errCrear)

	var pgErr *pgconn.PgError
	if !errors.As(errCrear, &pgErr) {
		t.Fatalf("el error de CREATE TABLE no es *pgconn.PgError: %v", errCrear)
	}
	if pgErr.Code != pgerrcode.InsufficientPrivilege {
		t.Errorf("SQLSTATE = %q, want %q", pgErr.Code, pgerrcode.InsufficientPrivilege)
	}
	if !strings.Contains(pgErr.Message, "permission denied for schema auth") {
		t.Errorf("mensaje = %q, want que contenga %q", pgErr.Message, "permission denied for schema auth")
	}
}

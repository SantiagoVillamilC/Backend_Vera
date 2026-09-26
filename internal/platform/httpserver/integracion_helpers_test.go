//go:build integration

package httpserver

import (
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// abrirPoolDeIntegracion abre el pool contra APP_DATABASE_URL y lo cierra al terminar la prueba.
func abrirPoolDeIntegracion(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("APP_DATABASE_URL")
	if dsn == "" {
		t.Skip("APP_DATABASE_URL no definida: la prueba de integración necesita un Postgres real")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("abrir pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return pool
}

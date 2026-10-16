//go:build integration

package main

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/bd"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Crea una base propia para no revertir tablas que otras pruebas estén usando.
func TestMigracionAsistenteReversible(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Fatal("DATABASE_URL obligatoria, usar una base desechable")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	nombre := "asistente_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identificador := pgx.Identifier{nombre}.Sanitize()
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+identificador); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP DATABASE "+identificador+" WITH (FORCE)"); err != nil {
			t.Errorf("limpiar base de prueba: %v", err)
		}
	})
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	// Registrar la configuración evita reconstruir y escapar manualmente el DSN.
	cfg.Database = nombre
	conexion := stdlib.RegisterConnConfig(cfg)
	t.Cleanup(func() { stdlib.UnregisterConnConfig(conexion) })
	db, err := sql.Open("pgx", conexion)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	goose.SetBaseFS(bd.Migraciones)
	if err := goose.RunContext(t.Context(), "up", db, "migraciones"); err != nil {
		t.Fatal(err)
	}
	if err := correrAuth(t.Context(), db, "up"); err != nil {
		t.Fatal(err)
	}
	for _, comando := range []string{"up", "up", "status", "down", "up"} {
		if err := correrAsistente(t.Context(), db, comando); err != nil {
			t.Fatalf("%s: %v", comando, err)
		}
	}
	var tablas int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM information_schema.tables WHERE table_schema='asistente'").Scan(&tablas); err != nil {
		t.Fatal(err)
	}
	if tablas != 2 {
		t.Fatalf("tablas=%d, esperadas 2", tablas)
	}
	var existeAuth bool
	if err := db.QueryRowContext(t.Context(), "SELECT to_regclass('auth.partes') IS NOT NULL").Scan(&existeAuth); err != nil || !existeAuth {
		t.Fatalf("auth afectado: %v", err)
	}
}

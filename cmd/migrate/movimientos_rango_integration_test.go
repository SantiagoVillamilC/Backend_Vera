//go:build integration

package main

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/bd"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestMigracionIndiceMovimientosConDatos(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Fatal("DATABASE_URL obligatoria")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	nombre := "rango_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identificador := pgx.Identifier{nombre}.Sanitize()
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+identificador); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP DATABASE "+identificador+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = nombre
	registrado := stdlib.RegisterConnConfig(cfg)
	t.Cleanup(func() { stdlib.UnregisterConnConfig(registrado) })
	db, err := sql.Open("pgx", registrado)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	goose.SetBaseFS(bd.Migraciones)
	goose.SetTableName(goose.DefaultTablename)
	if err := goose.UpToContext(t.Context(), db, "migraciones", 20); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var entry int64
	fecha := time.Date(2026, 10, 1, 5, 0, 0, 0, time.UTC)
	if err := tx.QueryRowContext(t.Context(), "INSERT INTO nucleo.journal_entry(tipo,ocurrido_en) VALUES ('prueba',$1) RETURNING id", fecha).Scan(&entry); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), "INSERT INTO nucleo.posting(entry_id,cuenta_id,monto,moneda) VALUES ($1,'activo:custodia:banco',100,'COP'),($1,'pasivo:gmf_por_pagar',-100,'COP')", entry); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, comando := range []string{"up", "down", "up"} {
		if err := goose.RunContext(t.Context(), comando, db, "migraciones"); err != nil {
			t.Fatal(err)
		}
		if comando == "down" {
			continue
		}
		var cantidad int
		var monto int64
		var ocurrida time.Time
		if err := db.QueryRowContext(t.Context(), "SELECT count(*),sum(monto),min(ocurrido_en) FROM nucleo.posting").Scan(&cantidad, &monto, &ocurrida); err != nil {
			t.Fatal(err)
		}
		if cantidad != 2 || monto != 0 || !ocurrida.Equal(fecha) {
			t.Fatal("el backfill alteró el ledger o la fecha")
		}
		var indice bool
		if err := db.QueryRowContext(t.Context(), "SELECT to_regclass('nucleo.posting_cuenta_fecha_idx') IS NOT NULL").Scan(&indice); err != nil || !indice {
			t.Fatalf("índice: %v %v", indice, err)
		}
	}
}

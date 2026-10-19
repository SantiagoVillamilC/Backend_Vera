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
	goose.SetBaseFS(bd.MigracionesAsistente)
	goose.SetTableName("goose_db_version_asistente")
	if err := goose.UpToContext(t.Context(), db, "migraciones-asistente", 3); err != nil {
		t.Fatal(err)
	}
	parteLegacy := uuid.New()
	if _, err := db.ExecContext(t.Context(), "INSERT INTO auth.partes(id,tipo) VALUES($1,'persona')", parteLegacy); err != nil {
		t.Fatal(err)
	}
	for _, resuelta := range []bool{false, true} {
		conv, accion := uuid.New(), uuid.New()
		if _, err := db.ExecContext(t.Context(), "INSERT INTO asistente.conversacion(id,usuario_id) VALUES($1,$2)", conv, parteLegacy); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(t.Context(), "INSERT INTO asistente.accion_pendiente(id,usuario_id,conversacion_id,tipo,destino,nombre_mostrado,monto,requiere_codigo,creada_en,expira_en) VALUES($1,$2,$3,'transferencia_interna','destino','L*** R***',100,false,now()-interval '10 minutes',now()-interval '5 minutes')", accion, parteLegacy, conv); err != nil {
			t.Fatal(err)
		}
		if resuelta {
			if _, err := db.ExecContext(t.Context(), "UPDATE asistente.accion_pendiente SET estado='cancelada',resuelta_en=clock_timestamp() WHERE id=$1", accion); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, comando := range []string{"up", "up", "status", "down", "up"} {
		if err := correrAsistente(t.Context(), db, comando); err != nil {
			t.Fatalf("%s: %v", comando, err)
		}
	}
	var conservadas int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM asistente.accion_pendiente WHERE usuario_id=$1 AND usuario_financiero_id=$1::uuid::text AND monto=100 AND estado IN ('pendiente','cancelada')", parteLegacy).Scan(&conservadas); err != nil || conservadas != 2 {
		t.Fatalf("migración perdió acciones antiguas: %d %v", conservadas, err)
	}
	var tablas int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM information_schema.tables WHERE table_schema='asistente'").Scan(&tablas); err != nil {
		t.Fatal(err)
	}
	if tablas != 5 {
		t.Fatalf("tablas=%d, esperadas 5", tablas)
	}
	var existeAuth bool
	if err := db.QueryRowContext(t.Context(), "SELECT to_regclass('auth.partes') IS NOT NULL").Scan(&existeAuth); err != nil || !existeAuth {
		t.Fatalf("auth afectado: %v", err)
	}
}

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/SantiagoVillamilC/Backend_Centinela/bd"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("migrate: %v", err)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return errors.New("uso: migrate <up|down|status|version>")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL es obligatoria")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("abrir conexión: %w", err)
	}
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(bd.Migraciones)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("dialecto postgres: %w", err)
	}

	ctx := context.Background()
	comando := os.Args[1]
	// River tiene su propio migrador y su propio esquema, y este binario impone el orden: arriba,
	// goose primero (crea el esquema `river`) y River después; abajo, River primero (vacía el
	// esquema) y goose después (lo borra). Correr goose o el CLI de River por separado
	// desincroniza las dos tablas de versión.
	switch comando {
	case "up":
		if err := goose.RunContext(ctx, "up", db, "migraciones"); err != nil {
			return fmt.Errorf("goose up: %w", err)
		}
		return subirRiver(ctx, dsn)
	case "down":
		if err := bajarRiver(ctx, dsn); err != nil {
			return err
		}
		if err := goose.RunContext(ctx, "down", db, "migraciones"); err != nil {
			return fmt.Errorf("goose down: %w", err)
		}
		return nil
	case "status":
		if err := goose.RunContext(ctx, "status", db, "migraciones"); err != nil {
			return fmt.Errorf("goose status: %w", err)
		}
		return estadoRiver(ctx, dsn)
	default:
		if err := goose.RunContext(ctx, comando, db, "migraciones"); err != nil {
			return fmt.Errorf("goose %s: %w", comando, err)
		}
		return nil
	}
}

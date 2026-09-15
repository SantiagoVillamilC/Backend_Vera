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
	if err := goose.RunContext(context.Background(), os.Args[1], db, "migraciones"); err != nil {
		return fmt.Errorf("goose %s: %w", os.Args[1], err)
	}
	return nil
}

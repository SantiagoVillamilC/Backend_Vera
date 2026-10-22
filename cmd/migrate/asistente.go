package main

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/SantiagoVillamilC/Backend_Centinela/bd"
	"github.com/pressly/goose/v3"
)

func correrAsistente(ctx context.Context, db *sql.DB, comando string) error {
	goose.SetBaseFS(bd.MigracionesAsistente)
	goose.SetTableName("goose_db_version_asistente")
	defer func() {
		goose.SetBaseFS(bd.Migraciones)
		goose.SetTableName(goose.DefaultTablename)
	}()
	if err := goose.RunContext(ctx, comando, db, "migraciones-asistente"); err != nil {
		return fmt.Errorf("goose asistente %s: %w", comando, err)
	}
	return nil
}

package main

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/SantiagoVillamilC/Backend_Centinela/bd"
	"github.com/pressly/goose/v3"
)

// tablaVersionAuth es la tabla de versión propia de auth, separada de goose_db_version del núcleo.
const tablaVersionAuth = "goose_db_version_auth"

// dirAuth es el directorio, dentro del FS embebido de auth, con sus migraciones.
const dirAuth = "migraciones-auth"

// correrAuth ejecuta un comando de goose sobre el FS y la tabla de versión de auth, y restaura los globales.
func correrAuth(ctx context.Context, db *sql.DB, comando string) error {
	goose.SetBaseFS(bd.MigracionesAuth)
	goose.SetTableName(tablaVersionAuth)
	defer func() {
		goose.SetBaseFS(bd.Migraciones)
		goose.SetTableName(goose.DefaultTablename)
	}()

	if err := goose.RunContext(ctx, comando, db, dirAuth); err != nil {
		return fmt.Errorf("goose auth %s: %w", comando, err)
	}
	return nil
}

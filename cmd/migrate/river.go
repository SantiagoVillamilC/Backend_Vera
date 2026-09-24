package main

import (
	"context"
	"fmt"
	"log"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/database"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/jobs"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// concesionesRiver es lo que el cliente de River necesita del rol de login de la API sobre su
// esquema. Corre después de aplicar las migraciones de River, cuando las tablas ya existen, y se
// repite en cada `up` para cubrir las tablas que agregue una versión nueva.
const concesionesRiver = `
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA ` + jobs.EsquemaRiver + ` TO rol_api;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA ` + jobs.EsquemaRiver + ` TO rol_api;
`

func subirRiver(ctx context.Context, dsn string) error {
	return migrarRiver(ctx, dsn, rivermigrate.DirectionUp)
}

func bajarRiver(ctx context.Context, dsn string) error {
	return migrarRiver(ctx, dsn, rivermigrate.DirectionDown)
}

func migrarRiver(ctx context.Context, dsn string, direccion rivermigrate.Direction) error {
	pool, err := database.NewPool(ctx, dsn)
	if err != nil {
		return fmt.Errorf("river: abrir pool: %w", err)
	}
	defer pool.Close()

	migrador, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{Schema: jobs.EsquemaRiver})
	if err != nil {
		return fmt.Errorf("river: migrador: %w", err)
	}

	opciones := &rivermigrate.MigrateOpts{}
	if direccion == rivermigrate.DirectionDown {
		opciones.TargetVersion = -1
	}
	resultado, err := migrador.Migrate(ctx, direccion, opciones)
	if err != nil {
		return fmt.Errorf("river %s: %w", direccion, err)
	}
	for _, version := range resultado.Versions {
		log.Printf("river %s: %d %s", direccion, version.Version, version.Name)
	}

	if direccion == rivermigrate.DirectionUp {
		if _, err := pool.Exec(ctx, concesionesRiver); err != nil {
			return fmt.Errorf("river: concesiones a rol_api: %w", err)
		}
	}
	return nil
}

func estadoRiver(ctx context.Context, dsn string) error {
	pool, err := database.NewPool(ctx, dsn)
	if err != nil {
		return fmt.Errorf("river: abrir pool: %w", err)
	}
	defer pool.Close()

	migrador, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{Schema: jobs.EsquemaRiver})
	if err != nil {
		return fmt.Errorf("river: migrador: %w", err)
	}
	versiones, err := migrador.ExistingVersions(ctx)
	if err != nil {
		return fmt.Errorf("river: versiones aplicadas: %w", err)
	}
	log.Printf("river: %d migraciones aplicadas en el esquema %q", len(versiones), jobs.EsquemaRiver) //nolint:gosec // solo se loguean un contador y una constante, no hay entrada externa
	return nil
}

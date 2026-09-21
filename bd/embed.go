package bd

import "embed"

//go:embed migraciones/*.sql
var Migraciones embed.FS

// MigracionesAuth es el FS embebido con las migraciones del módulo de auth.
//
//go:embed migraciones-auth/*.sql
var MigracionesAuth embed.FS

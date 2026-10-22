package bd

import "embed"

//go:embed migraciones/*.sql
var Migraciones embed.FS

// MigracionesAuth es el FS embebido con las migraciones del módulo de auth.
//
//go:embed migraciones-auth/*.sql
var MigracionesAuth embed.FS

// MigracionesAsistente contiene la persistencia de Vera, aplicada después de auth.
//
//go:embed migraciones-asistente/*.sql
var MigracionesAsistente embed.FS

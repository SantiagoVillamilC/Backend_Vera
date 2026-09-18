package bd

import "embed"

//go:embed migraciones/*.sql
var Migraciones embed.FS

//go:embed migraciones-auth/*.sql
var MigracionesAuth embed.FS

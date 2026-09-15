package bd

import "embed"

// Migraciones contiene los archivos SQL de Goose embebidos en el binario de migración, para que el
// paso dedicado de migración no dependa de que el sistema de archivos esté presente en la imagen.
//
//go:embed migraciones/*.sql
var Migraciones embed.FS

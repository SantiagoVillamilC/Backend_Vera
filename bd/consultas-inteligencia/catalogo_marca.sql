-- name: BuscarCatalogoPorNombre :one
SELECT nombre_normalizado, categoria, llave, origen, version, creada_en
  FROM inteligencia.catalogo_marca
 WHERE nombre_normalizado = sqlc.arg('nombre_normalizado')::text;

-- name: BuscarCatalogoPorLlave :one
SELECT nombre_normalizado, categoria, llave, origen, version, creada_en
  FROM inteligencia.catalogo_marca
 WHERE llave = sqlc.arg('llave')::text;

-- name: ListarCatalogoMarca :many
SELECT nombre_normalizado, categoria, llave
  FROM inteligencia.catalogo_marca
 ORDER BY nombre_normalizado;

-- name: UpsertCatalogoMarca :one
INSERT INTO inteligencia.catalogo_marca (nombre_normalizado, categoria, llave, origen, version)
VALUES (
    sqlc.arg('nombre_normalizado'), sqlc.arg('categoria'), sqlc.arg('llave'),
    sqlc.arg('origen'), sqlc.arg('version')
)
ON CONFLICT (nombre_normalizado) DO UPDATE SET
    categoria = EXCLUDED.categoria,
    llave = EXCLUDED.llave,
    origen = EXCLUDED.origen,
    version = EXCLUDED.version
RETURNING (xmax = 0) AS insertada;

-- name: BorrarCatalogoMarcaFueraDe :execrows
DELETE FROM inteligencia.catalogo_marca
 WHERE origen = sqlc.arg('origen')::text
   AND NOT (nombre_normalizado = ANY(sqlc.arg('nombres')::text[]));

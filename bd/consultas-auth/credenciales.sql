-- name: CrearCredencial :one
INSERT INTO auth.credenciales (id, parte_id, correo, celular, valor_derivado, estado)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: CredencialPorID :one
SELECT * FROM auth.credenciales
WHERE id = $1;

-- name: CredencialPorCorreo :one
SELECT * FROM auth.credenciales
WHERE correo = $1 AND estado <> 'cerrado';

-- name: CredencialPorCelular :one
SELECT * FROM auth.credenciales
WHERE celular = $1 AND estado <> 'cerrado';

-- name: RehashCredencial :execrows
UPDATE auth.credenciales
SET valor_derivado = $1, actualizada_en = now()
WHERE id = $2 AND valor_derivado = $3;

-- name: ActualizarCelularCredencial :execrows
UPDATE auth.credenciales
SET celular = sqlc.arg('celular'),
    celular_verificado_en = now(),
    celular_cambiado_en = now(),
    actualizada_en = now()
WHERE id = sqlc.arg('id') AND estado = 'activo' AND celular <> sqlc.arg('celular');

-- name: CredencialPorCelularNoCerrada :one
SELECT id
FROM auth.credenciales
WHERE celular = sqlc.arg('celular') AND estado <> 'cerrado' AND id <> sqlc.arg('id');

-- name: CredencialVivaPorDocumento :one
SELECT c.id, c.parte_id, c.correo, c.celular_verificado_en
FROM auth.credenciales c
JOIN auth.personas p ON p.parte_id = c.parte_id
WHERE p.tipo_documento = sqlc.arg('tipo_documento')
  AND p.documento = sqlc.arg('documento')
  AND c.estado <> 'cerrado';

-- name: InsertarCredencialDePrueba :exec
INSERT INTO auth.credenciales (id, parte_id, correo, celular, valor_derivado, estado)
VALUES ($1, $2, $3, $4, NULL, 'sin_login')
ON CONFLICT DO NOTHING;

-- name: ResolverDestinoPorCelular :one
SELECT c.parte_id
FROM auth.credenciales c
WHERE c.celular = $1
  AND c.celular_verificado_en IS NOT NULL
  AND c.estado = 'activo'
  AND (c.celular_cambiado_en IS NULL
       OR c.celular_cambiado_en <= now() - (sqlc.arg('enfriamiento_segundos')::double precision * interval '1 second'));

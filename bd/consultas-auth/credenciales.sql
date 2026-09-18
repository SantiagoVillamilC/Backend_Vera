-- name: CrearCredencial :one
INSERT INTO auth.credenciales (id, parte_id, correo, celular, valor_derivado, estado)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: CredencialPorID :one
SELECT * FROM auth.credenciales
WHERE id = $1;

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

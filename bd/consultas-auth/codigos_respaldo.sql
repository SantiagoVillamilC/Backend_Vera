-- name: CrearCodigoRespaldo :exec
INSERT INTO auth.codigos_respaldo (id, credencial_id, codigo_hmac)
VALUES ($1, $2, $3);

-- name: CodigosRespaldoPendientesDeCredencial :many
SELECT id, codigo_hmac
FROM auth.codigos_respaldo
WHERE credencial_id = $1 AND usado_en IS NULL;

-- name: ConsumirCodigoRespaldo :one
UPDATE auth.codigos_respaldo
SET usado_en = now()
WHERE id = $1 AND usado_en IS NULL
RETURNING id;

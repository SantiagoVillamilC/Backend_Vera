-- name: InsertarDesafio :one
INSERT INTO auth.desafios (
    id, parte_id, credencial_id, proposito, celular_destino, codigo_hmac,
    estado, creado_en, expira_en, ultimo_envio_en, contexto_solicitado
) VALUES (
    sqlc.arg('id'),
    sqlc.narg('parte_id'),
    sqlc.narg('credencial_id'),
    sqlc.arg('proposito'),
    sqlc.arg('celular_destino'),
    sqlc.arg('codigo_hmac'),
    'pendiente',
    sqlc.arg('creado_en'),
    sqlc.arg('expira_en'),
    sqlc.arg('ultimo_envio_en'),
    sqlc.narg('contexto_solicitado')
)
RETURNING *;

-- name: InvalidarDesafio :execrows
UPDATE auth.desafios
SET estado = 'invalidado'
WHERE id = $1 AND estado = 'pendiente';

-- name: DesafioPendientePorID :one
SELECT id, parte_id, credencial_id, proposito, celular_destino, codigo_hmac,
       intentos, reenvios, estado, creado_en, expira_en, ultimo_envio_en,
       contexto_solicitado, expira_en <= now() AS expirado
FROM auth.desafios
WHERE id = $1 AND estado = 'pendiente';

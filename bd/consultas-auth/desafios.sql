-- name: InsertarDesafio :one
INSERT INTO auth.desafios (
    id, parte_id, credencial_id, proposito, celular_destino, codigo_hmac,
    estado, creado_en, expira_en, ultimo_envio_en, contexto_solicitado,
    celular_nuevo, desafio_padre_id
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
    sqlc.narg('contexto_solicitado'),
    sqlc.narg('celular_nuevo'),
    sqlc.narg('desafio_padre_id')
)
RETURNING *;

-- name: InvalidarDesafio :execrows
UPDATE auth.desafios
SET estado = 'invalidado'
WHERE id = $1 AND estado = 'pendiente';

-- name: InvalidarDesafioNoPendiente :execrows
UPDATE auth.desafios
SET estado = 'invalidado'
WHERE id = $1 AND estado <> 'invalidado';

-- name: DesafioPendientePorID :one
SELECT id, parte_id, credencial_id, proposito, celular_destino, codigo_hmac,
       intentos, reenvios, estado, creado_en, expira_en, ultimo_envio_en,
       contexto_solicitado, celular_nuevo, desafio_padre_id, expira_en <= now() AS expirado
FROM auth.desafios
WHERE id = $1 AND estado = 'pendiente';

-- name: IncrementarIntentoDesafio :one
UPDATE auth.desafios
SET intentos = intentos + 1
WHERE id = $1 AND estado = 'pendiente' AND intentos < 3
RETURNING id, parte_id, credencial_id, proposito, celular_destino, codigo_hmac,
          intentos, reenvios, estado, creado_en, expira_en, ultimo_envio_en,
          contexto_solicitado, celular_nuevo, desafio_padre_id, expira_en <= now() AS expirado;

-- name: DesafioPorID :one
SELECT id, parte_id, credencial_id, proposito, celular_destino, codigo_hmac,
       intentos, reenvios, estado, creado_en, expira_en, ultimo_envio_en,
       contexto_solicitado, celular_nuevo, desafio_padre_id, expira_en <= now() AS expirado
FROM auth.desafios
WHERE id = $1;

-- name: DesafioHijoPendiente :one
SELECT id, parte_id, credencial_id, proposito, celular_destino, codigo_hmac,
       intentos, reenvios, estado, creado_en, expira_en, ultimo_envio_en,
       contexto_solicitado, celular_nuevo, desafio_padre_id, expira_en <= now() AS expirado
FROM auth.desafios
WHERE desafio_padre_id = $1 AND estado = 'pendiente'
ORDER BY creado_en DESC
LIMIT 1;

-- name: MarcarDesafioVerificado :execrows
UPDATE auth.desafios
SET estado = 'verificado'
WHERE id = $1 AND estado = 'pendiente';

-- name: MarcarDesafioExpirado :execrows
UPDATE auth.desafios
SET estado = 'expirado'
WHERE id = $1 AND estado = 'pendiente';

-- name: ActualizarReenvioDesafio :one
UPDATE auth.desafios
SET codigo_hmac = sqlc.arg('codigo_hmac'),
    reenvios = reenvios + 1,
    ultimo_envio_en = sqlc.arg('ahora'),
    expira_en = sqlc.arg('expira_en')
WHERE id = sqlc.arg('id')
  AND estado = 'pendiente'
  AND reenvios < 3
  AND ultimo_envio_en <= sqlc.arg('umbral')
  AND expira_en > sqlc.arg('ahora')
RETURNING *;

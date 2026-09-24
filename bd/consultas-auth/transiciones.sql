-- name: TransicionarCredencial :execrows
UPDATE auth.credenciales
SET estado = sqlc.arg('nuevo'),
    actualizada_en = now()
WHERE id = sqlc.arg('credencial_id') AND estado = sqlc.arg('esperado');

-- name: ActivarCredencial :execrows
UPDATE auth.credenciales
SET estado = 'activo',
    actualizada_en = now(),
    celular_verificado_en = now()
WHERE id = sqlc.arg('credencial_id') AND estado = 'pendiente_verificacion';

-- name: RegistrarTransicionCredencial :exec
INSERT INTO auth.transiciones_credencial (credencial_id, estado_anterior, estado_nuevo, motivo)
VALUES (sqlc.arg('credencial_id'), sqlc.arg('estado_anterior'), sqlc.arg('estado_nuevo'), sqlc.arg('motivo'));

-- name: PendientesVencidasParaCierre :many
SELECT id
FROM auth.credenciales
WHERE estado = 'pendiente_verificacion'
  AND creada_en < now() - (sqlc.arg('ventana_segundos')::double precision * interval '1 second')
ORDER BY creada_en
LIMIT sqlc.arg('limite')
FOR UPDATE SKIP LOCKED;

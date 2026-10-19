-- name: CrearAccion :one
INSERT INTO asistente.accion_pendiente (
    usuario_id, conversacion_id, tipo, destino, tipo_llave, nombre_mostrado,
    celular_enmascarado, entidad_destino, monto, requiere_codigo, usuario_financiero_id
)
SELECT c.usuario_id, c.id, @tipo, @destino, sqlc.narg('tipo_llave'), @nombre_mostrado,
    sqlc.narg('celular_enmascarado'), sqlc.narg('entidad_destino'), @monto, @requiere_codigo,
    COALESCE(sqlc.narg('usuario_financiero_id')::text, c.usuario_id::text)
FROM asistente.conversacion c
WHERE c.id = @conversacion_id AND c.usuario_id = @usuario_id AND c.estado = 'activo'
RETURNING *;

-- name: ObtenerAccion :one
SELECT * FROM asistente.accion_pendiente WHERE id = @id AND usuario_id = @usuario_id;

-- Mantener este bloqueo hasta terminar la confirmación, usando siempre el mismo tx.
-- name: BloquearAccion :one
SELECT * FROM asistente.accion_pendiente WHERE id = @id AND usuario_id = @usuario_id FOR UPDATE;

-- Una verificación local no disponible solo puede elevar la exigencia, nunca rebajarla.
-- name: VincularDesafioAccion :one
UPDATE asistente.accion_pendiente SET desafio_id = @desafio_id, requiere_codigo = true
WHERE id = @id AND usuario_id = @usuario_id AND estado = 'pendiente' AND expira_en > clock_timestamp()
RETURNING *;

-- CAS: cero filas significa que el llamador debe releer el estado, no ejecutar de nuevo.
-- El núcleo es quien genera transferencia_id; esta consulta no ejecuta ni autoriza dinero.
-- name: ConfirmarAccion :one
UPDATE asistente.accion_pendiente
SET estado = 'confirmada', transferencia_id = @transferencia_id, resuelta_en = clock_timestamp()
WHERE id = @id AND usuario_id = @usuario_id AND estado = 'pendiente' AND expira_en > clock_timestamp()
RETURNING *;

-- name: FallarAccion :one
UPDATE asistente.accion_pendiente
SET estado = 'fallida', codigo_error = @codigo_error, resuelta_en = clock_timestamp()
WHERE id = @id AND usuario_id = @usuario_id AND estado = 'pendiente' AND expira_en > clock_timestamp()
RETURNING *;

-- name: CancelarAccion :one
UPDATE asistente.accion_pendiente
SET estado = 'cancelada', resuelta_en = clock_timestamp()
WHERE id = @id AND usuario_id = @usuario_id AND estado = 'pendiente'
RETURNING *;

-- name: VencerAccion :one
UPDATE asistente.accion_pendiente
SET estado = 'vencida', resuelta_en = clock_timestamp()
WHERE id = @id AND usuario_id = @usuario_id AND estado = 'pendiente' AND expira_en <= clock_timestamp()
RETURNING *;

-- Base del barrido periódico de SCRUM-137: lotes pequeños y trabajadores sin bloqueo mutuo.
-- name: VencerAcciones :execrows
WITH lote AS (
    SELECT id FROM asistente.accion_pendiente
    WHERE estado = 'pendiente' AND expira_en <= clock_timestamp()
    ORDER BY expira_en, id LIMIT sqlc.arg('limite')::integer FOR UPDATE SKIP LOCKED
)
UPDATE asistente.accion_pendiente a SET estado = 'vencida', resuelta_en = clock_timestamp()
FROM lote WHERE a.id = lote.id AND a.estado = 'pendiente';

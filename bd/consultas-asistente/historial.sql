-- name: ListarHistorialConversaciones :many
SELECT * FROM asistente.conversacion
WHERE usuario_id = @usuario_id AND usuario_financiero_id = @usuario_financiero_id
AND (NOT @con_cursor::boolean OR (ultimo_mensaje_en, id) < (@actividad::timestamptz, @ancla::uuid))
ORDER BY ultimo_mensaje_en DESC, id DESC LIMIT @limite;

-- name: ConversacionDeHistorial :one
SELECT * FROM asistente.conversacion
WHERE id = @id AND usuario_id = @usuario_id AND usuario_financiero_id = @usuario_financiero_id
FOR SHARE;

-- name: ListarHistorialMensajes :many
SELECT * FROM asistente.mensaje
WHERE conversacion_id = @conversacion_id
AND rol IN ('usuario', 'asistente') AND COALESCE(metadatos->>'interno', 'false') = 'false'
AND (NOT @con_cursor::boolean OR numero_orden < @ancla::integer)
ORDER BY numero_orden DESC LIMIT @limite;

-- name: BorrarConversacionDeHistorial :execrows
DELETE FROM asistente.conversacion
WHERE id = @id AND usuario_id = @usuario_id AND usuario_financiero_id = @usuario_financiero_id;

-- Mismo orden de candados que confirmación y expiración. El DELETE dispara la cancelación.
-- name: BloquearHistorialParaBorrar :many
SELECT id FROM asistente.conversacion
WHERE usuario_id = @usuario_id AND usuario_financiero_id = @usuario_financiero_id
ORDER BY id FOR UPDATE;

-- name: ConversacionesParaRetencion :many
SELECT id FROM asistente.conversacion
WHERE ultimo_mensaje_en < @corte::timestamptz
ORDER BY ultimo_mensaje_en, id LIMIT 100 FOR UPDATE SKIP LOCKED;

-- name: BorrarConversacionPorRetencion :exec
DELETE FROM asistente.conversacion WHERE id = @id;

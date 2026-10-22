-- Serializa el reemplazo de propuestas, también cuando todavía no existe ninguna.
-- name: BloquearConversacionParaAccion :one
SELECT id FROM asistente.conversacion
WHERE id = @conversacion_id AND usuario_id = @usuario_id AND estado = 'activo'
FOR UPDATE;

-- La cancelación y el INSERT siguiente deben compartir la transacción y el bloqueo.
-- name: CancelarPropuestaAnterior :exec
UPDATE asistente.accion_pendiente
SET estado = CASE WHEN expira_en <= clock_timestamp() THEN 'vencida' ELSE 'cancelada' END,
    resuelta_en = clock_timestamp()
WHERE conversacion_id = @conversacion_id AND usuario_id = @usuario_id AND estado = 'pendiente';

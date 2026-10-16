-- name: CrearConversacion :one
INSERT INTO asistente.conversacion (usuario_id, titulo)
VALUES (@usuario_id, sqlc.narg('titulo')) RETURNING *;

-- name: ObtenerConversacion :one
SELECT * FROM asistente.conversacion WHERE id = @id AND usuario_id = @usuario_id;

-- La actualización bloquea la conversación y asigna orden sin MAX()+1 ni carreras.
-- Si el INSERT falla, el contador y la fecha también se revierten.
-- name: AnexarMensaje :one
WITH conversacion AS (
    UPDATE asistente.conversacion
    SET cantidad_mensajes = cantidad_mensajes + 1, ultimo_mensaje_en = clock_timestamp()
    WHERE asistente.conversacion.id = @conversacion_id AND usuario_id = @usuario_id AND estado = 'activo'
    RETURNING asistente.conversacion.id, asistente.conversacion.cantidad_mensajes
)
INSERT INTO asistente.mensaje (
    conversacion_id, numero_orden, rol, canal, contenido, nombre_modelo,
    version_instrucciones, tokens_entrada, tokens_salida, latencia_ms, metadatos, respuesta_a
)
SELECT id, cantidad_mensajes, @rol, @canal, @contenido, sqlc.narg('nombre_modelo'),
    sqlc.narg('version_instrucciones'), sqlc.narg('tokens_entrada'), sqlc.narg('tokens_salida'),
    sqlc.narg('latencia_ms'), @metadatos, sqlc.narg('respuesta_a')
FROM conversacion RETURNING *;

-- Contexto de los últimos 20 turnos de usuario, incluyendo sus respuestas y funciones.
-- Así el corte no deja resultados de herramienta sin el turno que los originó.
-- name: ContextoConversacion :many
SELECT m.* FROM asistente.mensaje m
JOIN asistente.conversacion c ON c.id = m.conversacion_id
WHERE c.id = @conversacion_id AND c.usuario_id = @usuario_id
AND m.numero_orden >= (
    SELECT min(ultimos.numero_orden) FROM (
        SELECT u.numero_orden FROM asistente.mensaje u
        WHERE u.conversacion_id = @conversacion_id AND u.rol = 'usuario'
        ORDER BY u.numero_orden DESC LIMIT 20
    ) ultimos
)
ORDER BY m.numero_orden;

-- El estado sin respuesta se deriva sin editar el turno emitido (DBML append-only).
-- name: MensajesSinRespuesta :many
SELECT m.* FROM asistente.mensaje m
JOIN asistente.conversacion c ON c.id = m.conversacion_id
WHERE c.id = @conversacion_id AND c.usuario_id = @usuario_id AND m.rol = 'usuario'
AND NOT EXISTS (SELECT 1 FROM asistente.mensaje r WHERE r.respuesta_a = m.id)
ORDER BY m.numero_orden;

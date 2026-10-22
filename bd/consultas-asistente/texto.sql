-- Serializa únicamente la admisión en DB, nunca la llamada de red.
-- name: BloquearEntradaTexto :exec
SELECT pg_advisory_xact_lock(hashtextextended(@usuario::text,0));

-- name: SolicitudTextoPorClave :one
SELECT * FROM asistente.solicitud_texto WHERE usuario_id = @usuario_id AND clave = @clave;

-- name: RegistrarSolicitudTexto :exec
INSERT INTO asistente.solicitud_texto(usuario_id,clave,conversacion_id,mensaje_id,huella)
VALUES(@usuario_id,@clave,@conversacion_id,@mensaje_id,@huella);

-- name: ConversacionParaTexto :one
SELECT * FROM asistente.conversacion WHERE id = @id AND usuario_id = @usuario_id AND estado='activo' FOR UPDATE;

-- name: UltimoUsuarioTexto :one
SELECT m.*,EXISTS(SELECT 1 FROM asistente.mensaje r WHERE r.respuesta_a=m.id) AS respondido
FROM asistente.mensaje m WHERE m.conversacion_id = @conversacion_id AND m.rol='usuario'
ORDER BY m.numero_orden DESC LIMIT 1;

-- name: RespuestaTextoPersistida :one
SELECT m.* FROM asistente.mensaje m JOIN asistente.conversacion c ON c.id=m.conversacion_id
WHERE c.id = @conversacion_id AND c.usuario_id = @usuario_id AND m.respuesta_a = @mensaje_id AND m.rol='asistente'
ORDER BY m.numero_orden DESC LIMIT 1;

-- name: ReservarEntradaTexto :execrows
UPDATE asistente.conversacion SET ciclo_token = @token,
    ciclo_expira_en=LEAST(@expira_en::timestamptz,clock_timestamp()+interval '45 seconds')
WHERE id = @conversacion_id AND usuario_id = @usuario_id AND estado='activo'
AND (ciclo_expira_en IS NULL OR ciclo_expira_en<=clock_timestamp());

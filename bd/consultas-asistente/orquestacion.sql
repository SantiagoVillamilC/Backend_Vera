-- El mensaje debe ser el último del usuario y no tener respuesta final.
-- El UUID de cercado evita que un proceso atrasado escriba o libere el ciclo de otro.
-- name: AdquirirCiclo :one
UPDATE asistente.conversacion c
SET ciclo_token = @token, ciclo_expira_en = LEAST(@expira_en::timestamptz, clock_timestamp() + interval '45 seconds')
WHERE c.id = @conversacion_id AND c.usuario_id = @usuario_id AND c.estado = 'activo'
AND (c.ciclo_expira_en IS NULL OR c.ciclo_expira_en <= clock_timestamp())
AND EXISTS (
    SELECT 1 FROM asistente.mensaje m
    WHERE m.id = @mensaje_id AND m.conversacion_id = c.id AND m.rol = 'usuario'
    AND NOT EXISTS (SELECT 1 FROM asistente.mensaje r WHERE r.respuesta_a = m.id)
    AND NOT EXISTS (SELECT 1 FROM asistente.mensaje u WHERE u.conversacion_id = c.id AND u.rol = 'usuario' AND u.numero_orden > m.numero_orden)
)
RETURNING c.id;

-- name: ValidarCiclo :one
SELECT id FROM asistente.conversacion
WHERE id = @conversacion_id AND usuario_id = @usuario_id
AND ciclo_token = @token AND ciclo_expira_en > clock_timestamp() AND estado = 'activo'
FOR UPDATE;

-- name: LiberarCiclo :exec
UPDATE asistente.conversacion SET ciclo_token = NULL, ciclo_expira_en = NULL
WHERE id = @conversacion_id AND usuario_id = @usuario_id AND ciclo_token = @token;

-- name: MensajeDeCiclo :one
SELECT m.* FROM asistente.mensaje m
JOIN asistente.conversacion c ON c.id = m.conversacion_id
WHERE m.id = @mensaje_id AND c.id = @conversacion_id AND c.usuario_id = @usuario_id AND m.rol = 'usuario';

-- Nunca exponer los turnos de function calling al historial de la aplicación.
-- name: MensajesPublicosConversacion :many
SELECT m.* FROM asistente.mensaje m
JOIN asistente.conversacion c ON c.id = m.conversacion_id
WHERE c.id = @conversacion_id AND c.usuario_id = @usuario_id
AND m.rol IN ('usuario','asistente') AND (m.metadatos->>'interno') IS DISTINCT FROM 'true'
ORDER BY m.numero_orden;

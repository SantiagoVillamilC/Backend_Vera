-- El orden global de bloqueos es conversación, acción y luego cuentas del núcleo.
-- name: BloquearConversacionConfirmacion :one
SELECT * FROM asistente.conversacion WHERE id = @id AND usuario_id = @usuario_id FOR UPDATE;

-- El cliente sólo puede elevar el segundo factor; nunca rebajarlo.
-- name: ElevarCodigoAccion :one
UPDATE asistente.accion_pendiente SET requiere_codigo=true
WHERE id = @id AND usuario_id = @usuario_id AND estado='pendiente' AND expira_en>clock_timestamp()
RETURNING *;

-- Barrido con el mismo orden de candados que las confirmaciones.
-- name: ConversacionesConAccionesVencidas :many
SELECT c.* FROM asistente.conversacion c
WHERE EXISTS (SELECT 1 FROM asistente.accion_pendiente a WHERE a.conversacion_id=c.id AND a.estado='pendiente' AND a.expira_en<=clock_timestamp())
ORDER BY c.id LIMIT sqlc.arg('limite')::integer FOR UPDATE OF c SKIP LOCKED;

-- name: VencerAccionesDeConversacion :many
UPDATE asistente.accion_pendiente SET estado='vencida',resuelta_en=clock_timestamp()
WHERE conversacion_id = @conversacion_id AND estado='pendiente' AND expira_en<=clock_timestamp()
RETURNING *;

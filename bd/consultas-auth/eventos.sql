-- name: RegistrarEventoAutenticacion :exec
INSERT INTO auth.eventos_autenticacion
    (tipo, actor_parte_id, credencial_id, resultado, ocurrido_en, origen, contexto, sesion_id, detalle)
VALUES (
    sqlc.arg('tipo'),
    sqlc.narg('actor_parte_id'),
    sqlc.narg('credencial_id'),
    sqlc.arg('resultado'),
    sqlc.arg('ocurrido_en'),
    sqlc.arg('origen'),
    sqlc.arg('contexto'),
    sqlc.narg('sesion_id'),
    sqlc.arg('detalle')
);

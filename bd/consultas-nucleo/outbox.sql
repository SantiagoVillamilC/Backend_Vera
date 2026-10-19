-- name: InsertarOutbox :one
INSERT INTO nucleo.outbox (tipo, payload)
VALUES (@tipo, @payload)
RETURNING id, tipo, payload, disponible_en, intentos, ultimo_error, despachado_en;

-- name: ObtenerOutbox :one
SELECT id, tipo, payload, disponible_en, intentos, ultimo_error, despachado_en
  FROM nucleo.outbox
 WHERE id = @id;

-- name: TomarOutboxPendientes :many
SELECT id, tipo, payload, disponible_en, intentos, ultimo_error, despachado_en
  FROM nucleo.outbox
 WHERE despachado_en IS NULL
   AND disponible_en <= now()
 ORDER BY id
 LIMIT @limite
   FOR UPDATE SKIP LOCKED;

-- name: AvanzarVentanaOutbox :execrows
UPDATE nucleo.outbox
   SET disponible_en = now() + @intervalo::interval
 WHERE id = @id;

-- name: ReintentarOutbox :one
UPDATE nucleo.outbox
   SET intentos = nucleo.outbox.intentos + 1,
       ultimo_error = @ultimo_error
 WHERE id = @id
RETURNING intentos;

-- name: MarcarOutboxDespachado :execrows
UPDATE nucleo.outbox
   SET despachado_en = now()
 WHERE id = @id;

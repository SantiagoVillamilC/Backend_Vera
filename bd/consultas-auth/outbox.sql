-- name: InsertarAviso :one
INSERT INTO auth.outbox (tipo, payload)
VALUES (@tipo, @payload)
RETURNING id, tipo, payload, disponible_en, intentos, ultimo_error, despachado_en, id_mensaje_proveedor;

-- name: TomarAvisosPendientes :many
SELECT id, tipo, payload, disponible_en, intentos, ultimo_error, despachado_en, id_mensaje_proveedor
  FROM auth.outbox
 WHERE despachado_en IS NULL
   AND disponible_en <= now()
 ORDER BY id
 LIMIT @limite
   FOR UPDATE SKIP LOCKED;

-- name: AvanzarVentanaAviso :execrows
UPDATE auth.outbox
   SET disponible_en = now() + @intervalo::interval
 WHERE id = @id;

-- name: ReintentarAviso :one
UPDATE auth.outbox
   SET intentos = auth.outbox.intentos + 1,
       ultimo_error = @ultimo_error
 WHERE id = @id
RETURNING intentos;

-- name: MarcarAvisoEntregado :execrows
UPDATE auth.outbox
   SET despachado_en = now(),
       id_mensaje_proveedor = @id_mensaje_proveedor
 WHERE id = @id;

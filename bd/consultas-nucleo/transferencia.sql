-- name: ObtenerTitularDeCuenta :one
SELECT titular_id
  FROM nucleo.cuenta
 WHERE id = @id;

-- name: InsertarTransferencia :one
INSERT INTO nucleo.transferencia (id, usuario_id, cuenta_origen, llave_destino, monto, moneda, idempotency_key, estado)
VALUES (@id, @usuario_id, @cuenta_origen, @llave_destino, @monto, @moneda, @idempotency_key, @estado)
RETURNING id, usuario_id, cuenta_origen, llave_destino, monto, moneda, idempotency_key, estado, creada_en, actualizada_en;

-- name: ObtenerTransferencia :one
SELECT id, usuario_id, cuenta_origen, llave_destino, monto, moneda, idempotency_key, estado, creada_en, actualizada_en
  FROM nucleo.transferencia
 WHERE id = @id;

-- name: ObtenerTransferenciaPorIdempotencyKey :one
SELECT id, usuario_id, cuenta_origen, llave_destino, monto, moneda, idempotency_key, estado, creada_en, actualizada_en
  FROM nucleo.transferencia
 WHERE usuario_id = @usuario_id
   AND idempotency_key = @idempotency_key;

-- name: AvanzarEstadoTransferencia :one
UPDATE nucleo.transferencia
   SET estado = @estado_nuevo,
       actualizada_en = now()
 WHERE id = @id
   AND estado = @estado_esperado
   AND (
          (estado = 'creada'        AND @estado_nuevo = 'reservada')
       OR (estado = 'reservada'     AND @estado_nuevo = 'despachada')
       OR (estado = 'despachada'    AND @estado_nuevo IN ('confirmada', 'fallida', 'indeterminada'))
       OR (estado = 'fallida'       AND @estado_nuevo = 'compensada')
       OR (estado = 'indeterminada' AND @estado_nuevo IN ('confirmada', 'compensada'))
   )
RETURNING id, usuario_id, cuenta_origen, llave_destino, monto, moneda, idempotency_key, estado, creada_en, actualizada_en;

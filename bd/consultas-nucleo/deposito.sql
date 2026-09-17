-- name: InsertarDeposito :one
INSERT INTO nucleo.deposito (id, usuario_id, monto, moneda, referencia, idempotency_key, cus)
VALUES (@id, @usuario_id, @monto, @moneda, @referencia, @idempotency_key, @cus)
RETURNING id, usuario_id, monto, moneda, referencia, idempotency_key, cus, estado, creada_en, actualizada_en;

-- name: ObtenerDeposito :one
SELECT id, usuario_id, monto, moneda, referencia, idempotency_key, cus, estado, creada_en, actualizada_en
  FROM nucleo.deposito
 WHERE id = @id;

-- name: ObtenerDepositoPorReferencia :one
SELECT id, usuario_id, monto, moneda, referencia, idempotency_key, cus, estado, creada_en, actualizada_en
  FROM nucleo.deposito
 WHERE referencia = @referencia;

-- name: ObtenerDepositoPorIdempotencyKey :one
SELECT id, usuario_id, monto, moneda, referencia, idempotency_key, cus, estado, creada_en, actualizada_en
  FROM nucleo.deposito
 WHERE usuario_id = @usuario_id
   AND idempotency_key = @idempotency_key;

-- name: ConfirmarDeposito :one
UPDATE nucleo.deposito
   SET estado = 'confirmada',
       cus = COALESCE(@cus, cus),
       actualizada_en = now()
 WHERE id = @id
   AND estado = 'pendiente'
RETURNING id, usuario_id, monto, moneda, referencia, idempotency_key, cus, estado, creada_en, actualizada_en;

-- name: RechazarDeposito :execrows
UPDATE nucleo.deposito
   SET estado = 'rechazada',
       actualizada_en = now()
 WHERE id = @id
   AND estado = 'pendiente';

-- name: ExpiarDeposito :execrows
UPDATE nucleo.deposito
   SET estado = 'expirada',
       actualizada_en = now()
 WHERE id = @id
   AND estado = 'pendiente';

-- name: ExpiarDepositosVencidos :execrows
UPDATE nucleo.deposito
   SET estado = 'expirada',
       actualizada_en = now()
 WHERE estado = 'pendiente'
   AND creada_en < now() - make_interval(secs => @segundos);

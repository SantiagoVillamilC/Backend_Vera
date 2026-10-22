-- Sólo permite reintentar una entrega fallida sin intentos de verificación.
-- Conserva enfriamiento y reenvíos; nunca recupera un desafío agotado o usado.
-- name: ReenviarEntregaConfirmacion :one
UPDATE auth.desafios SET estado='pendiente',codigo_hmac = @codigo_hmac,
    reenvios=reenvios+1,ultimo_envio_en = @ahora,expira_en = @expira_en
WHERE id = @id AND proposito='confirmacion_transferencia' AND estado='invalidado'
AND intentos=0 AND reenvios<3 AND ultimo_envio_en <= @umbral
RETURNING *;

-- Página de asientos propios por fecha, no de postings ni de subcuentas.
-- Los asientos sólo retenidos no agregan gasto: la salida ya se vio en la reserva.
-- name: ApuntesDePeriodo :many
WITH entradas AS MATERIALIZED (
    SELECT DISTINCT p.entry_id, p.ocurrido_en
    FROM nucleo.posting p
    JOIN nucleo.cuenta propia ON propia.id = p.cuenta_id AND propia.moneda = p.moneda
    WHERE p.cuenta_id = @disponible::text AND p.moneda = 'COP'
    AND p.ocurrido_en >= @desde::timestamptz AND p.ocurrido_en < @hasta::timestamptz
    AND (sqlc.narg('cursor_fecha')::timestamptz IS NULL
         OR (p.ocurrido_en, p.entry_id) < (sqlc.narg('cursor_fecha')::timestamptz, @cursor_id::bigint))
    ORDER BY p.ocurrido_en DESC, p.entry_id DESC LIMIT 500
)
SELECT j.id AS entry_id, j.tipo, j.ocurrido_en, p.cuenta_id, p.monto,
    COALESCE(t.usuario_id, '')::text AS emisor,
    COALESCE(t.llave_destino, '')::text AS destino,
    COALESCE(receptor.titular_id, '')::text AS receptor
FROM entradas e
JOIN nucleo.journal_entry j ON j.id = e.entry_id
JOIN nucleo.posting p ON p.entry_id = e.entry_id AND p.moneda = 'COP'
LEFT JOIN nucleo.transferencia t ON t.id = j.transferencia_id
LEFT JOIN LATERAL (
    SELECT c.titular_id
    FROM nucleo.journal_entry r
    JOIN nucleo.posting rp ON rp.entry_id = r.id AND rp.monto < 0 AND rp.moneda = 'COP'
    JOIN nucleo.cuenta c ON c.id = rp.cuenta_id AND c.id = 'pasivo:usuario:' || c.titular_id || ':disponible'
    WHERE r.transferencia_id = t.id AND r.tipo = 'transferencia_interna'
    ORDER BY r.id, rp.id LIMIT 1
) receptor ON true
ORDER BY j.ocurrido_en DESC, j.id DESC, p.id;

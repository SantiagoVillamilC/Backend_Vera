-- El receptor sale del asiento liquidado, nunca de resolver hoy una llave antigua.
-- name: DestinatariosPrevios :many
SELECT DISTINCT c.titular_id
FROM nucleo.transferencia t
JOIN nucleo.journal_entry j ON j.transferencia_id = t.id AND j.tipo = 'transferencia_interna'
JOIN nucleo.posting p ON p.entry_id = j.id AND p.monto < 0 AND p.moneda = 'COP'
JOIN nucleo.cuenta c ON c.id = p.cuenta_id AND c.id = 'pasivo:usuario:' || c.titular_id || ':disponible'
WHERE t.usuario_id = @usuario::text AND t.estado = 'confirmada'
AND c.titular_id <> @usuario::text AND c.titular_id > @despues::text
ORDER BY c.titular_id LIMIT 100;

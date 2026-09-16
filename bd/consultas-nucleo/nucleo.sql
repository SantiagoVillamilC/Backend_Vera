-- name: InsertarAsiento :one
INSERT INTO nucleo.journal_entry (tipo, transferencia_id, ocurrido_en, descripcion)
VALUES ($1, $2, $3, $4)
RETURNING id, tipo, transferencia_id, ocurrido_en, registrado_en, descripcion;

-- name: InsertarLinea :one
INSERT INTO nucleo.posting (entry_id, cuenta_id, monto, moneda)
VALUES ($1, $2, $3, $4)
RETURNING id, entry_id, cuenta_id, monto, moneda;

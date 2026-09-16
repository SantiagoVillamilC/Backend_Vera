-- name: InsertarAsiento :one
INSERT INTO nucleo.journal_entry (tipo, transferencia_id, deposito_id, ocurrido_en, descripcion)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, tipo, transferencia_id, ocurrido_en, registrado_en, descripcion, deposito_id;

-- name: InsertarLinea :one
INSERT INTO nucleo.posting (entry_id, cuenta_id, monto, moneda)
VALUES ($1, $2, $3, $4)
RETURNING id, entry_id, cuenta_id, monto, moneda;

-- name: InsertarSubcuentasDeUsuario :execrows
INSERT INTO nucleo.cuenta (id, naturaleza, clase, titular_id)
VALUES (@disponible, 'pasivo', 'usuario', @titular_id),
       (@retenido, 'pasivo', 'usuario', @titular_id)
ON CONFLICT (id) DO NOTHING;

-- name: SaldoDeCuenta :one
SELECT COALESCE(SUM(p.monto), 0)::bigint AS saldo
  FROM nucleo.posting p
  JOIN nucleo.cuenta c ON c.id = p.cuenta_id
 WHERE p.cuenta_id = @cuenta_id
   AND p.moneda = c.moneda;

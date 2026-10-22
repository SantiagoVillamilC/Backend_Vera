-- Sin FOR UPDATE: la preparación no reserva ni bloquea el disponible.
-- name: CuentasParaPrevalidacion :many
SELECT id FROM nucleo.cuenta WHERE id = ANY(@cuentas::text[]) ORDER BY id;

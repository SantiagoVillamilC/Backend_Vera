-- name: BloquearCuentasOrdenadas :many
SELECT id
  FROM nucleo.cuenta
 WHERE id = ANY(@cuentas::text[])
 ORDER BY id
   FOR UPDATE;

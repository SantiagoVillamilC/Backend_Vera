-- +goose Up

-- El CUS llega con la confirmación (NF-A1-07.4) y se escribe junto al estado, nunca después del insert.
GRANT UPDATE (cus) ON nucleo.deposito TO rol_nucleo;

-- +goose Down
REVOKE UPDATE (cus) ON nucleo.deposito FROM rol_nucleo;

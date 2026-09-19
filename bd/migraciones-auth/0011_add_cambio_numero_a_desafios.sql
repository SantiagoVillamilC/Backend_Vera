-- Columnas del cambio de numero en el desafio: numero nuevo y encadenamiento del OTP viejo con el nuevo (AUT-A3-03.1).

-- +goose Up
ALTER TABLE auth.desafios
    ADD COLUMN celular_nuevo text,
    ADD COLUMN desafio_padre_id uuid;

-- +goose Down
ALTER TABLE auth.desafios
    DROP COLUMN desafio_padre_id,
    DROP COLUMN celular_nuevo;

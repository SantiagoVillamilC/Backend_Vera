-- +goose Up
ALTER TABLE auth.desafios DROP CONSTRAINT desafios_proposito_check;
ALTER TABLE auth.desafios ADD CONSTRAINT desafios_proposito_check CHECK
    (proposito IN ('registro','login','recuperacion','cambio_numero','cambio_numero_viejo','confirmacion_transferencia'));

-- +goose Down
-- Conservar el historial financiero sin habilitar nuevos desafíos de ese propósito.
ALTER TABLE auth.desafios DROP CONSTRAINT desafios_proposito_check;
ALTER TABLE auth.desafios ADD CONSTRAINT desafios_proposito_check CHECK
    (proposito IN ('registro','login','recuperacion','cambio_numero','cambio_numero_viejo')) NOT VALID;

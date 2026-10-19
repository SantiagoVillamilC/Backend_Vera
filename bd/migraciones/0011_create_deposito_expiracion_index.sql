-- +goose Up

-- El barrido de expiración filtra las pendientes por creada_en: índice parcial en vez de seq scan.
CREATE INDEX IF NOT EXISTS nucleo_deposito_pendiente_creada_idx
    ON nucleo.deposito (creada_en)
 WHERE estado = 'pendiente';

-- +goose Down
DROP INDEX IF EXISTS nucleo.nucleo_deposito_pendiente_creada_idx;

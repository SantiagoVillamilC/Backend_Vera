-- +goose Up
CREATE TABLE nucleo.extracto_custodio (
    corte_en timestamptz PRIMARY KEY,
    saldo    bigint NOT NULL,
    origen   text NOT NULL
);

-- Registro inmutable: la conciliación solo inserta y lee; nada de UPDATE, DELETE ni TRUNCATE.
GRANT SELECT, INSERT ON nucleo.extracto_custodio TO rol_nucleo;
REVOKE UPDATE, DELETE, TRUNCATE ON nucleo.extracto_custodio FROM rol_nucleo;

-- +goose Down
DROP TABLE nucleo.extracto_custodio;

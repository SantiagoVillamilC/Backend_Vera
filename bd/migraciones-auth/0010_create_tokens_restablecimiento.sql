-- Tokens de restablecimiento de contrasena de un solo uso (AUT-A3-02.2).

-- +goose Up
CREATE TABLE auth.tokens_restablecimiento (
    id            uuid PRIMARY KEY,
    credencial_id uuid NOT NULL REFERENCES auth.credenciales(id),
    token_hash    bytea NOT NULL UNIQUE,
    expira_en     timestamptz NOT NULL,
    usado_en      timestamptz,
    creado_en     timestamptz NOT NULL DEFAULT now()
);

-- Indice parcial: el consumo de A3-02.2 solo mira los tokens vigentes.
CREATE INDEX tokens_restablecimiento_pendientes ON auth.tokens_restablecimiento (token_hash) WHERE usado_en IS NULL;

GRANT SELECT, INSERT ON auth.tokens_restablecimiento TO rol_auth;
-- UPDATE acotado a usado_en; sin DELETE ni TRUNCATE.
GRANT UPDATE (usado_en) ON auth.tokens_restablecimiento TO rol_auth;
REVOKE DELETE, TRUNCATE ON auth.tokens_restablecimiento FROM rol_auth;

-- +goose Down
DROP TABLE auth.tokens_restablecimiento;

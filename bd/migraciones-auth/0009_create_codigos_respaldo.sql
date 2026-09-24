-- Codigos de respaldo de un solo uso que se emiten al activar la cuenta (AUT-A1-05.2).

-- +goose Up
CREATE TABLE auth.codigos_respaldo (
    id            uuid PRIMARY KEY,
    credencial_id uuid NOT NULL REFERENCES auth.credenciales(id),
    codigo_hmac   bytea NOT NULL,
    usado_en      timestamptz,
    creado_en     timestamptz NOT NULL DEFAULT now()
);

-- Indice parcial: el consumo de A3-02.2 solo mira los codigos vigentes de la credencial.
CREATE INDEX codigos_respaldo_pendientes ON auth.codigos_respaldo (credencial_id) WHERE usado_en IS NULL;

GRANT SELECT, INSERT ON auth.codigos_respaldo TO rol_auth;
-- UPDATE acotado a usado_en; sin DELETE ni TRUNCATE.
GRANT UPDATE (usado_en) ON auth.codigos_respaldo TO rol_auth;
REVOKE DELETE, TRUNCATE ON auth.codigos_respaldo FROM rol_auth;

-- +goose Down
DROP TABLE auth.codigos_respaldo;

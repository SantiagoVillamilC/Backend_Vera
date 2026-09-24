-- Outbox transaccional de avisos de auth (AUT-A2-04).

-- +goose Up
CREATE TABLE auth.outbox (
    id                   bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
    tipo                 text NOT NULL,
    payload              jsonb NOT NULL,
    disponible_en        timestamptz NOT NULL DEFAULT now(),
    intentos             integer NOT NULL DEFAULT 0,
    ultimo_error         text,
    despachado_en        timestamptz,
    id_mensaje_proveedor text
);

-- Índice parcial: el barrido solo busca lo pendiente y vencido, así que no indexa lo ya despachado.
CREATE INDEX outbox_pendientes ON auth.outbox (disponible_en) WHERE despachado_en IS NULL;

GRANT SELECT, INSERT ON auth.outbox TO rol_auth;

-- FOR UPDATE del barrido exige privilegio de UPDATE; acotado a las cinco columnas mutables.
GRANT UPDATE (disponible_en, intentos, ultimo_error, despachado_en, id_mensaje_proveedor) ON auth.outbox TO rol_auth;

REVOKE DELETE, TRUNCATE ON auth.outbox FROM rol_auth;

-- +goose Down
DROP TABLE auth.outbox;

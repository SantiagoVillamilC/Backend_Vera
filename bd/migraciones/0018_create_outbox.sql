-- +goose Up
CREATE TABLE nucleo.outbox (
    id            bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
    tipo          text NOT NULL,
    payload       jsonb NOT NULL,
    disponible_en timestamptz NOT NULL DEFAULT now(),
    intentos      integer NOT NULL DEFAULT 0,
    ultimo_error  text,
    despachado_en timestamptz
);

-- Índice parcial: el barrido solo busca lo pendiente y vencido, así que no indexa lo ya despachado.
CREATE INDEX outbox_pendientes ON nucleo.outbox (disponible_en) WHERE despachado_en IS NULL;

GRANT SELECT, INSERT ON nucleo.outbox TO rol_nucleo;

-- FOR UPDATE del barrido exige privilegio de UPDATE; acotado a las cuatro columnas mutables.
GRANT UPDATE (disponible_en, intentos, ultimo_error, despachado_en) ON nucleo.outbox TO rol_nucleo;

REVOKE DELETE, TRUNCATE ON nucleo.outbox FROM rol_nucleo;

-- +goose Down
DROP TABLE nucleo.outbox;

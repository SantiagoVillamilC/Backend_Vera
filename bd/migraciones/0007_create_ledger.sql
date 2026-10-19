-- +goose Up
CREATE TABLE nucleo.cuenta (
    id         text PRIMARY KEY,
    naturaleza text NOT NULL CHECK (naturaleza IN ('activo', 'pasivo')),
    clase      text NOT NULL,
    titular_id text,
    moneda     char(3) NOT NULL DEFAULT 'COP',
    creada_en  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE nucleo.journal_entry (
    id               bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
    tipo             text NOT NULL,
    transferencia_id uuid,
    ocurrido_en      timestamptz NOT NULL,
    registrado_en    timestamptz NOT NULL DEFAULT now(),
    descripcion      text
);

CREATE TABLE nucleo.posting (
    id        bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
    entry_id  bigint NOT NULL REFERENCES nucleo.journal_entry(id),
    cuenta_id text NOT NULL REFERENCES nucleo.cuenta(id),
    monto     bigint NOT NULL CHECK (monto <> 0),
    moneda    char(3) NOT NULL
);

CREATE INDEX posting_por_cuenta ON nucleo.posting (cuenta_id, id);
CREATE INDEX posting_por_asiento ON nucleo.posting (entry_id);

-- Mínimo privilegio: SELECT e INSERT en el ledger; nada de UPDATE, DELETE ni TRUNCATE.
GRANT SELECT, INSERT ON nucleo.cuenta, nucleo.journal_entry, nucleo.posting TO rol_nucleo;
REVOKE UPDATE, DELETE, TRUNCATE ON nucleo.cuenta, nucleo.journal_entry, nucleo.posting FROM rol_nucleo;

-- +goose Down
DROP TABLE nucleo.posting;
DROP TABLE nucleo.journal_entry;
DROP TABLE nucleo.cuenta;

-- +goose Up
CREATE TABLE nucleo.deposito (
    id              uuid PRIMARY KEY,
    usuario_id      text NOT NULL,
    monto           bigint NOT NULL CHECK (monto > 0),
    moneda          char(3) NOT NULL DEFAULT 'COP',
    referencia      text NOT NULL,
    idempotency_key text NOT NULL,
    cus             text,
    estado          text NOT NULL DEFAULT 'pendiente'
        CHECK (estado IN ('pendiente', 'confirmada', 'rechazada', 'expirada')),
    creada_en       timestamptz NOT NULL DEFAULT now(),
    actualizada_en  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT deposito_referencia_unica UNIQUE (referencia),
    CONSTRAINT deposito_idempotencia_unica UNIQUE (usuario_id, idempotency_key)
);

ALTER TABLE nucleo.journal_entry
    ADD COLUMN deposito_id uuid REFERENCES nucleo.deposito(id);

-- A lo sumo un origen: hay asientos sin transferencia ni depósito (ajustes, GMF, cuadre).
ALTER TABLE nucleo.journal_entry
    ADD CONSTRAINT journal_entry_origen_unico
        CHECK (num_nonnulls(transferencia_id, deposito_id) <= 1);

-- Un asiento por intención a lo sumo: el índice parcial deja libres los asientos sin depósito.
CREATE UNIQUE INDEX journal_entry_deposito_unico
    ON nucleo.journal_entry (deposito_id)
 WHERE deposito_id IS NOT NULL;

-- deposito es máquina de estados, no diario: UPDATE acotado a las dos columnas que mueve el CAS.
GRANT SELECT, INSERT ON nucleo.deposito TO rol_nucleo;
GRANT UPDATE (estado, actualizada_en) ON nucleo.deposito TO rol_nucleo;
REVOKE DELETE, TRUNCATE ON nucleo.deposito FROM rol_nucleo;

-- +goose Down
DROP INDEX nucleo.journal_entry_deposito_unico;
ALTER TABLE nucleo.journal_entry DROP CONSTRAINT journal_entry_origen_unico;
ALTER TABLE nucleo.journal_entry DROP COLUMN deposito_id;
DROP TABLE nucleo.deposito;

-- Código de rubro del comercio (MCC) en el asiento.
--
-- El asiento de liquidación es la única pata del ledger que ve al comercio externo. Guardar ahí el
-- MCC de cuatro dígitos deja que la cascada lo lea sin tocar la tabla de transferencias ni el
-- contrato de la app, y mantiene el dato pegado al movimiento que lo originó.

-- +goose Up
ALTER TABLE nucleo.journal_entry
    ADD COLUMN mcc text;

ALTER TABLE nucleo.journal_entry
    ADD CONSTRAINT journal_entry_mcc_cuatro_digitos
        CHECK (mcc IS NULL OR mcc ~ '^[0-9]{4}$');

-- +goose Down
ALTER TABLE nucleo.journal_entry
    DROP CONSTRAINT IF EXISTS journal_entry_mcc_cuatro_digitos;
ALTER TABLE nucleo.journal_entry
    DROP COLUMN IF EXISTS mcc;

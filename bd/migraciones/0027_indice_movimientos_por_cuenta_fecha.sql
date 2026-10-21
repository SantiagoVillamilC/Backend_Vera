-- +goose Up
-- El índice compuesto requiere fecha y cuenta en posting. La fecha es copia del
-- asiento, nunca entrada del cliente; no se alteran montos ni el diario original.
ALTER TABLE nucleo.posting ADD COLUMN ocurrido_en timestamptz;
UPDATE nucleo.posting p SET ocurrido_en = j.ocurrido_en FROM nucleo.journal_entry j WHERE j.id = p.entry_id;
ALTER TABLE nucleo.posting ALTER COLUMN ocurrido_en SET NOT NULL;
-- +goose StatementBegin
CREATE FUNCTION nucleo.fijar_fecha_posting() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    SELECT ocurrido_en INTO STRICT NEW.ocurrido_en FROM nucleo.journal_entry WHERE id = NEW.entry_id;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER posting_fecha_asiento BEFORE INSERT ON nucleo.posting
FOR EACH ROW EXECUTE FUNCTION nucleo.fijar_fecha_posting();
CREATE INDEX posting_cuenta_fecha_idx ON nucleo.posting (cuenta_id, ocurrido_en DESC, entry_id DESC) INCLUDE (moneda);
CREATE INDEX journal_transferencia_tipo_idx ON nucleo.journal_entry (transferencia_id, tipo, id);

-- +goose Down
DROP INDEX nucleo.journal_transferencia_tipo_idx;
DROP INDEX nucleo.posting_cuenta_fecha_idx;
DROP TRIGGER posting_fecha_asiento ON nucleo.posting;
DROP FUNCTION nucleo.fijar_fecha_posting();
ALTER TABLE nucleo.posting DROP COLUMN ocurrido_en;

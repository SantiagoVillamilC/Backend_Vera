-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION nucleo.verificar_asiento_cuadra()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    desbalance bigint;
    lineas     integer;
BEGIN
    SELECT COALESCE(SUM(monto), 0), COUNT(*)
      INTO desbalance, lineas
      FROM nucleo.posting
     WHERE entry_id = NEW.entry_id;

    IF lineas < 2 THEN
        RAISE EXCEPTION 'asiento % tiene % linea(s), minimo 2', NEW.entry_id, lineas;
    END IF;

    IF desbalance <> 0 THEN
        RAISE EXCEPTION 'asiento % descuadrado en % centavos', NEW.entry_id, desbalance;
    END IF;

    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER asiento_cuadra
    AFTER INSERT ON nucleo.posting
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION nucleo.verificar_asiento_cuadra();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION nucleo.verificar_asiento_tiene_dos_lineas()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    lineas integer;
BEGIN
    SELECT COUNT(*)
      INTO lineas
      FROM nucleo.posting
     WHERE entry_id = NEW.id;

    IF lineas < 2 THEN
        RAISE EXCEPTION 'asiento % tiene % linea(s), minimo 2', NEW.id, lineas;
    END IF;

    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER asiento_minimo_dos_lineas
    AFTER INSERT ON nucleo.journal_entry
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION nucleo.verificar_asiento_tiene_dos_lineas();

-- +goose Down
DROP TRIGGER IF EXISTS asiento_minimo_dos_lineas ON nucleo.journal_entry;
DROP FUNCTION IF EXISTS nucleo.verificar_asiento_tiene_dos_lineas();
DROP TRIGGER IF EXISTS asiento_cuadra ON nucleo.posting;
DROP FUNCTION IF EXISTS nucleo.verificar_asiento_cuadra();

-- La parte que conversa puede operar en contexto de una empresa diferente.
-- +goose Up
ALTER TABLE asistente.accion_pendiente ADD COLUMN usuario_financiero_id text;
-- El trigger de estados prohíbe tocar acciones resueltas o vencidas. La migración
-- mantiene el bloqueo exclusivo de ALTER TABLE hasta completar la transacción.
ALTER TABLE asistente.accion_pendiente DISABLE TRIGGER accion_transicion;
UPDATE asistente.accion_pendiente SET usuario_financiero_id = usuario_id::text;
ALTER TABLE asistente.accion_pendiente ENABLE TRIGGER accion_transicion;
ALTER TABLE asistente.accion_pendiente ALTER COLUMN usuario_financiero_id SET NOT NULL;
ALTER TABLE asistente.accion_pendiente ADD CONSTRAINT accion_origen_presente CHECK (length(btrim(usuario_financiero_id)) > 0);
GRANT INSERT (usuario_financiero_id) ON asistente.accion_pendiente TO rol_asistente;

-- +goose StatementBegin
CREATE FUNCTION asistente.validar_origen_accion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.usuario_financiero_id IS DISTINCT FROM OLD.usuario_financiero_id THEN
        RAISE EXCEPTION 'origen financiero de accion inmutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER accion_origen_inmutable BEFORE UPDATE ON asistente.accion_pendiente
    FOR EACH ROW EXECUTE FUNCTION asistente.validar_origen_accion();
REVOKE ALL ON FUNCTION asistente.validar_origen_accion() FROM PUBLIC;

-- +goose Down
DROP TRIGGER accion_origen_inmutable ON asistente.accion_pendiente;
DROP FUNCTION asistente.validar_origen_accion();
ALTER TABLE asistente.accion_pendiente DROP COLUMN usuario_financiero_id;

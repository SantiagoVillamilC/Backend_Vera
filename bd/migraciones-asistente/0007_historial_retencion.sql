-- SCRUM-135: historial físico borrable; auditoría mínima independiente del contenido.
-- +goose Up
ALTER TABLE asistente.llamada_funcion DROP CONSTRAINT llamada_propietario_fk,
    DROP CONSTRAINT llamada_mensaje_fk;

-- Conserva los UUID de correlación de la auditoría aunque ya no exista el historial.
-- La validación y los candados siguen exigiendo referencias propias al insertar.
-- +goose StatementBegin
CREATE FUNCTION asistente.validar_referencia_auditoria() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
BEGIN
    PERFORM 1 FROM asistente.conversacion
        WHERE id = NEW.conversacion_id AND usuario_id = NEW.usuario_id FOR KEY SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'conversacion de auditoria no encontrada' USING ERRCODE = '23503';
    END IF;
    PERFORM 1 FROM asistente.mensaje
        WHERE conversacion_id = NEW.conversacion_id AND id = NEW.mensaje_id FOR KEY SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'mensaje de auditoria no encontrado' USING ERRCODE = '23503';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER llamada_referencia BEFORE INSERT ON asistente.llamada_funcion
    FOR EACH ROW EXECUTE FUNCTION asistente.validar_referencia_auditoria();
REVOKE ALL ON FUNCTION asistente.validar_referencia_auditoria() FROM PUBLIC;

ALTER TABLE asistente.accion_pendiente DROP CONSTRAINT accion_propietario_fk,
    ADD CONSTRAINT accion_propietario_fk FOREIGN KEY (conversacion_id, usuario_id)
        REFERENCES asistente.conversacion(id, usuario_id) ON DELETE CASCADE;
ALTER TABLE asistente.solicitud_texto DROP CONSTRAINT solicitud_texto_conversacion_id_usuario_id_fkey,
    DROP CONSTRAINT solicitud_texto_conversacion_id_mensaje_id_fkey,
    ADD CONSTRAINT solicitud_texto_conversacion_id_usuario_id_fkey FOREIGN KEY (conversacion_id, usuario_id)
        REFERENCES asistente.conversacion(id, usuario_id) ON DELETE CASCADE,
    ADD CONSTRAINT solicitud_texto_conversacion_id_mensaje_id_fkey FOREIGN KEY (conversacion_id, mensaje_id)
        REFERENCES asistente.mensaje(conversacion_id, id) ON DELETE CASCADE;

-- Borrado manual y retención usan la misma transición, bajo el candado de conversación.
-- Las transferencias y los desafíos pertenecen a otros módulos y no se borran en cascada.
-- +goose StatementBegin
CREATE FUNCTION asistente.cancelar_antes_de_borrar() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE asistente.accion_pendiente SET estado = 'cancelada', resuelta_en = clock_timestamp()
        WHERE conversacion_id = OLD.id AND estado = 'pendiente';
    RETURN OLD;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER conversacion_cancelar BEFORE DELETE ON asistente.conversacion
    FOR EACH ROW EXECUTE FUNCTION asistente.cancelar_antes_de_borrar();
REVOKE ALL ON FUNCTION asistente.cancelar_antes_de_borrar() FROM PUBLIC;
GRANT DELETE ON asistente.conversacion TO rol_asistente;

-- +goose Down
REVOKE DELETE ON asistente.conversacion FROM rol_asistente;
DROP TRIGGER conversacion_cancelar ON asistente.conversacion;
DROP FUNCTION asistente.cancelar_antes_de_borrar();
ALTER TABLE asistente.solicitud_texto DROP CONSTRAINT solicitud_texto_conversacion_id_usuario_id_fkey,
    DROP CONSTRAINT solicitud_texto_conversacion_id_mensaje_id_fkey,
    ADD FOREIGN KEY (conversacion_id, usuario_id) REFERENCES asistente.conversacion(id, usuario_id),
    ADD FOREIGN KEY (conversacion_id, mensaje_id) REFERENCES asistente.mensaje(conversacion_id, id);
ALTER TABLE asistente.accion_pendiente DROP CONSTRAINT accion_propietario_fk,
    ADD CONSTRAINT accion_propietario_fk FOREIGN KEY (conversacion_id, usuario_id)
        REFERENCES asistente.conversacion(id, usuario_id);
DROP TRIGGER llamada_referencia ON asistente.llamada_funcion;
DROP FUNCTION asistente.validar_referencia_auditoria();
-- NOT VALID conserva auditorías cuyo contenido ya se borró; valida futuras inserciones.
ALTER TABLE asistente.llamada_funcion ADD CONSTRAINT llamada_propietario_fk
    FOREIGN KEY (conversacion_id, usuario_id) REFERENCES asistente.conversacion(id, usuario_id) NOT VALID,
    ADD CONSTRAINT llamada_mensaje_fk FOREIGN KEY (conversacion_id, mensaje_id)
        REFERENCES asistente.mensaje(conversacion_id, id) NOT VALID;

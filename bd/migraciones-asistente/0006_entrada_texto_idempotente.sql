-- SCRUM-129: correlación de respuestas con su tarjeta y reintentos HTTP.
-- +goose Up
ALTER TABLE asistente.conversacion ADD COLUMN usuario_financiero_id text;
UPDATE asistente.conversacion SET usuario_financiero_id=usuario_id::text;
ALTER TABLE asistente.conversacion ALTER COLUMN usuario_financiero_id SET NOT NULL;
ALTER TABLE asistente.conversacion ADD CONSTRAINT conversacion_origen_presente CHECK(length(btrim(usuario_financiero_id))>0);
GRANT INSERT(usuario_financiero_id) ON asistente.conversacion TO rol_asistente;
ALTER TABLE asistente.accion_pendiente ADD CONSTRAINT accion_conversacion_id_uk UNIQUE(conversacion_id,id);
ALTER TABLE asistente.mensaje ADD COLUMN accion_id uuid;
ALTER TABLE asistente.mensaje ADD CONSTRAINT mensaje_accion_fk FOREIGN KEY(conversacion_id,accion_id)
    REFERENCES asistente.accion_pendiente(conversacion_id,id);
ALTER TABLE asistente.mensaje ADD CONSTRAINT mensaje_accion_final CHECK(accion_id IS NULL OR (rol='asistente' AND respuesta_a IS NOT NULL));
GRANT INSERT(accion_id) ON asistente.mensaje TO rol_asistente;

CREATE TABLE asistente.solicitud_texto(
    usuario_id uuid NOT NULL,
    clave uuid NOT NULL,
    conversacion_id uuid NOT NULL,
    mensaje_id uuid NOT NULL,
    huella bytea NOT NULL CHECK(octet_length(huella)=32),
    creada_en timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(usuario_id,clave),
    FOREIGN KEY(conversacion_id,usuario_id) REFERENCES asistente.conversacion(id,usuario_id),
    FOREIGN KEY(conversacion_id,mensaje_id) REFERENCES asistente.mensaje(conversacion_id,id)
);
GRANT SELECT,INSERT ON asistente.solicitud_texto TO rol_asistente;
REVOKE UPDATE,DELETE,TRUNCATE ON asistente.solicitud_texto FROM rol_asistente;

-- +goose Down
DROP TABLE asistente.solicitud_texto;
ALTER TABLE asistente.mensaje DROP COLUMN accion_id;
ALTER TABLE asistente.accion_pendiente DROP CONSTRAINT accion_conversacion_id_uk;
ALTER TABLE asistente.conversacion DROP COLUMN usuario_financiero_id;

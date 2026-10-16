-- SCRUM-117. Adaptación de ia.conversacion/mensaje del DBML a HU-BE-IA-02.
-- Se aplica después de auth: usuario_id es la parte del contexto autenticado.
-- +goose Up
CREATE SCHEMA asistente;
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'rol_asistente') THEN
        CREATE ROLE rol_asistente NOLOGIN;
    END IF;
END $$;
-- +goose StatementEnd
GRANT USAGE ON SCHEMA asistente TO rol_asistente;
GRANT rol_asistente TO rol_api WITH INHERIT FALSE, SET TRUE;

CREATE TABLE asistente.conversacion (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    usuario_id uuid NOT NULL REFERENCES auth.partes(id),
    titulo varchar(120),
    estado text NOT NULL DEFAULT 'activo' CHECK (estado IN ('activo', 'archivado')),
    cantidad_mensajes integer NOT NULL DEFAULT 0 CHECK (cantidad_mensajes >= 0),
    creada_en timestamptz NOT NULL DEFAULT clock_timestamp(),
    ultimo_mensaje_en timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX conversacion_reciente_idx ON asistente.conversacion
    (usuario_id, ultimo_mensaje_en DESC, id DESC);
CREATE INDEX conversacion_retencion_idx ON asistente.conversacion (ultimo_mensaje_en);

CREATE TABLE asistente.mensaje (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    conversacion_id uuid NOT NULL REFERENCES asistente.conversacion(id) ON DELETE CASCADE,
    numero_orden integer NOT NULL CHECK (numero_orden > 0),
    rol text NOT NULL CHECK (rol IN ('usuario', 'asistente', 'herramienta')),
    canal text NOT NULL CHECK (canal IN ('texto', 'voz')),
    contenido text NOT NULL CHECK (length(btrim(contenido)) > 0),
    nombre_modelo varchar(120),
    version_instrucciones text,
    tokens_entrada integer CHECK (tokens_entrada >= 0),
    tokens_salida integer CHECK (tokens_salida >= 0),
    latencia_ms integer CHECK (latencia_ms >= 0),
    -- Datos estructurados del protocolo: llamadas, identificadores y resultados.
    -- Nunca audio; no se expone este campo en el historial público.
    metadatos jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadatos) = 'object'),
    respuesta_a uuid,
    creada_en timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT mensaje_orden_uk UNIQUE (conversacion_id, numero_orden),
    CONSTRAINT mensaje_conversacion_id_uk UNIQUE (conversacion_id, id),
    CONSTRAINT mensaje_respuesta_fk FOREIGN KEY (conversacion_id, respuesta_a)
        REFERENCES asistente.mensaje(conversacion_id, id),
    CONSTRAINT mensaje_respuesta_rol CHECK (respuesta_a IS NULL OR rol = 'asistente')
);
CREATE INDEX mensaje_respuesta_idx ON asistente.mensaje (respuesta_a) WHERE respuesta_a IS NOT NULL;

GRANT SELECT, INSERT ON asistente.conversacion, asistente.mensaje TO rol_asistente;
GRANT UPDATE (titulo, estado, cantidad_mensajes, ultimo_mensaje_en) ON asistente.conversacion TO rol_asistente;
-- El borrado se habilitará con la política de acciones y retención de SCRUM-135.
REVOKE UPDATE, DELETE, TRUNCATE ON asistente.mensaje FROM rol_asistente;
REVOKE DELETE, TRUNCATE ON asistente.conversacion FROM rol_asistente;

-- +goose Down
DROP TABLE asistente.mensaje;
DROP TABLE asistente.conversacion;
REVOKE USAGE ON SCHEMA asistente FROM rol_asistente;
DROP SCHEMA asistente;
-- No eliminar roles del clúster: pueden estar en uso por otras bases.

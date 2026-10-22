-- SCRUM-138: eventos de auditoría, sin contenido de mensajes ni resultados de funciones.
-- +goose Up
CREATE TABLE asistente.llamada_funcion (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    llamada_id uuid NOT NULL,
    fase text NOT NULL CHECK (fase IN ('inicio','fin')),
    usuario_id uuid NOT NULL,
    conversacion_id uuid NOT NULL,
    mensaje_id uuid NOT NULL,
    funcion text NOT NULL CHECK (funcion IN ('consultar_saldo','consultar_limites',
        'listar_movimientos','resumir_movimientos','buscar_destinatario',
        'consultar_transferencia','preparar_transferencia','desconocida')),
    argumentos_saneados jsonb NOT NULL CHECK (jsonb_typeof(argumentos_saneados) = 'object'
        AND octet_length(argumentos_saneados::text) <= 2048),
    resultado varchar(40) NOT NULL CHECK (resultado ~ '^[A-Za-z][A-Za-z0-9]{0,39}$'),
    duracion_ms bigint NOT NULL CHECK (duracion_ms >= 0),
    request_id varchar(128) NOT NULL,
    version_instrucciones varchar(80) NOT NULL,
    ocurrida_en timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT llamada_fase_uk UNIQUE (llamada_id,fase),
    CONSTRAINT llamada_propietario_fk FOREIGN KEY (conversacion_id,usuario_id)
        REFERENCES asistente.conversacion(id,usuario_id),
    CONSTRAINT llamada_mensaje_fk FOREIGN KEY (conversacion_id,mensaje_id)
        REFERENCES asistente.mensaje(conversacion_id,id),
    CONSTRAINT llamada_inicio CHECK (fase <> 'inicio' OR (resultado='InProgress' AND duracion_ms=0))
);
CREATE INDEX llamada_mensaje_idx ON asistente.llamada_funcion(conversacion_id,mensaje_id,ocurrida_en,id);
CREATE INDEX llamada_fecha_idx ON asistente.llamada_funcion(ocurrida_en,id);
REVOKE ALL ON asistente.llamada_funcion FROM PUBLIC, rol_api, rol_asistente;
GRANT INSERT (llamada_id,fase,usuario_id,conversacion_id,mensaje_id,funcion,
    argumentos_saneados,resultado,duracion_ms,request_id,version_instrucciones)
    ON asistente.llamada_funcion TO rol_asistente;

-- +goose Down
DROP TABLE asistente.llamada_funcion;

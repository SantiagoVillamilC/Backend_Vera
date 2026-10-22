-- SCRUM-125: persistencia y máquina de estados; no ejecuta dinero ni verifica OTP.
-- +goose Up
ALTER TABLE asistente.conversacion ADD CONSTRAINT conversacion_propietario_uk UNIQUE (id, usuario_id);

CREATE TABLE asistente.accion_pendiente (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    usuario_id uuid NOT NULL,
    conversacion_id uuid NOT NULL,
    tipo text NOT NULL CHECK (tipo IN ('transferencia_interna', 'transferencia_llave')),
    destino text NOT NULL CHECK (length(btrim(destino)) > 0),
    tipo_llave text CHECK (tipo_llave IN ('documento', 'celular', 'correo', 'alfanumerico', 'comercio')),
    nombre_mostrado text NOT NULL CHECK (length(btrim(nombre_mostrado)) > 0),
    celular_enmascarado text,
    entidad_destino text,
    monto bigint NOT NULL CHECK (monto > 0),
    moneda text NOT NULL DEFAULT 'COP' CHECK (moneda = 'COP'),
    requiere_codigo boolean NOT NULL,
    desafio_id uuid REFERENCES auth.desafios(id),
    estado text NOT NULL DEFAULT 'pendiente'
        CHECK (estado IN ('pendiente', 'confirmada', 'cancelada', 'vencida', 'fallida')),
    transferencia_id uuid REFERENCES nucleo.transferencia(id),
    codigo_error text,
    creada_en timestamptz NOT NULL DEFAULT now(),
    expira_en timestamptz NOT NULL DEFAULT (now() + interval '5 minutes'),
    resuelta_en timestamptz,
    CONSTRAINT accion_propietario_fk FOREIGN KEY (conversacion_id, usuario_id)
        REFERENCES asistente.conversacion(id, usuario_id),
    CONSTRAINT accion_destino_tipo CHECK (
        (tipo = 'transferencia_interna' AND tipo_llave IS NULL)
        OR (tipo = 'transferencia_llave' AND tipo_llave IS NOT NULL)),
    CONSTRAINT accion_vigencia CHECK (expira_en = creada_en + interval '5 minutes'),
    CONSTRAINT accion_resolucion CHECK (
        (estado = 'pendiente' AND resuelta_en IS NULL)
        OR (estado <> 'pendiente' AND resuelta_en IS NOT NULL AND resuelta_en >= creada_en)),
    CONSTRAINT accion_transferencia CHECK ((estado = 'confirmada') = (transferencia_id IS NOT NULL)),
    CONSTRAINT accion_error CHECK (
        (estado = 'fallida' AND codigo_error IS NOT NULL AND length(btrim(codigo_error)) > 0)
        OR (estado <> 'fallida' AND codigo_error IS NULL)),
    CONSTRAINT accion_desafio CHECK (desafio_id IS NULL OR requiere_codigo),
    CONSTRAINT accion_confirmacion_codigo CHECK (
        estado <> 'confirmada' OR NOT requiere_codigo OR desafio_id IS NOT NULL)
);

-- Incluye pendientes vencidas aún no barridas: se deben resolver antes de preparar otra.
CREATE UNIQUE INDEX accion_pendiente_conversacion_uk
    ON asistente.accion_pendiente(conversacion_id) WHERE estado = 'pendiente';
CREATE UNIQUE INDEX accion_transferencia_uk
    ON asistente.accion_pendiente(transferencia_id) WHERE transferencia_id IS NOT NULL;
CREATE INDEX accion_expiracion_idx ON asistente.accion_pendiente(expira_en, id) WHERE estado = 'pendiente';
CREATE INDEX accion_usuario_idx ON asistente.accion_pendiente(usuario_id, creada_en DESC, id DESC);

-- +goose StatementBegin
CREATE FUNCTION asistente.validar_transicion_accion() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.estado <> 'pendiente' THEN
            RAISE EXCEPTION 'una accion debe nacer pendiente' USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;
    IF ROW(NEW.id, NEW.usuario_id, NEW.conversacion_id, NEW.tipo, NEW.destino,
           NEW.tipo_llave, NEW.nombre_mostrado, NEW.celular_enmascarado, NEW.entidad_destino,
           NEW.monto, NEW.moneda, NEW.creada_en, NEW.expira_en)
       IS DISTINCT FROM
       ROW(OLD.id, OLD.usuario_id, OLD.conversacion_id, OLD.tipo, OLD.destino,
           OLD.tipo_llave, OLD.nombre_mostrado, OLD.celular_enmascarado, OLD.entidad_destino,
           OLD.monto, OLD.moneda, OLD.creada_en, OLD.expira_en) THEN
        RAISE EXCEPTION 'datos de accion inmutables' USING ERRCODE = '23514';
    END IF;
    IF OLD.estado <> 'pendiente' THEN
        RAISE EXCEPTION 'accion ya resuelta' USING ERRCODE = '23514';
    END IF;
    IF OLD.requiere_codigo AND NOT NEW.requiere_codigo THEN
        RAISE EXCEPTION 'no se puede reducir la verificacion' USING ERRCODE = '23514';
    END IF;
    IF NEW.estado = 'pendiente' AND OLD.expira_en <= clock_timestamp() THEN
        RAISE EXCEPTION 'accion expirada' USING ERRCODE = '23514';
    END IF;
    IF NEW.estado IN ('confirmada', 'fallida') AND OLD.expira_en <= clock_timestamp() THEN
        RAISE EXCEPTION 'accion expirada' USING ERRCODE = '23514';
    END IF;
    IF NEW.estado = 'vencida' AND OLD.expira_en > clock_timestamp() THEN
        RAISE EXCEPTION 'accion aun vigente' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER accion_transicion BEFORE INSERT OR UPDATE ON asistente.accion_pendiente
    FOR EACH ROW EXECUTE FUNCTION asistente.validar_transicion_accion();
REVOKE ALL ON FUNCTION asistente.validar_transicion_accion() FROM PUBLIC;

GRANT SELECT ON asistente.accion_pendiente TO rol_asistente;
GRANT INSERT (usuario_id, conversacion_id, tipo, destino, tipo_llave, nombre_mostrado,
              celular_enmascarado, entidad_destino, monto, requiere_codigo)
    ON asistente.accion_pendiente TO rol_asistente;
GRANT UPDATE (estado, requiere_codigo, desafio_id, transferencia_id, codigo_error, resuelta_en)
    ON asistente.accion_pendiente TO rol_asistente;
REVOKE DELETE, TRUNCATE ON asistente.accion_pendiente FROM rol_asistente;

-- +goose Down
DROP TABLE asistente.accion_pendiente;
DROP FUNCTION asistente.validar_transicion_accion();
ALTER TABLE asistente.conversacion DROP CONSTRAINT conversacion_propietario_uk;

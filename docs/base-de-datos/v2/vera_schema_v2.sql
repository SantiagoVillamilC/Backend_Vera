-- Estructura de referencia; NO ejecutar sobre una BD existente. Roles y datos base: ver README.md.
--
-- PostgreSQL database dump
--

\restrict u89teAYQRWOderh30vA29coaI9s4jjPeUv5sP5BWltrfqYhXdONxAoGjb3OO8Sp

-- Dumped from database version 17.11 (Debian 17.11-1.pgdg13+2)
-- Dumped by pg_dump version 17.11 (Debian 17.11-1.pgdg13+2)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: asistente; Type: SCHEMA; Schema: -; Owner: -
--

CREATE SCHEMA asistente;


--
-- Name: auth; Type: SCHEMA; Schema: -; Owner: -
--

CREATE SCHEMA auth;


--
-- Name: finanzas; Type: SCHEMA; Schema: -; Owner: -
--

CREATE SCHEMA finanzas;


--
-- Name: nucleo; Type: SCHEMA; Schema: -; Owner: -
--

CREATE SCHEMA nucleo;


--
-- Name: producto; Type: SCHEMA; Schema: -; Owner: -
--

CREATE SCHEMA producto;


--
-- Name: public; Type: SCHEMA; Schema: -; Owner: -
--

CREATE SCHEMA IF NOT EXISTS public;
CREATE EXTENSION IF NOT EXISTS btree_gist WITH SCHEMA public;


--
-- Name: SCHEMA public; Type: COMMENT; Schema: -; Owner: -
--

COMMENT ON SCHEMA public IS 'standard public schema';


--
-- Name: cancelar_antes_de_borrar(); Type: FUNCTION; Schema: asistente; Owner: -
--

CREATE FUNCTION asistente.cancelar_antes_de_borrar() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    UPDATE asistente.accion_pendiente SET estado = 'cancelada', resuelta_en = clock_timestamp()
        WHERE conversacion_id = OLD.id AND estado = 'pendiente';
    RETURN OLD;
END;
$$;


--
-- Name: validar_origen_accion(); Type: FUNCTION; Schema: asistente; Owner: -
--

CREATE FUNCTION asistente.validar_origen_accion() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.usuario_financiero_id IS DISTINCT FROM OLD.usuario_financiero_id THEN
        RAISE EXCEPTION 'origen financiero de accion inmutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;


--
-- Name: validar_referencia_auditoria(); Type: FUNCTION; Schema: asistente; Owner: -
--

CREATE FUNCTION asistente.validar_referencia_auditoria() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    AS $$
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


--
-- Name: validar_transicion_accion(); Type: FUNCTION; Schema: asistente; Owner: -
--

CREATE FUNCTION asistente.validar_transicion_accion() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
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


--
-- Name: consentimientos_inmutable(); Type: FUNCTION; Schema: auth; Owner: -
--

CREATE FUNCTION auth.consentimientos_inmutable() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'auth.consentimientos es append-only: % no permitido', TG_OP
        USING ERRCODE = 'restrict_violation';
END $$;


--
-- Name: eventos_autenticacion_inmutable(); Type: FUNCTION; Schema: auth; Owner: -
--

CREATE FUNCTION auth.eventos_autenticacion_inmutable() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'auth.eventos_autenticacion es append-only: % no permitido', TG_OP
        USING ERRCODE = 'restrict_violation';
END $$;


--
-- Name: kyc_verificaciones_inmutable(); Type: FUNCTION; Schema: auth; Owner: -
--

CREATE FUNCTION auth.kyc_verificaciones_inmutable() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'auth.kyc_verificaciones es append-only: % no permitido', TG_OP
        USING ERRCODE = 'restrict_violation';
END $$;


--
-- Name: auditar_clasificacion(); Type: FUNCTION; Schema: finanzas; Owner: -
--

CREATE FUNCTION finanzas.auditar_clasificacion() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP='UPDATE' THEN
        IF ROW(NEW.componente_id,NEW.propietario_id) IS DISTINCT FROM ROW(OLD.componente_id,OLD.propietario_id)
           OR NEW.version<>OLD.version+1 THEN
            RAISE EXCEPTION 'Version o identidad de clasificacion invalida' USING ERRCODE='23514';
        END IF;
        IF OLD.fuente='usuario' AND NEW.fuente<>'usuario' THEN
            RAISE EXCEPTION 'La clasificacion manual tiene prioridad' USING ERRCODE='23514';
        END IF;
    ELSIF NEW.version<>1 THEN
        RAISE EXCEPTION 'Version inicial debe ser uno' USING ERRCODE='23514';
    END IF;
    INSERT INTO finanzas.cambio_clasificacion(componente_id,version,anterior,nueva)
    VALUES(NEW.componente_id,NEW.version,CASE WHEN TG_OP='UPDATE' THEN to_jsonb(OLD) END,to_jsonb(NEW));
    RETURN NEW;
END $$;


--
-- Name: validar_categoria(); Type: FUNCTION; Schema: finanzas; Owner: -
--

CREATE FUNCTION finanzas.validar_categoria() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE p finanzas.categoria;
BEGIN
    IF NEW.padre_id IS NOT NULL THEN
        SELECT * INTO STRICT p FROM finanzas.categoria WHERE id=NEW.padre_id;
        IF NEW.id=NEW.padre_id OR p.padre_id IS NOT NULL OR p.naturaleza<>NEW.naturaleza
           OR (p.propietario_id IS NOT NULL AND p.propietario_id IS DISTINCT FROM NEW.propietario_id) THEN
            RAISE EXCEPTION 'Jerarquia o propietario de categoria invalido' USING ERRCODE='23514';
        END IF;
    END IF;
    IF TG_OP='UPDATE' AND ROW(NEW.propietario_id,NEW.padre_id,NEW.naturaleza,NEW.codigo)
        IS DISTINCT FROM ROW(OLD.propietario_id,OLD.padre_id,OLD.naturaleza,OLD.codigo) THEN
        RAISE EXCEPTION 'Identidad de categoria inmutable; crear otra y archivar' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;


--
-- Name: validar_categoria_visible(); Type: FUNCTION; Schema: finanzas; Owner: -
--

CREATE FUNCTION finanzas.validar_categoria_visible() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE c finanzas.categoria;
BEGIN
    IF NEW.categoria_id IS NULL THEN RETURN NEW; END IF;
    -- Sin FOR SHARE: RLS permite leer catálogo común, pero no actualizarlo.
    -- Propietario, naturaleza y jerarquía ya son inmutables por trigger.
    SELECT * INTO STRICT c FROM finanzas.categoria WHERE id=NEW.categoria_id;
    IF (c.propietario_id IS NOT NULL AND c.propietario_id<>NEW.propietario_id) OR NOT c.activa THEN
        RAISE EXCEPTION 'Categoria ajena o archivada' USING ERRCODE='23514';
    END IF;
    IF TG_TABLE_NAME='clasificacion_movimiento' THEN
        IF c.naturaleza<>NEW.naturaleza THEN
            RAISE EXCEPTION 'Naturaleza incompatible' USING ERRCODE='23514';
        END IF;
    END IF;
    IF TG_TABLE_NAME='presupuesto' AND c.naturaleza<>'gasto' THEN
        RAISE EXCEPTION 'Presupuesto requiere categoria de gasto' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;


--
-- Name: validar_componente(); Type: FUNCTION; Schema: finanzas; Owner: -
--

CREATE FUNCTION finanzas.validar_componente() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE m finanzas.movimiento; p nucleo.posting; j nucleo.journal_entry;
BEGIN
    SELECT * INTO STRICT m FROM finanzas.movimiento WHERE id=NEW.movimiento_id AND propietario_id=NEW.propietario_id;
    SELECT * INTO STRICT p FROM nucleo.posting WHERE id=NEW.posting_id;
    SELECT * INTO STRICT j FROM nucleo.journal_entry WHERE id=p.entry_id;
    IF p.cuenta_id<>m.cuenta_id OR p.moneda<>'COP'
       OR (m.transferencia_id IS NOT NULL AND j.transferencia_id IS DISTINCT FROM m.transferencia_id)
       OR (m.entry_sin_transferencia_id IS NOT NULL AND j.id<>m.entry_sin_transferencia_id) THEN
        RAISE EXCEPTION 'Apunte ajeno al movimiento' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;


--
-- Name: validar_movimiento(); Type: FUNCTION; Schema: finanzas; Owner: -
--

CREATE FUNCTION finanzas.validar_movimiento() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE c nucleo.cuenta; j nucleo.journal_entry;
BEGIN
    SELECT * INTO STRICT c FROM nucleo.cuenta WHERE id=NEW.cuenta_id;
    IF c.titular_id IS DISTINCT FROM NEW.usuario_financiero_id
       OR c.id <> 'pasivo:usuario:'||NEW.usuario_financiero_id||':disponible' OR c.moneda<>'COP' THEN
        RAISE EXCEPTION 'Cuenta ajena o no disponible' USING ERRCODE='23514';
    END IF;
    IF NEW.entry_sin_transferencia_id IS NOT NULL THEN
        SELECT * INTO STRICT j FROM nucleo.journal_entry WHERE id=NEW.entry_sin_transferencia_id;
        IF j.transferencia_id IS NOT NULL THEN
            RAISE EXCEPTION 'El asiento pertenece a una transferencia' USING ERRCODE='23514';
        END IF;
    END IF;
    RETURN NEW;
END $$;


--
-- Name: fijar_fecha_posting(); Type: FUNCTION; Schema: nucleo; Owner: -
--

CREATE FUNCTION nucleo.fijar_fecha_posting() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    SELECT ocurrido_en INTO STRICT NEW.ocurrido_en FROM nucleo.journal_entry WHERE id = NEW.entry_id;
    RETURN NEW;
END;
$$;


--
-- Name: verificar_asiento_cuadra(); Type: FUNCTION; Schema: nucleo; Owner: -
--

CREATE FUNCTION nucleo.verificar_asiento_cuadra() RETURNS trigger
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


--
-- Name: verificar_asiento_tiene_dos_lineas(); Type: FUNCTION; Schema: nucleo; Owner: -
--

CREATE FUNCTION nucleo.verificar_asiento_tiene_dos_lineas() RETURNS trigger
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


--
-- Name: validar_cuenta_tarjeta(); Type: FUNCTION; Schema: producto; Owner: -
--

CREATE FUNCTION producto.validar_cuenta_tarjeta() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'pg_catalog'
    AS $$
BEGIN
    IF NOT EXISTS(SELECT 1 FROM nucleo.cuenta c JOIN finanzas.titular t
       ON t.usuario_financiero_id=c.titular_id
       WHERE c.id=NEW.cuenta_id AND t.parte_id=NEW.propietario_id
       AND c.id='pasivo:usuario:'||c.titular_id||':disponible' AND c.moneda='COP') THEN
        RAISE EXCEPTION 'Cuenta de tarjeta ajena o no disponible' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: accion_pendiente; Type: TABLE; Schema: asistente; Owner: -
--

CREATE TABLE asistente.accion_pendiente (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    usuario_id uuid NOT NULL,
    conversacion_id uuid NOT NULL,
    tipo text NOT NULL,
    destino text NOT NULL,
    tipo_llave text,
    nombre_mostrado text NOT NULL,
    celular_enmascarado text,
    entidad_destino text,
    monto bigint NOT NULL,
    moneda text DEFAULT 'COP'::text NOT NULL,
    requiere_codigo boolean NOT NULL,
    desafio_id uuid,
    estado text DEFAULT 'pendiente'::text NOT NULL,
    transferencia_id uuid,
    codigo_error text,
    creada_en timestamp with time zone DEFAULT now() NOT NULL,
    expira_en timestamp with time zone DEFAULT (now() + '00:05:00'::interval) NOT NULL,
    resuelta_en timestamp with time zone,
    usuario_financiero_id text NOT NULL,
    CONSTRAINT accion_confirmacion_codigo CHECK (((estado <> 'confirmada'::text) OR (NOT requiere_codigo) OR (desafio_id IS NOT NULL))),
    CONSTRAINT accion_desafio CHECK (((desafio_id IS NULL) OR requiere_codigo)),
    CONSTRAINT accion_destino_tipo CHECK ((((tipo = 'transferencia_interna'::text) AND (tipo_llave IS NULL)) OR ((tipo = 'transferencia_llave'::text) AND (tipo_llave IS NOT NULL)))),
    CONSTRAINT accion_error CHECK ((((estado = 'fallida'::text) AND (codigo_error IS NOT NULL) AND (length(btrim(codigo_error)) > 0)) OR ((estado <> 'fallida'::text) AND (codigo_error IS NULL)))),
    CONSTRAINT accion_origen_presente CHECK ((length(btrim(usuario_financiero_id)) > 0)),
    CONSTRAINT accion_pendiente_destino_check CHECK ((length(btrim(destino)) > 0)),
    CONSTRAINT accion_pendiente_estado_check CHECK ((estado = ANY (ARRAY['pendiente'::text, 'confirmada'::text, 'cancelada'::text, 'vencida'::text, 'fallida'::text]))),
    CONSTRAINT accion_pendiente_moneda_check CHECK ((moneda = 'COP'::text)),
    CONSTRAINT accion_pendiente_monto_check CHECK ((monto > 0)),
    CONSTRAINT accion_pendiente_nombre_mostrado_check CHECK ((length(btrim(nombre_mostrado)) > 0)),
    CONSTRAINT accion_pendiente_tipo_check CHECK ((tipo = ANY (ARRAY['transferencia_interna'::text, 'transferencia_llave'::text]))),
    CONSTRAINT accion_pendiente_tipo_llave_check CHECK ((tipo_llave = ANY (ARRAY['documento'::text, 'celular'::text, 'correo'::text, 'alfanumerico'::text, 'comercio'::text]))),
    CONSTRAINT accion_resolucion CHECK ((((estado = 'pendiente'::text) AND (resuelta_en IS NULL)) OR ((estado <> 'pendiente'::text) AND (resuelta_en IS NOT NULL) AND (resuelta_en >= creada_en)))),
    CONSTRAINT accion_transferencia CHECK (((estado = 'confirmada'::text) = (transferencia_id IS NOT NULL))),
    CONSTRAINT accion_vigencia CHECK ((expira_en = (creada_en + '00:05:00'::interval)))
);


--
-- Name: conversacion; Type: TABLE; Schema: asistente; Owner: -
--

CREATE TABLE asistente.conversacion (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    usuario_id uuid NOT NULL,
    titulo character varying(120),
    estado text DEFAULT 'activo'::text NOT NULL,
    cantidad_mensajes integer DEFAULT 0 NOT NULL,
    creada_en timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    ultimo_mensaje_en timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    ciclo_token uuid,
    ciclo_expira_en timestamp with time zone,
    usuario_financiero_id text NOT NULL,
    CONSTRAINT conversacion_cantidad_mensajes_check CHECK ((cantidad_mensajes >= 0)),
    CONSTRAINT conversacion_ciclo_completo CHECK (((ciclo_token IS NULL) = (ciclo_expira_en IS NULL))),
    CONSTRAINT conversacion_estado_check CHECK ((estado = ANY (ARRAY['activo'::text, 'archivado'::text]))),
    CONSTRAINT conversacion_origen_presente CHECK ((length(btrim(usuario_financiero_id)) > 0))
);


--
-- Name: ejecucion_proveedor; Type: TABLE; Schema: asistente; Owner: -
--

CREATE TABLE asistente.ejecucion_proveedor (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    turno_id uuid NOT NULL,
    usuario_id uuid NOT NULL,
    usuario_financiero_id text NOT NULL,
    conversacion_id uuid,
    mensaje_id uuid,
    operacion text NOT NULL,
    proveedor character varying(80) NOT NULL,
    modelo character varying(120) NOT NULL,
    version_instrucciones character varying(80),
    intento smallint NOT NULL,
    resultado text NOT NULL,
    codigo_proveedor character varying(80),
    reintentar_en timestamp with time zone,
    tokens_entrada bigint,
    tokens_salida bigint,
    latencia_ms bigint NOT NULL,
    primer_audio_ms bigint,
    request_id character varying(128) NOT NULL,
    iniciada_en timestamp with time zone NOT NULL,
    CONSTRAINT ejecucion_proveedor_check CHECK (((primer_audio_ms >= 0) AND (primer_audio_ms <= latencia_ms))),
    CONSTRAINT ejecucion_proveedor_intento_check CHECK ((intento > 0)),
    CONSTRAINT ejecucion_proveedor_latencia_ms_check CHECK ((latencia_ms >= 0)),
    CONSTRAINT ejecucion_proveedor_operacion_check CHECK ((operacion = ANY (ARRAY['chat'::text, 'transcripcion'::text, 'sintesis'::text]))),
    CONSTRAINT ejecucion_proveedor_resultado_check CHECK ((resultado = ANY (ARRAY['ok'::text, 'cuota'::text, 'seguridad'::text, 'timeout'::text, 'cancelada'::text, 'error'::text]))),
    CONSTRAINT ejecucion_proveedor_tokens_entrada_check CHECK ((tokens_entrada >= 0)),
    CONSTRAINT ejecucion_proveedor_tokens_salida_check CHECK ((tokens_salida >= 0))
);


--
-- Name: llamada_funcion; Type: TABLE; Schema: asistente; Owner: -
--

CREATE TABLE asistente.llamada_funcion (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    llamada_id uuid NOT NULL,
    fase text NOT NULL,
    usuario_id uuid NOT NULL,
    conversacion_id uuid NOT NULL,
    mensaje_id uuid NOT NULL,
    funcion text NOT NULL,
    argumentos_saneados jsonb NOT NULL,
    resultado character varying(40) NOT NULL,
    duracion_ms bigint NOT NULL,
    request_id character varying(128) NOT NULL,
    version_instrucciones character varying(80) NOT NULL,
    ocurrida_en timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT llamada_funcion_argumentos_saneados_check CHECK (((jsonb_typeof(argumentos_saneados) = 'object'::text) AND (octet_length((argumentos_saneados)::text) <= 2048))),
    CONSTRAINT llamada_funcion_duracion_ms_check CHECK ((duracion_ms >= 0)),
    CONSTRAINT llamada_funcion_fase_check CHECK ((fase = ANY (ARRAY['inicio'::text, 'fin'::text]))),
    CONSTRAINT llamada_funcion_funcion_check CHECK ((funcion = ANY (ARRAY['consultar_saldo'::text, 'consultar_limites'::text, 'listar_movimientos'::text, 'resumir_movimientos'::text, 'buscar_destinatario'::text, 'consultar_transferencia'::text, 'preparar_transferencia'::text, 'desconocida'::text]))),
    CONSTRAINT llamada_funcion_resultado_check CHECK (((resultado)::text ~ '^[A-Za-z][A-Za-z0-9]{0,39}$'::text)),
    CONSTRAINT llamada_inicio CHECK (((fase <> 'inicio'::text) OR (((resultado)::text = 'InProgress'::text) AND (duracion_ms = 0))))
);


--
-- Name: mensaje; Type: TABLE; Schema: asistente; Owner: -
--

CREATE TABLE asistente.mensaje (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    conversacion_id uuid NOT NULL,
    numero_orden integer NOT NULL,
    rol text NOT NULL,
    canal text NOT NULL,
    contenido text NOT NULL,
    nombre_modelo character varying(120),
    version_instrucciones text,
    tokens_entrada integer,
    tokens_salida integer,
    latencia_ms integer,
    metadatos jsonb DEFAULT '{}'::jsonb NOT NULL,
    respuesta_a uuid,
    creada_en timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    accion_id uuid,
    CONSTRAINT mensaje_accion_final CHECK (((accion_id IS NULL) OR ((rol = 'asistente'::text) AND (respuesta_a IS NOT NULL)))),
    CONSTRAINT mensaje_canal_check CHECK ((canal = ANY (ARRAY['texto'::text, 'voz'::text]))),
    CONSTRAINT mensaje_contenido_check CHECK ((length(btrim(contenido)) > 0)),
    CONSTRAINT mensaje_latencia_ms_check CHECK ((latencia_ms >= 0)),
    CONSTRAINT mensaje_metadatos_check CHECK ((jsonb_typeof(metadatos) = 'object'::text)),
    CONSTRAINT mensaje_numero_orden_check CHECK ((numero_orden > 0)),
    CONSTRAINT mensaje_respuesta_rol CHECK (((respuesta_a IS NULL) OR (rol = 'asistente'::text))),
    CONSTRAINT mensaje_rol_check CHECK ((rol = ANY (ARRAY['usuario'::text, 'asistente'::text, 'herramienta'::text]))),
    CONSTRAINT mensaje_tokens_entrada_check CHECK ((tokens_entrada >= 0)),
    CONSTRAINT mensaje_tokens_salida_check CHECK ((tokens_salida >= 0))
);


--
-- Name: solicitud_texto; Type: TABLE; Schema: asistente; Owner: -
--

CREATE TABLE asistente.solicitud_texto (
    usuario_id uuid NOT NULL,
    clave uuid NOT NULL,
    conversacion_id uuid NOT NULL,
    mensaje_id uuid NOT NULL,
    huella bytea NOT NULL,
    creada_en timestamp with time zone DEFAULT clock_timestamp() NOT NULL,
    CONSTRAINT solicitud_texto_huella_check CHECK ((octet_length(huella) = 32))
);


--
-- Name: codigos_respaldo; Type: TABLE; Schema: auth; Owner: -
--

CREATE TABLE auth.codigos_respaldo (
    id uuid NOT NULL,
    credencial_id uuid NOT NULL,
    codigo_hmac bytea NOT NULL,
    usado_en timestamp with time zone,
    creado_en timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: consentimientos; Type: TABLE; Schema: auth; Owner: -
--

CREATE TABLE auth.consentimientos (
    id uuid NOT NULL,
    parte_id uuid NOT NULL,
    tipo text NOT NULL,
    version_texto text NOT NULL,
    texto text NOT NULL,
    aceptado boolean NOT NULL,
    accion text NOT NULL,
    actor uuid,
    origen text NOT NULL,
    user_agent text DEFAULT ''::text NOT NULL,
    metodo text NOT NULL,
    ocurrido_en timestamp with time zone DEFAULT now() NOT NULL,
    orden bigint NOT NULL,
    CONSTRAINT consentimientos_tipo_check CHECK ((tipo = ANY (ARRAY['tratamiento_datos'::text, 'whatsapp_optin'::text, 'biometricos'::text, 'asistente_ia'::text])))
);


--
-- Name: consentimientos_orden_seq; Type: SEQUENCE; Schema: auth; Owner: -
--

ALTER TABLE auth.consentimientos ALTER COLUMN orden ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME auth.consentimientos_orden_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: credenciales; Type: TABLE; Schema: auth; Owner: -
--

CREATE TABLE auth.credenciales (
    id uuid NOT NULL,
    parte_id uuid NOT NULL,
    correo text NOT NULL,
    celular text NOT NULL,
    celular_verificado_en timestamp with time zone,
    correo_verificado_en timestamp with time zone,
    celular_cambiado_en timestamp with time zone,
    valor_derivado text,
    estado text NOT NULL,
    creada_en timestamp with time zone DEFAULT now() NOT NULL,
    actualizada_en timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT credenciales_estado_check CHECK ((estado = ANY (ARRAY['pendiente_verificacion'::text, 'activo'::text, 'suspendido'::text, 'cerrado'::text, 'sin_login'::text]))),
    CONSTRAINT credenciales_valor_derivado_requerido CHECK (((estado = 'sin_login'::text) OR (valor_derivado IS NOT NULL)))
);


--
-- Name: desafios; Type: TABLE; Schema: auth; Owner: -
--

CREATE TABLE auth.desafios (
    id uuid NOT NULL,
    parte_id uuid,
    credencial_id uuid,
    proposito text NOT NULL,
    celular_destino text NOT NULL,
    codigo_hmac bytea NOT NULL,
    intentos integer DEFAULT 0 NOT NULL,
    reenvios integer DEFAULT 0 NOT NULL,
    estado text NOT NULL,
    creado_en timestamp with time zone NOT NULL,
    expira_en timestamp with time zone NOT NULL,
    ultimo_envio_en timestamp with time zone NOT NULL,
    contexto_solicitado text,
    celular_nuevo text,
    desafio_padre_id uuid,
    CONSTRAINT desafios_estado_check CHECK ((estado = ANY (ARRAY['pendiente'::text, 'verificado'::text, 'invalidado'::text, 'expirado'::text]))),
    CONSTRAINT desafios_proposito_check CHECK ((proposito = ANY (ARRAY['registro'::text, 'login'::text, 'recuperacion'::text, 'cambio_numero'::text, 'cambio_numero_viejo'::text, 'confirmacion_transferencia'::text])))
);


--
-- Name: empresas; Type: TABLE; Schema: auth; Owner: -
--

CREATE TABLE auth.empresas (
    parte_id uuid NOT NULL,
    nit text NOT NULL,
    razon_social text NOT NULL
);


--
-- Name: eventos_autenticacion; Type: TABLE; Schema: auth; Owner: -
--

CREATE TABLE auth.eventos_autenticacion (
    id bigint NOT NULL,
    tipo text NOT NULL,
    actor_parte_id uuid,
    credencial_id uuid,
    resultado text NOT NULL,
    ocurrido_en timestamp with time zone DEFAULT now() NOT NULL,
    origen text NOT NULL,
    contexto text DEFAULT ''::text NOT NULL,
    sesion_id uuid,
    detalle jsonb DEFAULT '{}'::jsonb NOT NULL
);


--
-- Name: eventos_autenticacion_id_seq; Type: SEQUENCE; Schema: auth; Owner: -
--

ALTER TABLE auth.eventos_autenticacion ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME auth.eventos_autenticacion_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: kyc_estados; Type: TABLE; Schema: auth; Owner: -
--

CREATE TABLE auth.kyc_estados (
    parte_id uuid NOT NULL,
    producto text NOT NULL,
    estado_kyc text NOT NULL,
    actualizado_en timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT kyc_estados_producto_check CHECK ((producto = ANY (ARRAY['bajo_monto'::text, 'pleno'::text, 'deposito_ordinario'::text])))
);


--
-- Name: kyc_verificaciones; Type: TABLE; Schema: auth; Owner: -
--

CREATE TABLE auth.kyc_verificaciones (
    id bigint NOT NULL,
    parte_id uuid NOT NULL,
    nivel_solicitado text NOT NULL,
    medio text NOT NULL,
    proveedor text,
    resultado text NOT NULL,
    referencia_proveedor text,
    ocurrida_en timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT kyc_verificaciones_nivel_solicitado_check CHECK ((nivel_solicitado = ANY (ARRAY['bajo_monto'::text, 'pleno'::text])))
);


--
-- Name: kyc_verificaciones_id_seq; Type: SEQUENCE; Schema: auth; Owner: -
--

ALTER TABLE auth.kyc_verificaciones ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME auth.kyc_verificaciones_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: outbox; Type: TABLE; Schema: auth; Owner: -
--

CREATE TABLE auth.outbox (
    id bigint NOT NULL,
    tipo text NOT NULL,
    payload jsonb NOT NULL,
    disponible_en timestamp with time zone DEFAULT now() NOT NULL,
    intentos integer DEFAULT 0 NOT NULL,
    ultimo_error text,
    despachado_en timestamp with time zone,
    id_mensaje_proveedor text
);


--
-- Name: outbox_id_seq; Type: SEQUENCE; Schema: auth; Owner: -
--

ALTER TABLE auth.outbox ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME auth.outbox_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: partes; Type: TABLE; Schema: auth; Owner: -
--

CREATE TABLE auth.partes (
    id uuid NOT NULL,
    tipo text NOT NULL,
    creada_en timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT partes_tipo_check CHECK ((tipo = ANY (ARRAY['persona'::text, 'empresa'::text])))
);


--
-- Name: personas; Type: TABLE; Schema: auth; Owner: -
--

CREATE TABLE auth.personas (
    parte_id uuid NOT NULL,
    tipo_documento text NOT NULL,
    documento text NOT NULL,
    nombre text NOT NULL,
    perfil_nombre text,
    nacida_en date,
    CONSTRAINT personas_tipo_documento_check CHECK ((tipo_documento = ANY (ARRAY['CC'::text, 'CE'::text, 'PA'::text])))
);


--
-- Name: roles_de_parte; Type: TABLE; Schema: auth; Owner: -
--

CREATE TABLE auth.roles_de_parte (
    id bigint NOT NULL,
    parte_id uuid NOT NULL,
    rol text NOT NULL,
    representada_id uuid NOT NULL,
    creado_en timestamp with time zone DEFAULT now() NOT NULL,
    revocado_en timestamp with time zone,
    CONSTRAINT roles_de_parte_rol_check CHECK ((rol = ANY (ARRAY['representante_legal'::text, 'administrador'::text, 'beneficiario_final'::text])))
);


--
-- Name: roles_de_parte_id_seq; Type: SEQUENCE; Schema: auth; Owner: -
--

ALTER TABLE auth.roles_de_parte ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME auth.roles_de_parte_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: sesiones; Type: TABLE; Schema: auth; Owner: -
--

CREATE TABLE auth.sesiones (
    id uuid NOT NULL,
    familia_id uuid NOT NULL,
    parte_id uuid NOT NULL,
    contexto_activo text DEFAULT ''::text NOT NULL,
    dispositivo text DEFAULT ''::text NOT NULL,
    refresh_hash bytea NOT NULL,
    creada_en timestamp with time zone DEFAULT now() NOT NULL,
    ultimo_uso_en timestamp with time zone DEFAULT now() NOT NULL,
    usado_en timestamp with time zone,
    revocada_en timestamp with time zone,
    vence_en timestamp with time zone NOT NULL
);


--
-- Name: textos_consentimiento; Type: TABLE; Schema: auth; Owner: -
--

CREATE TABLE auth.textos_consentimiento (
    tipo text NOT NULL,
    version text NOT NULL,
    texto text NOT NULL,
    vigente_desde timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT textos_consentimiento_tipo_check CHECK ((tipo = ANY (ARRAY['tratamiento_datos'::text, 'whatsapp_optin'::text, 'biometricos'::text, 'asistente_ia'::text])))
);


--
-- Name: tokens_restablecimiento; Type: TABLE; Schema: auth; Owner: -
--

CREATE TABLE auth.tokens_restablecimiento (
    id uuid NOT NULL,
    credencial_id uuid NOT NULL,
    token_hash bytea NOT NULL,
    expira_en timestamp with time zone NOT NULL,
    usado_en timestamp with time zone,
    creado_en timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: transiciones_credencial; Type: TABLE; Schema: auth; Owner: -
--

CREATE TABLE auth.transiciones_credencial (
    id bigint NOT NULL,
    credencial_id uuid NOT NULL,
    estado_anterior text,
    estado_nuevo text NOT NULL,
    motivo text,
    ocurrida_en timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: transiciones_credencial_id_seq; Type: SEQUENCE; Schema: auth; Owner: -
--

ALTER TABLE auth.transiciones_credencial ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME auth.transiciones_credencial_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: cambio_clasificacion; Type: TABLE; Schema: finanzas; Owner: -
--

CREATE TABLE finanzas.cambio_clasificacion (
    id bigint NOT NULL,
    componente_id uuid NOT NULL,
    version integer NOT NULL,
    anterior jsonb,
    nueva jsonb NOT NULL,
    ocurrida_en timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: cambio_clasificacion_id_seq; Type: SEQUENCE; Schema: finanzas; Owner: -
--

ALTER TABLE finanzas.cambio_clasificacion ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME finanzas.cambio_clasificacion_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: categoria; Type: TABLE; Schema: finanzas; Owner: -
--

CREATE TABLE finanzas.categoria (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    propietario_id uuid,
    codigo character varying(60) NOT NULL,
    nombre character varying(80) NOT NULL,
    padre_id uuid,
    naturaleza text NOT NULL,
    icono character varying(60),
    color character varying(7),
    activa boolean DEFAULT true NOT NULL,
    creada_en timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT categoria_color_check CHECK (((color)::text ~ '^#[0-9A-Fa-f]{6}$'::text)),
    CONSTRAINT categoria_naturaleza_check CHECK ((naturaleza = ANY (ARRAY['ingreso'::text, 'gasto'::text, 'traslado'::text, 'ajuste'::text]))),
    CONSTRAINT categoria_nombre_check CHECK ((length(btrim((nombre)::text)) > 0))
);


--
-- Name: clasificacion_movimiento; Type: TABLE; Schema: finanzas; Owner: -
--

CREATE TABLE finanzas.clasificacion_movimiento (
    componente_id uuid NOT NULL,
    propietario_id uuid NOT NULL,
    categoria_id uuid,
    naturaleza text DEFAULT 'pendiente'::text NOT NULL,
    fuente text NOT NULL,
    confianza_ppm integer,
    modelo character varying(120),
    version_clasificador character varying(80),
    version integer DEFAULT 1 NOT NULL,
    actor_id uuid,
    actualizada_en timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT clasificacion_movimiento_check CHECK (((naturaleza = 'pendiente'::text) = (categoria_id IS NULL))),
    CONSTRAINT clasificacion_movimiento_check1 CHECK (((fuente <> 'usuario'::text) OR (actor_id IS NOT NULL))),
    CONSTRAINT clasificacion_movimiento_check2 CHECK (((fuente <> 'ia'::text) OR ((modelo IS NOT NULL) AND (version_clasificador IS NOT NULL) AND (confianza_ppm IS NOT NULL)))),
    CONSTRAINT clasificacion_movimiento_confianza_ppm_check CHECK (((confianza_ppm >= 0) AND (confianza_ppm <= 1000000))),
    CONSTRAINT clasificacion_movimiento_fuente_check CHECK ((fuente = ANY (ARRAY['usuario'::text, 'regla'::text, 'ia'::text, 'sistema'::text]))),
    CONSTRAINT clasificacion_movimiento_naturaleza_check CHECK ((naturaleza = ANY (ARRAY['ingreso'::text, 'gasto'::text, 'traslado'::text, 'ajuste'::text, 'pendiente'::text]))),
    CONSTRAINT clasificacion_movimiento_version_check CHECK ((version > 0))
);


--
-- Name: componente_movimiento; Type: TABLE; Schema: finanzas; Owner: -
--

CREATE TABLE finanzas.componente_movimiento (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    movimiento_id uuid NOT NULL,
    propietario_id uuid NOT NULL,
    posting_id bigint NOT NULL,
    motivo text NOT NULL,
    CONSTRAINT componente_movimiento_motivo_check CHECK ((motivo = ANY (ARRAY['deposito'::text, 'transferencia_recibida'::text, 'envio'::text, 'gravamen'::text, 'devolucion'::text, 'devolucion_gravamen'::text])))
);


--
-- Name: movimiento; Type: TABLE; Schema: finanzas; Owner: -
--

CREATE TABLE finanzas.movimiento (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    propietario_id uuid NOT NULL,
    usuario_financiero_id text NOT NULL,
    cuenta_id text NOT NULL,
    transferencia_id uuid,
    entry_sin_transferencia_id bigint,
    ocurrido_en timestamp with time zone NOT NULL,
    version_proyeccion integer DEFAULT 1 NOT NULL,
    proyectado_en timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT movimiento_check CHECK ((num_nonnulls(transferencia_id, entry_sin_transferencia_id) = 1)),
    CONSTRAINT movimiento_version_proyeccion_check CHECK ((version_proyeccion > 0))
);


--
-- Name: periodo_presupuesto; Type: TABLE; Schema: finanzas; Owner: -
--

CREATE TABLE finanzas.periodo_presupuesto (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    presupuesto_id uuid NOT NULL,
    propietario_id uuid NOT NULL,
    desde date NOT NULL,
    hasta_exclusivo date NOT NULL,
    limite_centavos bigint NOT NULL,
    umbral_porcentaje smallint DEFAULT 80 NOT NULL,
    CONSTRAINT periodo_presupuesto_check CHECK ((desde < hasta_exclusivo)),
    CONSTRAINT periodo_presupuesto_limite_centavos_check CHECK ((limite_centavos > 0)),
    CONSTRAINT periodo_presupuesto_umbral_porcentaje_check CHECK (((umbral_porcentaje >= 1) AND (umbral_porcentaje <= 100)))
);


--
-- Name: presupuesto; Type: TABLE; Schema: finanzas; Owner: -
--

CREATE TABLE finanzas.presupuesto (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    propietario_id uuid NOT NULL,
    categoria_id uuid,
    nombre character varying(80) NOT NULL,
    periodicidad text NOT NULL,
    zona_horaria text DEFAULT 'America/Bogota'::text NOT NULL,
    activo boolean DEFAULT true NOT NULL,
    creado_en timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT presupuesto_periodicidad_check CHECK ((periodicidad = ANY (ARRAY['semanal'::text, 'mensual'::text, 'anual'::text, 'unico'::text]))),
    CONSTRAINT presupuesto_zona_horaria_check CHECK ((zona_horaria = 'America/Bogota'::text))
);


--
-- Name: regla_clasificacion; Type: TABLE; Schema: finanzas; Owner: -
--

CREATE TABLE finanzas.regla_clasificacion (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    propietario_id uuid NOT NULL,
    contraparte_hmac bytea NOT NULL,
    version_clave integer NOT NULL,
    motivo text NOT NULL,
    categoria_id uuid NOT NULL,
    activa boolean DEFAULT true NOT NULL,
    creada_en timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT regla_clasificacion_contraparte_hmac_check CHECK ((octet_length(contraparte_hmac) = 32)),
    CONSTRAINT regla_clasificacion_motivo_check CHECK ((motivo = ANY (ARRAY['deposito'::text, 'transferencia_recibida'::text, 'envio'::text, 'gravamen'::text, 'devolucion'::text, 'devolucion_gravamen'::text]))),
    CONSTRAINT regla_clasificacion_version_clave_check CHECK ((version_clave > 0))
);


--
-- Name: titular; Type: TABLE; Schema: finanzas; Owner: -
--

CREATE TABLE finanzas.titular (
    usuario_financiero_id text NOT NULL,
    parte_id uuid NOT NULL,
    creada_en timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: conciliacion_corrida; Type: TABLE; Schema: nucleo; Owner: -
--

CREATE TABLE nucleo.conciliacion_corrida (
    id bigint NOT NULL,
    ventana_hasta timestamp with time zone NOT NULL,
    corrida_en timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: conciliacion_corrida_id_seq; Type: SEQUENCE; Schema: nucleo; Owner: -
--

ALTER TABLE nucleo.conciliacion_corrida ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME nucleo.conciliacion_corrida_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: cuenta; Type: TABLE; Schema: nucleo; Owner: -
--

CREATE TABLE nucleo.cuenta (
    id text NOT NULL,
    naturaleza text NOT NULL,
    clase text NOT NULL,
    titular_id text,
    moneda character(3) DEFAULT 'COP'::bpchar NOT NULL,
    creada_en timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT cuenta_naturaleza_check CHECK ((naturaleza = ANY (ARRAY['activo'::text, 'pasivo'::text])))
);


--
-- Name: deposito; Type: TABLE; Schema: nucleo; Owner: -
--

CREATE TABLE nucleo.deposito (
    id uuid NOT NULL,
    usuario_id text NOT NULL,
    monto bigint NOT NULL,
    moneda character(3) DEFAULT 'COP'::bpchar NOT NULL,
    referencia text NOT NULL,
    idempotency_key text NOT NULL,
    cus text,
    estado text DEFAULT 'pendiente'::text NOT NULL,
    creada_en timestamp with time zone DEFAULT now() NOT NULL,
    actualizada_en timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT deposito_estado_check CHECK ((estado = ANY (ARRAY['pendiente'::text, 'confirmada'::text, 'rechazada'::text, 'expirada'::text]))),
    CONSTRAINT deposito_monto_check CHECK ((monto > 0))
);


--
-- Name: descuadre; Type: TABLE; Schema: nucleo; Owner: -
--

CREATE TABLE nucleo.descuadre (
    id bigint NOT NULL,
    tipo text NOT NULL,
    transferencia_id uuid,
    referencia_externa text,
    monto_interno bigint,
    monto_externo bigint,
    ventana_desde timestamp with time zone NOT NULL,
    ventana_hasta timestamp with time zone NOT NULL,
    detectado_en timestamp with time zone DEFAULT now() NOT NULL,
    resuelto_en timestamp with time zone,
    resolucion text,
    CONSTRAINT descuadre_identidad_check CHECK ((num_nonnulls(transferencia_id, referencia_externa) = 1))
);


--
-- Name: descuadre_id_seq; Type: SEQUENCE; Schema: nucleo; Owner: -
--

ALTER TABLE nucleo.descuadre ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME nucleo.descuadre_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: detalle_transferencia; Type: TABLE; Schema: nucleo; Owner: -
--

CREATE TABLE nucleo.detalle_transferencia (
    transferencia_id uuid NOT NULL,
    actor_id uuid,
    canal text NOT NULL,
    tipo_destino text NOT NULL,
    tipo_llave text,
    destinatario_id uuid,
    nombre_enmascarado text,
    entidad_destino text,
    referencia_externa text,
    request_id character varying(128),
    accion_ia_id uuid,
    confirmada_por_usuario_en timestamp with time zone,
    creada_en timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT detalle_transferencia_canal_check CHECK ((canal = ANY (ARRAY['app'::text, 'asistente'::text, 'api'::text, 'legado'::text]))),
    CONSTRAINT detalle_transferencia_check CHECK (((tipo_destino = 'bre_b'::text) = (tipo_llave IS NOT NULL))),
    CONSTRAINT detalle_transferencia_check1 CHECK (((tipo_destino <> 'interna'::text) OR (destinatario_id IS NOT NULL))),
    CONSTRAINT detalle_transferencia_tipo_destino_check CHECK ((tipo_destino = ANY (ARRAY['interna'::text, 'bre_b'::text, 'desconocido'::text]))),
    CONSTRAINT detalle_transferencia_tipo_llave_check CHECK ((tipo_llave = ANY (ARRAY['documento'::text, 'celular'::text, 'correo'::text, 'alfanumerico'::text, 'comercio'::text])))
);


--
-- Name: evento_custodio; Type: TABLE; Schema: nucleo; Owner: -
--

CREATE TABLE nucleo.evento_custodio (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    proveedor text NOT NULL,
    evento_externo_id text NOT NULL,
    deposito_id uuid,
    transferencia_id uuid,
    huella bytea NOT NULL,
    resultado text NOT NULL,
    codigo_resultado character varying(80),
    ocurrido_en timestamp with time zone NOT NULL,
    recibido_en timestamp with time zone DEFAULT now() NOT NULL,
    procesado_en timestamp with time zone,
    CONSTRAINT evento_custodio_check CHECK ((num_nonnulls(deposito_id, transferencia_id) = 1)),
    CONSTRAINT evento_custodio_check1 CHECK (((resultado = 'recibido'::text) = (procesado_en IS NULL))),
    CONSTRAINT evento_custodio_huella_check CHECK ((octet_length(huella) = 32)),
    CONSTRAINT evento_custodio_resultado_check CHECK ((resultado = ANY (ARRAY['recibido'::text, 'procesado'::text, 'rechazado'::text])))
);


--
-- Name: evento_transferencia; Type: TABLE; Schema: nucleo; Owner: -
--

CREATE TABLE nucleo.evento_transferencia (
    id bigint NOT NULL,
    transferencia_id uuid NOT NULL,
    estado_anterior text,
    estado_nuevo text NOT NULL,
    codigo_resultado character varying(80),
    request_id character varying(128),
    ocurrido_en timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT evento_transferencia_estado_nuevo_check CHECK ((estado_nuevo = ANY (ARRAY['creada'::text, 'reservada'::text, 'despachada'::text, 'confirmada'::text, 'fallida'::text, 'indeterminada'::text, 'compensada'::text])))
);


--
-- Name: evento_transferencia_id_seq; Type: SEQUENCE; Schema: nucleo; Owner: -
--

ALTER TABLE nucleo.evento_transferencia ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME nucleo.evento_transferencia_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: extracto_custodio; Type: TABLE; Schema: nucleo; Owner: -
--

CREATE TABLE nucleo.extracto_custodio (
    corte_en timestamp with time zone NOT NULL,
    saldo bigint NOT NULL,
    origen text NOT NULL
);


--
-- Name: journal_entry; Type: TABLE; Schema: nucleo; Owner: -
--

CREATE TABLE nucleo.journal_entry (
    id bigint NOT NULL,
    tipo text NOT NULL,
    transferencia_id uuid,
    ocurrido_en timestamp with time zone NOT NULL,
    registrado_en timestamp with time zone DEFAULT now() NOT NULL,
    descripcion text,
    deposito_id uuid,
    CONSTRAINT journal_entry_origen_unico CHECK ((num_nonnulls(transferencia_id, deposito_id) <= 1))
);


--
-- Name: journal_entry_id_seq; Type: SEQUENCE; Schema: nucleo; Owner: -
--

ALTER TABLE nucleo.journal_entry ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME nucleo.journal_entry_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: outbox; Type: TABLE; Schema: nucleo; Owner: -
--

CREATE TABLE nucleo.outbox (
    id bigint NOT NULL,
    tipo text NOT NULL,
    payload jsonb NOT NULL,
    disponible_en timestamp with time zone DEFAULT now() NOT NULL,
    intentos integer DEFAULT 0 NOT NULL,
    ultimo_error text,
    despachado_en timestamp with time zone
);


--
-- Name: outbox_id_seq; Type: SEQUENCE; Schema: nucleo; Owner: -
--

ALTER TABLE nucleo.outbox ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME nucleo.outbox_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: posting; Type: TABLE; Schema: nucleo; Owner: -
--

CREATE TABLE nucleo.posting (
    id bigint NOT NULL,
    entry_id bigint NOT NULL,
    cuenta_id text NOT NULL,
    monto bigint NOT NULL,
    moneda character(3) NOT NULL,
    ocurrido_en timestamp with time zone NOT NULL,
    CONSTRAINT posting_monto_check CHECK ((monto <> 0))
);


--
-- Name: posting_id_seq; Type: SEQUENCE; Schema: nucleo; Owner: -
--

ALTER TABLE nucleo.posting ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME nucleo.posting_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: regla_limite; Type: TABLE; Schema: nucleo; Owner: -
--

CREATE TABLE nucleo.regla_limite (
    id text NOT NULL,
    nivel_kyc text NOT NULL,
    ventana text NOT NULL,
    tope_uvt_centesimas bigint NOT NULL,
    conteo_maximo integer,
    vigente_desde date NOT NULL,
    vigente_hasta date,
    fuente text NOT NULL,
    tipo text DEFAULT 'rechaza'::text NOT NULL,
    tarifa_ppm bigint DEFAULT 0 NOT NULL,
    CONSTRAINT regla_limite_tarifa_coherente CHECK ((((tipo = 'rechaza'::text) AND (tarifa_ppm = 0)) OR ((tipo = 'grava'::text) AND (tarifa_ppm > 0)))),
    CONSTRAINT regla_limite_tipo_valido CHECK ((tipo = ANY (ARRAY['rechaza'::text, 'grava'::text]))),
    CONSTRAINT regla_limite_tope_uvt_centesimas_check CHECK ((tope_uvt_centesimas > 0)),
    CONSTRAINT regla_limite_ventana_check CHECK ((ventana = ANY (ARRAY['operacion'::text, 'diaria'::text, 'mensual'::text]))),
    CONSTRAINT regla_limite_vigencia_coherente CHECK (((vigente_hasta IS NULL) OR (vigente_hasta > vigente_desde)))
);


--
-- Name: transferencia; Type: TABLE; Schema: nucleo; Owner: -
--

CREATE TABLE nucleo.transferencia (
    id uuid NOT NULL,
    usuario_id text NOT NULL,
    cuenta_origen text NOT NULL,
    llave_destino text NOT NULL,
    monto bigint NOT NULL,
    moneda character(3) DEFAULT 'COP'::bpchar NOT NULL,
    idempotency_key text NOT NULL,
    estado text NOT NULL,
    creada_en timestamp with time zone DEFAULT now() NOT NULL,
    actualizada_en timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT transferencia_estado_check CHECK ((estado = ANY (ARRAY['creada'::text, 'reservada'::text, 'despachada'::text, 'confirmada'::text, 'fallida'::text, 'indeterminada'::text, 'compensada'::text]))),
    CONSTRAINT transferencia_monto_check CHECK ((monto > 0))
);


--
-- Name: valor_uvt; Type: TABLE; Schema: nucleo; Owner: -
--

CREATE TABLE nucleo.valor_uvt (
    anio integer NOT NULL,
    valor_centavos bigint NOT NULL,
    fuente text NOT NULL,
    CONSTRAINT valor_uvt_valor_centavos_check CHECK ((valor_centavos > 0))
);


--
-- Name: destinatario_favorito; Type: TABLE; Schema: producto; Owner: -
--

CREATE TABLE producto.destinatario_favorito (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    propietario_id uuid NOT NULL,
    destinatario_id uuid,
    alias character varying(80) NOT NULL,
    tipo_llave text,
    llave_cifrada bytea,
    llave_hmac bytea,
    version_clave integer,
    creado_en timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT destinatario_favorito_check CHECK ((((destinatario_id IS NOT NULL) AND (tipo_llave IS NULL) AND (llave_cifrada IS NULL) AND (llave_hmac IS NULL) AND (version_clave IS NULL)) OR ((destinatario_id IS NULL) AND (tipo_llave IS NOT NULL) AND (llave_cifrada IS NOT NULL) AND (llave_hmac IS NOT NULL) AND (version_clave IS NOT NULL) AND (octet_length(llave_hmac) = 32) AND (version_clave > 0)))),
    CONSTRAINT destinatario_favorito_tipo_llave_check CHECK ((tipo_llave = ANY (ARRAY['documento'::text, 'celular'::text, 'correo'::text, 'alfanumerico'::text, 'comercio'::text])))
);


--
-- Name: notificacion; Type: TABLE; Schema: producto; Owner: -
--

CREATE TABLE producto.notificacion (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    propietario_id uuid NOT NULL,
    tipo text NOT NULL,
    clave_deduplicacion text NOT NULL,
    referencia_id text NOT NULL,
    creada_en timestamp with time zone DEFAULT now() NOT NULL,
    leida_en timestamp with time zone,
    CONSTRAINT notificacion_tipo_check CHECK ((tipo = ANY (ARRAY['transferencia'::text, 'deposito'::text, 'presupuesto'::text, 'seguridad'::text])))
);


--
-- Name: preferencia; Type: TABLE; Schema: producto; Owner: -
--

CREATE TABLE producto.preferencia (
    parte_id uuid NOT NULL,
    idioma text DEFAULT 'es-CO'::text NOT NULL,
    zona_horaria text DEFAULT 'America/Bogota'::text NOT NULL,
    voz_preferida character varying(80),
    leer_respuestas boolean DEFAULT false NOT NULL,
    avisos_presupuesto boolean DEFAULT true NOT NULL,
    actualizada_en timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: tarjeta; Type: TABLE; Schema: producto; Owner: -
--

CREATE TABLE producto.tarjeta (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    propietario_id uuid NOT NULL,
    cuenta_id text NOT NULL,
    proveedor text NOT NULL,
    referencia_proveedor text NOT NULL,
    ultimos_cuatro character(4) NOT NULL,
    marca character varying(40) NOT NULL,
    tipo text NOT NULL,
    estado text NOT NULL,
    vence_mes smallint NOT NULL,
    vence_anio smallint NOT NULL,
    creada_en timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tarjeta_estado_check CHECK ((estado = ANY (ARRAY['solicitada'::text, 'activa'::text, 'bloqueada'::text, 'cancelada'::text, 'vencida'::text]))),
    CONSTRAINT tarjeta_tipo_check CHECK ((tipo = ANY (ARRAY['fisica'::text, 'virtual'::text]))),
    CONSTRAINT tarjeta_ultimos_cuatro_check CHECK ((ultimos_cuatro ~ '^[0-9]{4}$'::text)),
    CONSTRAINT tarjeta_vence_anio_check CHECK (((vence_anio >= 2000) AND (vence_anio <= 9999))),
    CONSTRAINT tarjeta_vence_mes_check CHECK (((vence_mes >= 1) AND (vence_mes <= 12)))
);


--
-- Name: users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.users (
    id uuid NOT NULL,
    email text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: accion_pendiente accion_conversacion_id_uk; Type: CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.accion_pendiente
    ADD CONSTRAINT accion_conversacion_id_uk UNIQUE (conversacion_id, id);


--
-- Name: accion_pendiente accion_pendiente_pkey; Type: CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.accion_pendiente
    ADD CONSTRAINT accion_pendiente_pkey PRIMARY KEY (id);


--
-- Name: conversacion conversacion_pkey; Type: CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.conversacion
    ADD CONSTRAINT conversacion_pkey PRIMARY KEY (id);


--
-- Name: conversacion conversacion_propietario_uk; Type: CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.conversacion
    ADD CONSTRAINT conversacion_propietario_uk UNIQUE (id, usuario_id);


--
-- Name: ejecucion_proveedor ejecucion_proveedor_pkey; Type: CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.ejecucion_proveedor
    ADD CONSTRAINT ejecucion_proveedor_pkey PRIMARY KEY (id);


--
-- Name: ejecucion_proveedor ejecucion_proveedor_turno_id_operacion_intento_key; Type: CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.ejecucion_proveedor
    ADD CONSTRAINT ejecucion_proveedor_turno_id_operacion_intento_key UNIQUE (turno_id, operacion, intento);


--
-- Name: llamada_funcion llamada_fase_uk; Type: CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.llamada_funcion
    ADD CONSTRAINT llamada_fase_uk UNIQUE (llamada_id, fase);


--
-- Name: llamada_funcion llamada_funcion_pkey; Type: CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.llamada_funcion
    ADD CONSTRAINT llamada_funcion_pkey PRIMARY KEY (id);


--
-- Name: mensaje mensaje_conversacion_id_uk; Type: CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.mensaje
    ADD CONSTRAINT mensaje_conversacion_id_uk UNIQUE (conversacion_id, id);


--
-- Name: mensaje mensaje_orden_uk; Type: CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.mensaje
    ADD CONSTRAINT mensaje_orden_uk UNIQUE (conversacion_id, numero_orden);


--
-- Name: mensaje mensaje_pkey; Type: CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.mensaje
    ADD CONSTRAINT mensaje_pkey PRIMARY KEY (id);


--
-- Name: solicitud_texto solicitud_texto_pkey; Type: CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.solicitud_texto
    ADD CONSTRAINT solicitud_texto_pkey PRIMARY KEY (usuario_id, clave);


--
-- Name: codigos_respaldo codigos_respaldo_pkey; Type: CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.codigos_respaldo
    ADD CONSTRAINT codigos_respaldo_pkey PRIMARY KEY (id);


--
-- Name: consentimientos consentimientos_pkey; Type: CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.consentimientos
    ADD CONSTRAINT consentimientos_pkey PRIMARY KEY (id);


--
-- Name: credenciales credenciales_parte_id_uk; Type: CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.credenciales
    ADD CONSTRAINT credenciales_parte_id_uk UNIQUE (parte_id);


--
-- Name: credenciales credenciales_pkey; Type: CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.credenciales
    ADD CONSTRAINT credenciales_pkey PRIMARY KEY (id);


--
-- Name: desafios desafios_pkey; Type: CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.desafios
    ADD CONSTRAINT desafios_pkey PRIMARY KEY (id);


--
-- Name: empresas empresas_nit_uk; Type: CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.empresas
    ADD CONSTRAINT empresas_nit_uk UNIQUE (nit);


--
-- Name: empresas empresas_pkey; Type: CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.empresas
    ADD CONSTRAINT empresas_pkey PRIMARY KEY (parte_id);


--
-- Name: eventos_autenticacion eventos_autenticacion_pkey; Type: CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.eventos_autenticacion
    ADD CONSTRAINT eventos_autenticacion_pkey PRIMARY KEY (id);


--
-- Name: kyc_estados kyc_estados_pkey; Type: CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.kyc_estados
    ADD CONSTRAINT kyc_estados_pkey PRIMARY KEY (parte_id);


--
-- Name: kyc_verificaciones kyc_verificaciones_pkey; Type: CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.kyc_verificaciones
    ADD CONSTRAINT kyc_verificaciones_pkey PRIMARY KEY (id);


--
-- Name: outbox outbox_pkey; Type: CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.outbox
    ADD CONSTRAINT outbox_pkey PRIMARY KEY (id);


--
-- Name: partes partes_pkey; Type: CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.partes
    ADD CONSTRAINT partes_pkey PRIMARY KEY (id);


--
-- Name: personas personas_documento_uk; Type: CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.personas
    ADD CONSTRAINT personas_documento_uk UNIQUE (tipo_documento, documento);


--
-- Name: personas personas_pkey; Type: CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.personas
    ADD CONSTRAINT personas_pkey PRIMARY KEY (parte_id);


--
-- Name: roles_de_parte roles_de_parte_pkey; Type: CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.roles_de_parte
    ADD CONSTRAINT roles_de_parte_pkey PRIMARY KEY (id);


--
-- Name: sesiones sesiones_pkey; Type: CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.sesiones
    ADD CONSTRAINT sesiones_pkey PRIMARY KEY (id);


--
-- Name: sesiones sesiones_refresh_hash_key; Type: CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.sesiones
    ADD CONSTRAINT sesiones_refresh_hash_key UNIQUE (refresh_hash);


--
-- Name: textos_consentimiento textos_consentimiento_pkey; Type: CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.textos_consentimiento
    ADD CONSTRAINT textos_consentimiento_pkey PRIMARY KEY (tipo, version);


--
-- Name: tokens_restablecimiento tokens_restablecimiento_pkey; Type: CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.tokens_restablecimiento
    ADD CONSTRAINT tokens_restablecimiento_pkey PRIMARY KEY (id);


--
-- Name: tokens_restablecimiento tokens_restablecimiento_token_hash_key; Type: CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.tokens_restablecimiento
    ADD CONSTRAINT tokens_restablecimiento_token_hash_key UNIQUE (token_hash);


--
-- Name: transiciones_credencial transiciones_credencial_pkey; Type: CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.transiciones_credencial
    ADD CONSTRAINT transiciones_credencial_pkey PRIMARY KEY (id);


--
-- Name: cambio_clasificacion cambio_clasificacion_componente_id_version_key; Type: CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.cambio_clasificacion
    ADD CONSTRAINT cambio_clasificacion_componente_id_version_key UNIQUE (componente_id, version);


--
-- Name: cambio_clasificacion cambio_clasificacion_pkey; Type: CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.cambio_clasificacion
    ADD CONSTRAINT cambio_clasificacion_pkey PRIMARY KEY (id);


--
-- Name: categoria categoria_pkey; Type: CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.categoria
    ADD CONSTRAINT categoria_pkey PRIMARY KEY (id);


--
-- Name: categoria categoria_propietario_id_codigo_key; Type: CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.categoria
    ADD CONSTRAINT categoria_propietario_id_codigo_key UNIQUE NULLS NOT DISTINCT (propietario_id, codigo);


--
-- Name: clasificacion_movimiento clasificacion_movimiento_pkey; Type: CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.clasificacion_movimiento
    ADD CONSTRAINT clasificacion_movimiento_pkey PRIMARY KEY (componente_id);


--
-- Name: componente_movimiento componente_movimiento_id_propietario_id_key; Type: CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.componente_movimiento
    ADD CONSTRAINT componente_movimiento_id_propietario_id_key UNIQUE (id, propietario_id);


--
-- Name: componente_movimiento componente_movimiento_pkey; Type: CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.componente_movimiento
    ADD CONSTRAINT componente_movimiento_pkey PRIMARY KEY (id);


--
-- Name: componente_movimiento componente_movimiento_posting_id_key; Type: CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.componente_movimiento
    ADD CONSTRAINT componente_movimiento_posting_id_key UNIQUE (posting_id);


--
-- Name: movimiento movimiento_id_propietario_id_key; Type: CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.movimiento
    ADD CONSTRAINT movimiento_id_propietario_id_key UNIQUE (id, propietario_id);


--
-- Name: movimiento movimiento_pkey; Type: CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.movimiento
    ADD CONSTRAINT movimiento_pkey PRIMARY KEY (id);


--
-- Name: periodo_presupuesto periodo_presupuesto_pkey; Type: CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.periodo_presupuesto
    ADD CONSTRAINT periodo_presupuesto_pkey PRIMARY KEY (id);


--
-- Name: periodo_presupuesto periodo_presupuesto_presupuesto_id_daterange_excl; Type: CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.periodo_presupuesto
    ADD CONSTRAINT periodo_presupuesto_presupuesto_id_daterange_excl EXCLUDE USING gist (presupuesto_id WITH =, daterange(desde, hasta_exclusivo, '[)'::text) WITH &&);


--
-- Name: periodo_presupuesto periodo_presupuesto_presupuesto_id_desde_key; Type: CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.periodo_presupuesto
    ADD CONSTRAINT periodo_presupuesto_presupuesto_id_desde_key UNIQUE (presupuesto_id, desde);


--
-- Name: presupuesto presupuesto_id_propietario_id_key; Type: CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.presupuesto
    ADD CONSTRAINT presupuesto_id_propietario_id_key UNIQUE (id, propietario_id);


--
-- Name: presupuesto presupuesto_pkey; Type: CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.presupuesto
    ADD CONSTRAINT presupuesto_pkey PRIMARY KEY (id);


--
-- Name: regla_clasificacion regla_clasificacion_pkey; Type: CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.regla_clasificacion
    ADD CONSTRAINT regla_clasificacion_pkey PRIMARY KEY (id);


--
-- Name: regla_clasificacion regla_clasificacion_propietario_id_contraparte_hmac_version_key; Type: CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.regla_clasificacion
    ADD CONSTRAINT regla_clasificacion_propietario_id_contraparte_hmac_version_key UNIQUE (propietario_id, contraparte_hmac, version_clave, motivo);


--
-- Name: titular titular_parte_id_key; Type: CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.titular
    ADD CONSTRAINT titular_parte_id_key UNIQUE (parte_id);


--
-- Name: titular titular_pkey; Type: CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.titular
    ADD CONSTRAINT titular_pkey PRIMARY KEY (usuario_financiero_id);


--
-- Name: titular titular_usuario_financiero_id_parte_id_key; Type: CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.titular
    ADD CONSTRAINT titular_usuario_financiero_id_parte_id_key UNIQUE (usuario_financiero_id, parte_id);


--
-- Name: conciliacion_corrida conciliacion_corrida_pkey; Type: CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.conciliacion_corrida
    ADD CONSTRAINT conciliacion_corrida_pkey PRIMARY KEY (id);


--
-- Name: cuenta cuenta_pkey; Type: CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.cuenta
    ADD CONSTRAINT cuenta_pkey PRIMARY KEY (id);


--
-- Name: deposito deposito_idempotencia_unica; Type: CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.deposito
    ADD CONSTRAINT deposito_idempotencia_unica UNIQUE (usuario_id, idempotency_key);


--
-- Name: deposito deposito_pkey; Type: CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.deposito
    ADD CONSTRAINT deposito_pkey PRIMARY KEY (id);


--
-- Name: deposito deposito_referencia_unica; Type: CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.deposito
    ADD CONSTRAINT deposito_referencia_unica UNIQUE (referencia);


--
-- Name: descuadre descuadre_pkey; Type: CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.descuadre
    ADD CONSTRAINT descuadre_pkey PRIMARY KEY (id);


--
-- Name: detalle_transferencia detalle_transferencia_pkey; Type: CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.detalle_transferencia
    ADD CONSTRAINT detalle_transferencia_pkey PRIMARY KEY (transferencia_id);


--
-- Name: evento_custodio evento_custodio_pkey; Type: CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.evento_custodio
    ADD CONSTRAINT evento_custodio_pkey PRIMARY KEY (id);


--
-- Name: evento_custodio evento_custodio_proveedor_evento_externo_id_key; Type: CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.evento_custodio
    ADD CONSTRAINT evento_custodio_proveedor_evento_externo_id_key UNIQUE (proveedor, evento_externo_id);


--
-- Name: evento_transferencia evento_transferencia_pkey; Type: CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.evento_transferencia
    ADD CONSTRAINT evento_transferencia_pkey PRIMARY KEY (id);


--
-- Name: extracto_custodio extracto_custodio_pkey; Type: CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.extracto_custodio
    ADD CONSTRAINT extracto_custodio_pkey PRIMARY KEY (corte_en);


--
-- Name: journal_entry journal_entry_pkey; Type: CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.journal_entry
    ADD CONSTRAINT journal_entry_pkey PRIMARY KEY (id);


--
-- Name: outbox outbox_pkey; Type: CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.outbox
    ADD CONSTRAINT outbox_pkey PRIMARY KEY (id);


--
-- Name: posting posting_pkey; Type: CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.posting
    ADD CONSTRAINT posting_pkey PRIMARY KEY (id);


--
-- Name: regla_limite regla_limite_pkey; Type: CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.regla_limite
    ADD CONSTRAINT regla_limite_pkey PRIMARY KEY (id);


--
-- Name: transferencia transferencia_idempotencia_unica; Type: CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.transferencia
    ADD CONSTRAINT transferencia_idempotencia_unica UNIQUE (usuario_id, idempotency_key);


--
-- Name: transferencia transferencia_pkey; Type: CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.transferencia
    ADD CONSTRAINT transferencia_pkey PRIMARY KEY (id);


--
-- Name: valor_uvt valor_uvt_pkey; Type: CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.valor_uvt
    ADD CONSTRAINT valor_uvt_pkey PRIMARY KEY (anio);


--
-- Name: destinatario_favorito destinatario_favorito_pkey; Type: CONSTRAINT; Schema: producto; Owner: -
--

ALTER TABLE ONLY producto.destinatario_favorito
    ADD CONSTRAINT destinatario_favorito_pkey PRIMARY KEY (id);


--
-- Name: notificacion notificacion_pkey; Type: CONSTRAINT; Schema: producto; Owner: -
--

ALTER TABLE ONLY producto.notificacion
    ADD CONSTRAINT notificacion_pkey PRIMARY KEY (id);


--
-- Name: notificacion notificacion_propietario_id_tipo_clave_deduplicacion_key; Type: CONSTRAINT; Schema: producto; Owner: -
--

ALTER TABLE ONLY producto.notificacion
    ADD CONSTRAINT notificacion_propietario_id_tipo_clave_deduplicacion_key UNIQUE (propietario_id, tipo, clave_deduplicacion);


--
-- Name: preferencia preferencia_pkey; Type: CONSTRAINT; Schema: producto; Owner: -
--

ALTER TABLE ONLY producto.preferencia
    ADD CONSTRAINT preferencia_pkey PRIMARY KEY (parte_id);


--
-- Name: tarjeta tarjeta_pkey; Type: CONSTRAINT; Schema: producto; Owner: -
--

ALTER TABLE ONLY producto.tarjeta
    ADD CONSTRAINT tarjeta_pkey PRIMARY KEY (id);


--
-- Name: tarjeta tarjeta_proveedor_referencia_proveedor_key; Type: CONSTRAINT; Schema: producto; Owner: -
--

ALTER TABLE ONLY producto.tarjeta
    ADD CONSTRAINT tarjeta_proveedor_referencia_proveedor_key UNIQUE (proveedor, referencia_proveedor);


--
-- Name: users users_email_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_email_key UNIQUE (email);


--
-- Name: users users_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);


--
-- Name: accion_expiracion_idx; Type: INDEX; Schema: asistente; Owner: -
--

CREATE INDEX accion_expiracion_idx ON asistente.accion_pendiente USING btree (expira_en, id) WHERE (estado = 'pendiente'::text);


--
-- Name: accion_pendiente_conversacion_uk; Type: INDEX; Schema: asistente; Owner: -
--

CREATE UNIQUE INDEX accion_pendiente_conversacion_uk ON asistente.accion_pendiente USING btree (conversacion_id) WHERE (estado = 'pendiente'::text);


--
-- Name: accion_transferencia_uk; Type: INDEX; Schema: asistente; Owner: -
--

CREATE UNIQUE INDEX accion_transferencia_uk ON asistente.accion_pendiente USING btree (transferencia_id) WHERE (transferencia_id IS NOT NULL);


--
-- Name: accion_usuario_idx; Type: INDEX; Schema: asistente; Owner: -
--

CREATE INDEX accion_usuario_idx ON asistente.accion_pendiente USING btree (usuario_id, creada_en DESC, id DESC);


--
-- Name: conversacion_reciente_idx; Type: INDEX; Schema: asistente; Owner: -
--

CREATE INDEX conversacion_reciente_idx ON asistente.conversacion USING btree (usuario_id, ultimo_mensaje_en DESC, id DESC);


--
-- Name: conversacion_retencion_idx; Type: INDEX; Schema: asistente; Owner: -
--

CREATE INDEX conversacion_retencion_idx ON asistente.conversacion USING btree (ultimo_mensaje_en);


--
-- Name: ejecucion_proveedor_fecha_idx; Type: INDEX; Schema: asistente; Owner: -
--

CREATE INDEX ejecucion_proveedor_fecha_idx ON asistente.ejecucion_proveedor USING btree (iniciada_en);


--
-- Name: ejecucion_proveedor_turno_idx; Type: INDEX; Schema: asistente; Owner: -
--

CREATE INDEX ejecucion_proveedor_turno_idx ON asistente.ejecucion_proveedor USING btree (usuario_id, turno_id);


--
-- Name: llamada_fecha_idx; Type: INDEX; Schema: asistente; Owner: -
--

CREATE INDEX llamada_fecha_idx ON asistente.llamada_funcion USING btree (ocurrida_en, id);


--
-- Name: llamada_mensaje_idx; Type: INDEX; Schema: asistente; Owner: -
--

CREATE INDEX llamada_mensaje_idx ON asistente.llamada_funcion USING btree (conversacion_id, mensaje_id, ocurrida_en, id);


--
-- Name: mensaje_respuesta_idx; Type: INDEX; Schema: asistente; Owner: -
--

CREATE INDEX mensaje_respuesta_idx ON asistente.mensaje USING btree (respuesta_a) WHERE (respuesta_a IS NOT NULL);


--
-- Name: codigos_respaldo_pendientes; Type: INDEX; Schema: auth; Owner: -
--

CREATE INDEX codigos_respaldo_pendientes ON auth.codigos_respaldo USING btree (credencial_id) WHERE (usado_en IS NULL);


--
-- Name: consentimientos_asistente_ultimo; Type: INDEX; Schema: auth; Owner: -
--

CREATE INDEX consentimientos_asistente_ultimo ON auth.consentimientos USING btree (parte_id, orden DESC) WHERE (tipo = 'asistente_ia'::text);


--
-- Name: consentimientos_parte; Type: INDEX; Schema: auth; Owner: -
--

CREATE INDEX consentimientos_parte ON auth.consentimientos USING btree (parte_id, tipo, ocurrido_en);


--
-- Name: credenciales_celular_uk; Type: INDEX; Schema: auth; Owner: -
--

CREATE UNIQUE INDEX credenciales_celular_uk ON auth.credenciales USING btree (celular) WHERE (estado <> 'cerrado'::text);


--
-- Name: credenciales_correo_uk; Type: INDEX; Schema: auth; Owner: -
--

CREATE UNIQUE INDEX credenciales_correo_uk ON auth.credenciales USING btree (correo) WHERE (estado <> 'cerrado'::text);


--
-- Name: desafios_pendientes; Type: INDEX; Schema: auth; Owner: -
--

CREATE INDEX desafios_pendientes ON auth.desafios USING btree (expira_en) WHERE (estado = 'pendiente'::text);


--
-- Name: eventos_autenticacion_actor; Type: INDEX; Schema: auth; Owner: -
--

CREATE INDEX eventos_autenticacion_actor ON auth.eventos_autenticacion USING btree (actor_parte_id, ocurrido_en);


--
-- Name: eventos_autenticacion_tipo; Type: INDEX; Schema: auth; Owner: -
--

CREATE INDEX eventos_autenticacion_tipo ON auth.eventos_autenticacion USING btree (tipo, ocurrido_en);


--
-- Name: kyc_verificaciones_parte; Type: INDEX; Schema: auth; Owner: -
--

CREATE INDEX kyc_verificaciones_parte ON auth.kyc_verificaciones USING btree (parte_id, ocurrida_en);


--
-- Name: outbox_pendientes; Type: INDEX; Schema: auth; Owner: -
--

CREATE INDEX outbox_pendientes ON auth.outbox USING btree (disponible_en) WHERE (despachado_en IS NULL);


--
-- Name: roles_de_parte_vigente_uk; Type: INDEX; Schema: auth; Owner: -
--

CREATE UNIQUE INDEX roles_de_parte_vigente_uk ON auth.roles_de_parte USING btree (parte_id, rol, representada_id) WHERE (revocado_en IS NULL);


--
-- Name: sesiones_familia; Type: INDEX; Schema: auth; Owner: -
--

CREATE INDEX sesiones_familia ON auth.sesiones USING btree (familia_id);


--
-- Name: sesiones_parte_vigente; Type: INDEX; Schema: auth; Owner: -
--

CREATE INDEX sesiones_parte_vigente ON auth.sesiones USING btree (parte_id, revocada_en);


--
-- Name: tokens_restablecimiento_pendientes; Type: INDEX; Schema: auth; Owner: -
--

CREATE INDEX tokens_restablecimiento_pendientes ON auth.tokens_restablecimiento USING btree (token_hash) WHERE (usado_en IS NULL);


--
-- Name: categoria_propietario_idx; Type: INDEX; Schema: finanzas; Owner: -
--

CREATE INDEX categoria_propietario_idx ON finanzas.categoria USING btree (propietario_id, padre_id);


--
-- Name: clasificacion_categoria_idx; Type: INDEX; Schema: finanzas; Owner: -
--

CREATE INDEX clasificacion_categoria_idx ON finanzas.clasificacion_movimiento USING btree (propietario_id, categoria_id, componente_id);


--
-- Name: componente_movimiento_idx; Type: INDEX; Schema: finanzas; Owner: -
--

CREATE INDEX componente_movimiento_idx ON finanzas.componente_movimiento USING btree (movimiento_id);


--
-- Name: movimiento_entry_uk; Type: INDEX; Schema: finanzas; Owner: -
--

CREATE UNIQUE INDEX movimiento_entry_uk ON finanzas.movimiento USING btree (propietario_id, entry_sin_transferencia_id) WHERE (entry_sin_transferencia_id IS NOT NULL);


--
-- Name: movimiento_periodo_idx; Type: INDEX; Schema: finanzas; Owner: -
--

CREATE INDEX movimiento_periodo_idx ON finanzas.movimiento USING btree (propietario_id, ocurrido_en DESC, id DESC);


--
-- Name: movimiento_transferencia_uk; Type: INDEX; Schema: finanzas; Owner: -
--

CREATE UNIQUE INDEX movimiento_transferencia_uk ON finanzas.movimiento USING btree (propietario_id, transferencia_id) WHERE (transferencia_id IS NOT NULL);


--
-- Name: periodo_presupuesto_idx; Type: INDEX; Schema: finanzas; Owner: -
--

CREATE INDEX periodo_presupuesto_idx ON finanzas.periodo_presupuesto USING btree (propietario_id, desde, hasta_exclusivo);


--
-- Name: conciliacion_corrida_por_ventana; Type: INDEX; Schema: nucleo; Owner: -
--

CREATE INDEX conciliacion_corrida_por_ventana ON nucleo.conciliacion_corrida USING btree (ventana_hasta DESC, id DESC);


--
-- Name: descuadre_identidad_unico; Type: INDEX; Schema: nucleo; Owner: -
--

CREATE UNIQUE INDEX descuadre_identidad_unico ON nucleo.descuadre USING btree (tipo, COALESCE((transferencia_id)::text, referencia_externa)) WHERE (resuelto_en IS NULL);


--
-- Name: detalle_transferencia_destino_idx; Type: INDEX; Schema: nucleo; Owner: -
--

CREATE INDEX detalle_transferencia_destino_idx ON nucleo.detalle_transferencia USING btree (destinatario_id, creada_en DESC);


--
-- Name: evento_transferencia_idx; Type: INDEX; Schema: nucleo; Owner: -
--

CREATE INDEX evento_transferencia_idx ON nucleo.evento_transferencia USING btree (transferencia_id, id);


--
-- Name: journal_entry_deposito_unico; Type: INDEX; Schema: nucleo; Owner: -
--

CREATE UNIQUE INDEX journal_entry_deposito_unico ON nucleo.journal_entry USING btree (deposito_id) WHERE (deposito_id IS NOT NULL);


--
-- Name: journal_entry_ocurrido_en; Type: INDEX; Schema: nucleo; Owner: -
--

CREATE INDEX journal_entry_ocurrido_en ON nucleo.journal_entry USING btree (ocurrido_en);


--
-- Name: journal_entry_transferencia_id; Type: INDEX; Schema: nucleo; Owner: -
--

CREATE INDEX journal_entry_transferencia_id ON nucleo.journal_entry USING btree (transferencia_id);


--
-- Name: journal_transferencia_tipo_idx; Type: INDEX; Schema: nucleo; Owner: -
--

CREATE INDEX journal_transferencia_tipo_idx ON nucleo.journal_entry USING btree (transferencia_id, tipo, id);


--
-- Name: nucleo_deposito_pendiente_creada_idx; Type: INDEX; Schema: nucleo; Owner: -
--

CREATE INDEX nucleo_deposito_pendiente_creada_idx ON nucleo.deposito USING btree (creada_en) WHERE (estado = 'pendiente'::text);


--
-- Name: outbox_despacho_por_transferencia; Type: INDEX; Schema: nucleo; Owner: -
--

CREATE INDEX outbox_despacho_por_transferencia ON nucleo.outbox USING btree (((payload ->> 'transferencia_id'::text))) WHERE ((tipo = 'despacho_transferencia'::text) AND (despachado_en IS NULL));


--
-- Name: outbox_pendientes; Type: INDEX; Schema: nucleo; Owner: -
--

CREATE INDEX outbox_pendientes ON nucleo.outbox USING btree (disponible_en) WHERE (despachado_en IS NULL);


--
-- Name: posting_cuenta_fecha_idx; Type: INDEX; Schema: nucleo; Owner: -
--

CREATE INDEX posting_cuenta_fecha_idx ON nucleo.posting USING btree (cuenta_id, ocurrido_en DESC, entry_id DESC) INCLUDE (moneda);


--
-- Name: posting_por_asiento; Type: INDEX; Schema: nucleo; Owner: -
--

CREATE INDEX posting_por_asiento ON nucleo.posting USING btree (entry_id);


--
-- Name: posting_por_cuenta; Type: INDEX; Schema: nucleo; Owner: -
--

CREATE INDEX posting_por_cuenta ON nucleo.posting USING btree (cuenta_id, id);


--
-- Name: transferencia_activas_por_actualizada; Type: INDEX; Schema: nucleo; Owner: -
--

CREATE INDEX transferencia_activas_por_actualizada ON nucleo.transferencia USING btree (actualizada_en) WHERE (estado = ANY (ARRAY['despachada'::text, 'indeterminada'::text]));


--
-- Name: transferencia_debito_del_mes; Type: INDEX; Schema: nucleo; Owner: -
--

CREATE INDEX transferencia_debito_del_mes ON nucleo.transferencia USING btree (usuario_id, creada_en) INCLUDE (monto, estado);


--
-- Name: favorito_interno_uk; Type: INDEX; Schema: producto; Owner: -
--

CREATE UNIQUE INDEX favorito_interno_uk ON producto.destinatario_favorito USING btree (propietario_id, destinatario_id) WHERE (destinatario_id IS NOT NULL);


--
-- Name: favorito_llave_uk; Type: INDEX; Schema: producto; Owner: -
--

CREATE UNIQUE INDEX favorito_llave_uk ON producto.destinatario_favorito USING btree (propietario_id, tipo_llave, llave_hmac, version_clave) WHERE (tipo_llave IS NOT NULL);


--
-- Name: notificacion_reciente_idx; Type: INDEX; Schema: producto; Owner: -
--

CREATE INDEX notificacion_reciente_idx ON producto.notificacion USING btree (propietario_id, creada_en DESC, id DESC);


--
-- Name: accion_pendiente accion_origen_inmutable; Type: TRIGGER; Schema: asistente; Owner: -
--

CREATE TRIGGER accion_origen_inmutable BEFORE UPDATE ON asistente.accion_pendiente FOR EACH ROW EXECUTE FUNCTION asistente.validar_origen_accion();


--
-- Name: accion_pendiente accion_transicion; Type: TRIGGER; Schema: asistente; Owner: -
--

CREATE TRIGGER accion_transicion BEFORE INSERT OR UPDATE ON asistente.accion_pendiente FOR EACH ROW EXECUTE FUNCTION asistente.validar_transicion_accion();


--
-- Name: conversacion conversacion_cancelar; Type: TRIGGER; Schema: asistente; Owner: -
--

CREATE TRIGGER conversacion_cancelar BEFORE DELETE ON asistente.conversacion FOR EACH ROW EXECUTE FUNCTION asistente.cancelar_antes_de_borrar();


--
-- Name: llamada_funcion llamada_referencia; Type: TRIGGER; Schema: asistente; Owner: -
--

CREATE TRIGGER llamada_referencia BEFORE INSERT ON asistente.llamada_funcion FOR EACH ROW EXECUTE FUNCTION asistente.validar_referencia_auditoria();


--
-- Name: consentimientos consentimientos_inmutable; Type: TRIGGER; Schema: auth; Owner: -
--

CREATE TRIGGER consentimientos_inmutable BEFORE DELETE OR UPDATE ON auth.consentimientos FOR EACH ROW EXECUTE FUNCTION auth.consentimientos_inmutable();


--
-- Name: eventos_autenticacion eventos_autenticacion_inmutable; Type: TRIGGER; Schema: auth; Owner: -
--

CREATE TRIGGER eventos_autenticacion_inmutable BEFORE DELETE OR UPDATE ON auth.eventos_autenticacion FOR EACH ROW EXECUTE FUNCTION auth.eventos_autenticacion_inmutable();


--
-- Name: kyc_verificaciones kyc_verificaciones_inmutable; Type: TRIGGER; Schema: auth; Owner: -
--

CREATE TRIGGER kyc_verificaciones_inmutable BEFORE DELETE OR UPDATE ON auth.kyc_verificaciones FOR EACH ROW EXECUTE FUNCTION auth.kyc_verificaciones_inmutable();


--
-- Name: categoria categoria_valida; Type: TRIGGER; Schema: finanzas; Owner: -
--

CREATE TRIGGER categoria_valida BEFORE INSERT OR UPDATE ON finanzas.categoria FOR EACH ROW EXECUTE FUNCTION finanzas.validar_categoria();


--
-- Name: clasificacion_movimiento clasificacion_auditada; Type: TRIGGER; Schema: finanzas; Owner: -
--

CREATE TRIGGER clasificacion_auditada AFTER INSERT OR UPDATE ON finanzas.clasificacion_movimiento FOR EACH ROW EXECUTE FUNCTION finanzas.auditar_clasificacion();


--
-- Name: clasificacion_movimiento clasificacion_visible; Type: TRIGGER; Schema: finanzas; Owner: -
--

CREATE TRIGGER clasificacion_visible BEFORE INSERT OR UPDATE ON finanzas.clasificacion_movimiento FOR EACH ROW EXECUTE FUNCTION finanzas.validar_categoria_visible();


--
-- Name: componente_movimiento componente_valido; Type: TRIGGER; Schema: finanzas; Owner: -
--

CREATE TRIGGER componente_valido BEFORE INSERT OR UPDATE ON finanzas.componente_movimiento FOR EACH ROW EXECUTE FUNCTION finanzas.validar_componente();


--
-- Name: movimiento movimiento_valido; Type: TRIGGER; Schema: finanzas; Owner: -
--

CREATE TRIGGER movimiento_valido BEFORE INSERT OR UPDATE ON finanzas.movimiento FOR EACH ROW EXECUTE FUNCTION finanzas.validar_movimiento();


--
-- Name: presupuesto presupuesto_visible; Type: TRIGGER; Schema: finanzas; Owner: -
--

CREATE TRIGGER presupuesto_visible BEFORE INSERT OR UPDATE ON finanzas.presupuesto FOR EACH ROW EXECUTE FUNCTION finanzas.validar_categoria_visible();


--
-- Name: regla_clasificacion regla_visible; Type: TRIGGER; Schema: finanzas; Owner: -
--

CREATE TRIGGER regla_visible BEFORE INSERT OR UPDATE ON finanzas.regla_clasificacion FOR EACH ROW EXECUTE FUNCTION finanzas.validar_categoria_visible();


--
-- Name: posting asiento_cuadra; Type: TRIGGER; Schema: nucleo; Owner: -
--

CREATE CONSTRAINT TRIGGER asiento_cuadra AFTER INSERT ON nucleo.posting DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION nucleo.verificar_asiento_cuadra();


--
-- Name: journal_entry asiento_minimo_dos_lineas; Type: TRIGGER; Schema: nucleo; Owner: -
--

CREATE CONSTRAINT TRIGGER asiento_minimo_dos_lineas AFTER INSERT ON nucleo.journal_entry DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION nucleo.verificar_asiento_tiene_dos_lineas();


--
-- Name: posting posting_fecha_asiento; Type: TRIGGER; Schema: nucleo; Owner: -
--

CREATE TRIGGER posting_fecha_asiento BEFORE INSERT ON nucleo.posting FOR EACH ROW EXECUTE FUNCTION nucleo.fijar_fecha_posting();


--
-- Name: tarjeta tarjeta_cuenta_valida; Type: TRIGGER; Schema: producto; Owner: -
--

CREATE TRIGGER tarjeta_cuenta_valida BEFORE INSERT OR UPDATE ON producto.tarjeta FOR EACH ROW EXECUTE FUNCTION producto.validar_cuenta_tarjeta();


--
-- Name: accion_pendiente accion_pendiente_desafio_id_fkey; Type: FK CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.accion_pendiente
    ADD CONSTRAINT accion_pendiente_desafio_id_fkey FOREIGN KEY (desafio_id) REFERENCES auth.desafios(id);


--
-- Name: accion_pendiente accion_pendiente_transferencia_id_fkey; Type: FK CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.accion_pendiente
    ADD CONSTRAINT accion_pendiente_transferencia_id_fkey FOREIGN KEY (transferencia_id) REFERENCES nucleo.transferencia(id);


--
-- Name: accion_pendiente accion_propietario_fk; Type: FK CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.accion_pendiente
    ADD CONSTRAINT accion_propietario_fk FOREIGN KEY (conversacion_id, usuario_id) REFERENCES asistente.conversacion(id, usuario_id) ON DELETE CASCADE;


--
-- Name: conversacion conversacion_usuario_id_fkey; Type: FK CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.conversacion
    ADD CONSTRAINT conversacion_usuario_id_fkey FOREIGN KEY (usuario_id) REFERENCES auth.partes(id);


--
-- Name: ejecucion_proveedor ejecucion_proveedor_usuario_id_fkey; Type: FK CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.ejecucion_proveedor
    ADD CONSTRAINT ejecucion_proveedor_usuario_id_fkey FOREIGN KEY (usuario_id) REFERENCES auth.partes(id);


--
-- Name: mensaje mensaje_accion_fk; Type: FK CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.mensaje
    ADD CONSTRAINT mensaje_accion_fk FOREIGN KEY (conversacion_id, accion_id) REFERENCES asistente.accion_pendiente(conversacion_id, id);


--
-- Name: mensaje mensaje_conversacion_id_fkey; Type: FK CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.mensaje
    ADD CONSTRAINT mensaje_conversacion_id_fkey FOREIGN KEY (conversacion_id) REFERENCES asistente.conversacion(id) ON DELETE CASCADE;


--
-- Name: mensaje mensaje_respuesta_fk; Type: FK CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.mensaje
    ADD CONSTRAINT mensaje_respuesta_fk FOREIGN KEY (conversacion_id, respuesta_a) REFERENCES asistente.mensaje(conversacion_id, id);


--
-- Name: solicitud_texto solicitud_texto_conversacion_id_mensaje_id_fkey; Type: FK CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.solicitud_texto
    ADD CONSTRAINT solicitud_texto_conversacion_id_mensaje_id_fkey FOREIGN KEY (conversacion_id, mensaje_id) REFERENCES asistente.mensaje(conversacion_id, id) ON DELETE CASCADE;


--
-- Name: solicitud_texto solicitud_texto_conversacion_id_usuario_id_fkey; Type: FK CONSTRAINT; Schema: asistente; Owner: -
--

ALTER TABLE ONLY asistente.solicitud_texto
    ADD CONSTRAINT solicitud_texto_conversacion_id_usuario_id_fkey FOREIGN KEY (conversacion_id, usuario_id) REFERENCES asistente.conversacion(id, usuario_id) ON DELETE CASCADE;


--
-- Name: codigos_respaldo codigos_respaldo_credencial_id_fkey; Type: FK CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.codigos_respaldo
    ADD CONSTRAINT codigos_respaldo_credencial_id_fkey FOREIGN KEY (credencial_id) REFERENCES auth.credenciales(id);


--
-- Name: consentimientos consentimientos_parte_id_fkey; Type: FK CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.consentimientos
    ADD CONSTRAINT consentimientos_parte_id_fkey FOREIGN KEY (parte_id) REFERENCES auth.partes(id);


--
-- Name: credenciales credenciales_parte_id_fkey; Type: FK CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.credenciales
    ADD CONSTRAINT credenciales_parte_id_fkey FOREIGN KEY (parte_id) REFERENCES auth.partes(id);


--
-- Name: desafios desafios_parte_id_fkey; Type: FK CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.desafios
    ADD CONSTRAINT desafios_parte_id_fkey FOREIGN KEY (parte_id) REFERENCES auth.partes(id);


--
-- Name: empresas empresas_parte_id_fkey; Type: FK CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.empresas
    ADD CONSTRAINT empresas_parte_id_fkey FOREIGN KEY (parte_id) REFERENCES auth.partes(id);


--
-- Name: kyc_estados kyc_estados_parte_id_fkey; Type: FK CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.kyc_estados
    ADD CONSTRAINT kyc_estados_parte_id_fkey FOREIGN KEY (parte_id) REFERENCES auth.partes(id);


--
-- Name: kyc_verificaciones kyc_verificaciones_parte_id_fkey; Type: FK CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.kyc_verificaciones
    ADD CONSTRAINT kyc_verificaciones_parte_id_fkey FOREIGN KEY (parte_id) REFERENCES auth.partes(id);


--
-- Name: personas personas_parte_id_fkey; Type: FK CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.personas
    ADD CONSTRAINT personas_parte_id_fkey FOREIGN KEY (parte_id) REFERENCES auth.partes(id);


--
-- Name: roles_de_parte roles_de_parte_parte_id_fkey; Type: FK CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.roles_de_parte
    ADD CONSTRAINT roles_de_parte_parte_id_fkey FOREIGN KEY (parte_id) REFERENCES auth.partes(id);


--
-- Name: roles_de_parte roles_de_parte_representada_id_fkey; Type: FK CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.roles_de_parte
    ADD CONSTRAINT roles_de_parte_representada_id_fkey FOREIGN KEY (representada_id) REFERENCES auth.partes(id);


--
-- Name: sesiones sesiones_parte_id_fkey; Type: FK CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.sesiones
    ADD CONSTRAINT sesiones_parte_id_fkey FOREIGN KEY (parte_id) REFERENCES auth.partes(id);


--
-- Name: tokens_restablecimiento tokens_restablecimiento_credencial_id_fkey; Type: FK CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.tokens_restablecimiento
    ADD CONSTRAINT tokens_restablecimiento_credencial_id_fkey FOREIGN KEY (credencial_id) REFERENCES auth.credenciales(id);


--
-- Name: transiciones_credencial transiciones_credencial_credencial_id_fkey; Type: FK CONSTRAINT; Schema: auth; Owner: -
--

ALTER TABLE ONLY auth.transiciones_credencial
    ADD CONSTRAINT transiciones_credencial_credencial_id_fkey FOREIGN KEY (credencial_id) REFERENCES auth.credenciales(id);


--
-- Name: cambio_clasificacion cambio_clasificacion_componente_id_fkey; Type: FK CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.cambio_clasificacion
    ADD CONSTRAINT cambio_clasificacion_componente_id_fkey FOREIGN KEY (componente_id) REFERENCES finanzas.componente_movimiento(id);


--
-- Name: categoria categoria_padre_id_fkey; Type: FK CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.categoria
    ADD CONSTRAINT categoria_padre_id_fkey FOREIGN KEY (padre_id) REFERENCES finanzas.categoria(id);


--
-- Name: categoria categoria_propietario_id_fkey; Type: FK CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.categoria
    ADD CONSTRAINT categoria_propietario_id_fkey FOREIGN KEY (propietario_id) REFERENCES auth.partes(id);


--
-- Name: clasificacion_movimiento clasificacion_movimiento_actor_id_fkey; Type: FK CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.clasificacion_movimiento
    ADD CONSTRAINT clasificacion_movimiento_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES auth.partes(id);


--
-- Name: clasificacion_movimiento clasificacion_movimiento_categoria_id_fkey; Type: FK CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.clasificacion_movimiento
    ADD CONSTRAINT clasificacion_movimiento_categoria_id_fkey FOREIGN KEY (categoria_id) REFERENCES finanzas.categoria(id);


--
-- Name: clasificacion_movimiento clasificacion_movimiento_componente_id_propietario_id_fkey; Type: FK CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.clasificacion_movimiento
    ADD CONSTRAINT clasificacion_movimiento_componente_id_propietario_id_fkey FOREIGN KEY (componente_id, propietario_id) REFERENCES finanzas.componente_movimiento(id, propietario_id);


--
-- Name: componente_movimiento componente_movimiento_movimiento_id_propietario_id_fkey; Type: FK CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.componente_movimiento
    ADD CONSTRAINT componente_movimiento_movimiento_id_propietario_id_fkey FOREIGN KEY (movimiento_id, propietario_id) REFERENCES finanzas.movimiento(id, propietario_id);


--
-- Name: componente_movimiento componente_movimiento_posting_id_fkey; Type: FK CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.componente_movimiento
    ADD CONSTRAINT componente_movimiento_posting_id_fkey FOREIGN KEY (posting_id) REFERENCES nucleo.posting(id);


--
-- Name: movimiento movimiento_cuenta_id_fkey; Type: FK CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.movimiento
    ADD CONSTRAINT movimiento_cuenta_id_fkey FOREIGN KEY (cuenta_id) REFERENCES nucleo.cuenta(id);


--
-- Name: movimiento movimiento_entry_sin_transferencia_id_fkey; Type: FK CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.movimiento
    ADD CONSTRAINT movimiento_entry_sin_transferencia_id_fkey FOREIGN KEY (entry_sin_transferencia_id) REFERENCES nucleo.journal_entry(id);


--
-- Name: movimiento movimiento_propietario_id_fkey; Type: FK CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.movimiento
    ADD CONSTRAINT movimiento_propietario_id_fkey FOREIGN KEY (propietario_id) REFERENCES auth.partes(id);


--
-- Name: movimiento movimiento_transferencia_id_fkey; Type: FK CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.movimiento
    ADD CONSTRAINT movimiento_transferencia_id_fkey FOREIGN KEY (transferencia_id) REFERENCES nucleo.transferencia(id);


--
-- Name: movimiento movimiento_usuario_financiero_id_propietario_id_fkey; Type: FK CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.movimiento
    ADD CONSTRAINT movimiento_usuario_financiero_id_propietario_id_fkey FOREIGN KEY (usuario_financiero_id, propietario_id) REFERENCES finanzas.titular(usuario_financiero_id, parte_id);


--
-- Name: periodo_presupuesto periodo_presupuesto_presupuesto_id_propietario_id_fkey; Type: FK CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.periodo_presupuesto
    ADD CONSTRAINT periodo_presupuesto_presupuesto_id_propietario_id_fkey FOREIGN KEY (presupuesto_id, propietario_id) REFERENCES finanzas.presupuesto(id, propietario_id);


--
-- Name: presupuesto presupuesto_categoria_id_fkey; Type: FK CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.presupuesto
    ADD CONSTRAINT presupuesto_categoria_id_fkey FOREIGN KEY (categoria_id) REFERENCES finanzas.categoria(id);


--
-- Name: presupuesto presupuesto_propietario_id_fkey; Type: FK CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.presupuesto
    ADD CONSTRAINT presupuesto_propietario_id_fkey FOREIGN KEY (propietario_id) REFERENCES auth.partes(id);


--
-- Name: regla_clasificacion regla_clasificacion_categoria_id_fkey; Type: FK CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.regla_clasificacion
    ADD CONSTRAINT regla_clasificacion_categoria_id_fkey FOREIGN KEY (categoria_id) REFERENCES finanzas.categoria(id);


--
-- Name: regla_clasificacion regla_clasificacion_propietario_id_fkey; Type: FK CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.regla_clasificacion
    ADD CONSTRAINT regla_clasificacion_propietario_id_fkey FOREIGN KEY (propietario_id) REFERENCES auth.partes(id);


--
-- Name: titular titular_parte_id_fkey; Type: FK CONSTRAINT; Schema: finanzas; Owner: -
--

ALTER TABLE ONLY finanzas.titular
    ADD CONSTRAINT titular_parte_id_fkey FOREIGN KEY (parte_id) REFERENCES auth.partes(id);


--
-- Name: detalle_transferencia detalle_transferencia_actor_id_fkey; Type: FK CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.detalle_transferencia
    ADD CONSTRAINT detalle_transferencia_actor_id_fkey FOREIGN KEY (actor_id) REFERENCES auth.partes(id);


--
-- Name: detalle_transferencia detalle_transferencia_destinatario_id_fkey; Type: FK CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.detalle_transferencia
    ADD CONSTRAINT detalle_transferencia_destinatario_id_fkey FOREIGN KEY (destinatario_id) REFERENCES auth.partes(id);


--
-- Name: detalle_transferencia detalle_transferencia_transferencia_id_fkey; Type: FK CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.detalle_transferencia
    ADD CONSTRAINT detalle_transferencia_transferencia_id_fkey FOREIGN KEY (transferencia_id) REFERENCES nucleo.transferencia(id);


--
-- Name: evento_custodio evento_custodio_deposito_id_fkey; Type: FK CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.evento_custodio
    ADD CONSTRAINT evento_custodio_deposito_id_fkey FOREIGN KEY (deposito_id) REFERENCES nucleo.deposito(id);


--
-- Name: evento_custodio evento_custodio_transferencia_id_fkey; Type: FK CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.evento_custodio
    ADD CONSTRAINT evento_custodio_transferencia_id_fkey FOREIGN KEY (transferencia_id) REFERENCES nucleo.transferencia(id);


--
-- Name: evento_transferencia evento_transferencia_transferencia_id_fkey; Type: FK CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.evento_transferencia
    ADD CONSTRAINT evento_transferencia_transferencia_id_fkey FOREIGN KEY (transferencia_id) REFERENCES nucleo.transferencia(id);


--
-- Name: journal_entry journal_entry_deposito_id_fkey; Type: FK CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.journal_entry
    ADD CONSTRAINT journal_entry_deposito_id_fkey FOREIGN KEY (deposito_id) REFERENCES nucleo.deposito(id);


--
-- Name: posting posting_cuenta_id_fkey; Type: FK CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.posting
    ADD CONSTRAINT posting_cuenta_id_fkey FOREIGN KEY (cuenta_id) REFERENCES nucleo.cuenta(id);


--
-- Name: posting posting_entry_id_fkey; Type: FK CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.posting
    ADD CONSTRAINT posting_entry_id_fkey FOREIGN KEY (entry_id) REFERENCES nucleo.journal_entry(id);


--
-- Name: transferencia transferencia_cuenta_origen_fkey; Type: FK CONSTRAINT; Schema: nucleo; Owner: -
--

ALTER TABLE ONLY nucleo.transferencia
    ADD CONSTRAINT transferencia_cuenta_origen_fkey FOREIGN KEY (cuenta_origen) REFERENCES nucleo.cuenta(id);


--
-- Name: destinatario_favorito destinatario_favorito_destinatario_id_fkey; Type: FK CONSTRAINT; Schema: producto; Owner: -
--

ALTER TABLE ONLY producto.destinatario_favorito
    ADD CONSTRAINT destinatario_favorito_destinatario_id_fkey FOREIGN KEY (destinatario_id) REFERENCES auth.partes(id);


--
-- Name: destinatario_favorito destinatario_favorito_propietario_id_fkey; Type: FK CONSTRAINT; Schema: producto; Owner: -
--

ALTER TABLE ONLY producto.destinatario_favorito
    ADD CONSTRAINT destinatario_favorito_propietario_id_fkey FOREIGN KEY (propietario_id) REFERENCES auth.partes(id);


--
-- Name: notificacion notificacion_propietario_id_fkey; Type: FK CONSTRAINT; Schema: producto; Owner: -
--

ALTER TABLE ONLY producto.notificacion
    ADD CONSTRAINT notificacion_propietario_id_fkey FOREIGN KEY (propietario_id) REFERENCES auth.partes(id);


--
-- Name: preferencia preferencia_parte_id_fkey; Type: FK CONSTRAINT; Schema: producto; Owner: -
--

ALTER TABLE ONLY producto.preferencia
    ADD CONSTRAINT preferencia_parte_id_fkey FOREIGN KEY (parte_id) REFERENCES auth.partes(id);


--
-- Name: tarjeta tarjeta_cuenta_id_fkey; Type: FK CONSTRAINT; Schema: producto; Owner: -
--

ALTER TABLE ONLY producto.tarjeta
    ADD CONSTRAINT tarjeta_cuenta_id_fkey FOREIGN KEY (cuenta_id) REFERENCES nucleo.cuenta(id);


--
-- Name: tarjeta tarjeta_propietario_id_fkey; Type: FK CONSTRAINT; Schema: producto; Owner: -
--

ALTER TABLE ONLY producto.tarjeta
    ADD CONSTRAINT tarjeta_propietario_id_fkey FOREIGN KEY (propietario_id) REFERENCES auth.partes(id);


--
-- Name: cambio_clasificacion; Type: ROW SECURITY; Schema: finanzas; Owner: -
--

ALTER TABLE finanzas.cambio_clasificacion ENABLE ROW LEVEL SECURITY;

--
-- Name: cambio_clasificacion cambio_propio; Type: POLICY; Schema: finanzas; Owner: -
--

CREATE POLICY cambio_propio ON finanzas.cambio_clasificacion TO rol_finanzas USING ((EXISTS ( SELECT 1
   FROM finanzas.componente_movimiento c
  WHERE (c.id = cambio_clasificacion.componente_id)))) WITH CHECK ((EXISTS ( SELECT 1
   FROM finanzas.componente_movimiento c
  WHERE (c.id = cambio_clasificacion.componente_id))));


--
-- Name: categoria; Type: ROW SECURITY; Schema: finanzas; Owner: -
--

ALTER TABLE finanzas.categoria ENABLE ROW LEVEL SECURITY;

--
-- Name: categoria categoria_alta; Type: POLICY; Schema: finanzas; Owner: -
--

CREATE POLICY categoria_alta ON finanzas.categoria FOR INSERT TO rol_finanzas WITH CHECK (((propietario_id)::text = current_setting('vera.parte_id'::text, true)));


--
-- Name: categoria categoria_edicion; Type: POLICY; Schema: finanzas; Owner: -
--

CREATE POLICY categoria_edicion ON finanzas.categoria FOR UPDATE TO rol_finanzas USING (((propietario_id)::text = current_setting('vera.parte_id'::text, true))) WITH CHECK (((propietario_id)::text = current_setting('vera.parte_id'::text, true)));


--
-- Name: categoria categoria_lectura; Type: POLICY; Schema: finanzas; Owner: -
--

CREATE POLICY categoria_lectura ON finanzas.categoria FOR SELECT TO rol_finanzas USING (((propietario_id IS NULL) OR ((propietario_id)::text = current_setting('vera.parte_id'::text, true))));


--
-- Name: clasificacion_movimiento; Type: ROW SECURITY; Schema: finanzas; Owner: -
--

ALTER TABLE finanzas.clasificacion_movimiento ENABLE ROW LEVEL SECURITY;

--
-- Name: componente_movimiento; Type: ROW SECURITY; Schema: finanzas; Owner: -
--

ALTER TABLE finanzas.componente_movimiento ENABLE ROW LEVEL SECURITY;

--
-- Name: movimiento; Type: ROW SECURITY; Schema: finanzas; Owner: -
--

ALTER TABLE finanzas.movimiento ENABLE ROW LEVEL SECURITY;

--
-- Name: periodo_presupuesto; Type: ROW SECURITY; Schema: finanzas; Owner: -
--

ALTER TABLE finanzas.periodo_presupuesto ENABLE ROW LEVEL SECURITY;

--
-- Name: presupuesto; Type: ROW SECURITY; Schema: finanzas; Owner: -
--

ALTER TABLE finanzas.presupuesto ENABLE ROW LEVEL SECURITY;

--
-- Name: clasificacion_movimiento propietario; Type: POLICY; Schema: finanzas; Owner: -
--

CREATE POLICY propietario ON finanzas.clasificacion_movimiento TO rol_finanzas USING (((propietario_id)::text = current_setting('vera.parte_id'::text, true))) WITH CHECK (((propietario_id)::text = current_setting('vera.parte_id'::text, true)));


--
-- Name: componente_movimiento propietario; Type: POLICY; Schema: finanzas; Owner: -
--

CREATE POLICY propietario ON finanzas.componente_movimiento TO rol_finanzas USING (((propietario_id)::text = current_setting('vera.parte_id'::text, true))) WITH CHECK (((propietario_id)::text = current_setting('vera.parte_id'::text, true)));


--
-- Name: movimiento propietario; Type: POLICY; Schema: finanzas; Owner: -
--

CREATE POLICY propietario ON finanzas.movimiento TO rol_finanzas USING (((propietario_id)::text = current_setting('vera.parte_id'::text, true))) WITH CHECK (((propietario_id)::text = current_setting('vera.parte_id'::text, true)));


--
-- Name: periodo_presupuesto propietario; Type: POLICY; Schema: finanzas; Owner: -
--

CREATE POLICY propietario ON finanzas.periodo_presupuesto TO rol_finanzas USING (((propietario_id)::text = current_setting('vera.parte_id'::text, true))) WITH CHECK (((propietario_id)::text = current_setting('vera.parte_id'::text, true)));


--
-- Name: presupuesto propietario; Type: POLICY; Schema: finanzas; Owner: -
--

CREATE POLICY propietario ON finanzas.presupuesto TO rol_finanzas USING (((propietario_id)::text = current_setting('vera.parte_id'::text, true))) WITH CHECK (((propietario_id)::text = current_setting('vera.parte_id'::text, true)));


--
-- Name: regla_clasificacion propietario; Type: POLICY; Schema: finanzas; Owner: -
--

CREATE POLICY propietario ON finanzas.regla_clasificacion TO rol_finanzas USING (((propietario_id)::text = current_setting('vera.parte_id'::text, true))) WITH CHECK (((propietario_id)::text = current_setting('vera.parte_id'::text, true)));


--
-- Name: regla_clasificacion; Type: ROW SECURITY; Schema: finanzas; Owner: -
--

ALTER TABLE finanzas.regla_clasificacion ENABLE ROW LEVEL SECURITY;

--
-- Name: titular; Type: ROW SECURITY; Schema: finanzas; Owner: -
--

ALTER TABLE finanzas.titular ENABLE ROW LEVEL SECURITY;

--
-- Name: titular titular_propio; Type: POLICY; Schema: finanzas; Owner: -
--

CREATE POLICY titular_propio ON finanzas.titular TO rol_finanzas USING (((parte_id)::text = current_setting('vera.parte_id'::text, true)));


--
-- Name: destinatario_favorito; Type: ROW SECURITY; Schema: producto; Owner: -
--

ALTER TABLE producto.destinatario_favorito ENABLE ROW LEVEL SECURITY;

--
-- Name: notificacion; Type: ROW SECURITY; Schema: producto; Owner: -
--

ALTER TABLE producto.notificacion ENABLE ROW LEVEL SECURITY;

--
-- Name: preferencia; Type: ROW SECURITY; Schema: producto; Owner: -
--

ALTER TABLE producto.preferencia ENABLE ROW LEVEL SECURITY;

--
-- Name: preferencia preferencia_propia; Type: POLICY; Schema: producto; Owner: -
--

CREATE POLICY preferencia_propia ON producto.preferencia TO rol_producto USING (((parte_id)::text = current_setting('vera.parte_id'::text, true))) WITH CHECK (((parte_id)::text = current_setting('vera.parte_id'::text, true)));


--
-- Name: destinatario_favorito propietario; Type: POLICY; Schema: producto; Owner: -
--

CREATE POLICY propietario ON producto.destinatario_favorito TO rol_producto USING (((propietario_id)::text = current_setting('vera.parte_id'::text, true))) WITH CHECK (((propietario_id)::text = current_setting('vera.parte_id'::text, true)));


--
-- Name: notificacion propietario; Type: POLICY; Schema: producto; Owner: -
--

CREATE POLICY propietario ON producto.notificacion TO rol_producto USING (((propietario_id)::text = current_setting('vera.parte_id'::text, true))) WITH CHECK (((propietario_id)::text = current_setting('vera.parte_id'::text, true)));


--
-- Name: tarjeta propietario; Type: POLICY; Schema: producto; Owner: -
--

CREATE POLICY propietario ON producto.tarjeta TO rol_producto USING (((propietario_id)::text = current_setting('vera.parte_id'::text, true))) WITH CHECK (((propietario_id)::text = current_setting('vera.parte_id'::text, true)));


--
-- Name: tarjeta; Type: ROW SECURITY; Schema: producto; Owner: -
--

ALTER TABLE producto.tarjeta ENABLE ROW LEVEL SECURITY;

--
-- Name: SCHEMA asistente; Type: ACL; Schema: -; Owner: -
--

GRANT USAGE ON SCHEMA asistente TO rol_asistente;


--
-- Name: SCHEMA auth; Type: ACL; Schema: -; Owner: -
--

GRANT USAGE ON SCHEMA auth TO rol_auth;


--
-- Name: SCHEMA finanzas; Type: ACL; Schema: -; Owner: -
--

GRANT USAGE ON SCHEMA finanzas TO rol_finanzas;


--
-- Name: SCHEMA nucleo; Type: ACL; Schema: -; Owner: -
--

GRANT USAGE ON SCHEMA nucleo TO rol_nucleo;
GRANT USAGE ON SCHEMA nucleo TO rol_finanzas;


--
-- Name: SCHEMA producto; Type: ACL; Schema: -; Owner: -
--

GRANT USAGE ON SCHEMA producto TO rol_producto;


--
-- Name: FUNCTION cancelar_antes_de_borrar(); Type: ACL; Schema: asistente; Owner: -
--

REVOKE ALL ON FUNCTION asistente.cancelar_antes_de_borrar() FROM PUBLIC;


--
-- Name: FUNCTION validar_origen_accion(); Type: ACL; Schema: asistente; Owner: -
--

REVOKE ALL ON FUNCTION asistente.validar_origen_accion() FROM PUBLIC;


--
-- Name: FUNCTION validar_referencia_auditoria(); Type: ACL; Schema: asistente; Owner: -
--

REVOKE ALL ON FUNCTION asistente.validar_referencia_auditoria() FROM PUBLIC;


--
-- Name: FUNCTION validar_transicion_accion(); Type: ACL; Schema: asistente; Owner: -
--

REVOKE ALL ON FUNCTION asistente.validar_transicion_accion() FROM PUBLIC;


--
-- Name: FUNCTION auditar_clasificacion(); Type: ACL; Schema: finanzas; Owner: -
--

REVOKE ALL ON FUNCTION finanzas.auditar_clasificacion() FROM PUBLIC;
GRANT ALL ON FUNCTION finanzas.auditar_clasificacion() TO rol_finanzas;


--
-- Name: FUNCTION validar_categoria(); Type: ACL; Schema: finanzas; Owner: -
--

REVOKE ALL ON FUNCTION finanzas.validar_categoria() FROM PUBLIC;
GRANT ALL ON FUNCTION finanzas.validar_categoria() TO rol_finanzas;


--
-- Name: FUNCTION validar_categoria_visible(); Type: ACL; Schema: finanzas; Owner: -
--

REVOKE ALL ON FUNCTION finanzas.validar_categoria_visible() FROM PUBLIC;
GRANT ALL ON FUNCTION finanzas.validar_categoria_visible() TO rol_finanzas;


--
-- Name: FUNCTION validar_componente(); Type: ACL; Schema: finanzas; Owner: -
--

REVOKE ALL ON FUNCTION finanzas.validar_componente() FROM PUBLIC;
GRANT ALL ON FUNCTION finanzas.validar_componente() TO rol_finanzas;


--
-- Name: FUNCTION validar_movimiento(); Type: ACL; Schema: finanzas; Owner: -
--

REVOKE ALL ON FUNCTION finanzas.validar_movimiento() FROM PUBLIC;
GRANT ALL ON FUNCTION finanzas.validar_movimiento() TO rol_finanzas;


--
-- Name: FUNCTION validar_cuenta_tarjeta(); Type: ACL; Schema: producto; Owner: -
--

REVOKE ALL ON FUNCTION producto.validar_cuenta_tarjeta() FROM PUBLIC;


--
-- Name: TABLE accion_pendiente; Type: ACL; Schema: asistente; Owner: -
--

GRANT SELECT ON TABLE asistente.accion_pendiente TO rol_asistente;


--
-- Name: COLUMN accion_pendiente.usuario_id; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(usuario_id) ON TABLE asistente.accion_pendiente TO rol_asistente;


--
-- Name: COLUMN accion_pendiente.conversacion_id; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(conversacion_id) ON TABLE asistente.accion_pendiente TO rol_asistente;


--
-- Name: COLUMN accion_pendiente.tipo; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(tipo) ON TABLE asistente.accion_pendiente TO rol_asistente;


--
-- Name: COLUMN accion_pendiente.destino; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(destino) ON TABLE asistente.accion_pendiente TO rol_asistente;


--
-- Name: COLUMN accion_pendiente.tipo_llave; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(tipo_llave) ON TABLE asistente.accion_pendiente TO rol_asistente;


--
-- Name: COLUMN accion_pendiente.nombre_mostrado; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(nombre_mostrado) ON TABLE asistente.accion_pendiente TO rol_asistente;


--
-- Name: COLUMN accion_pendiente.celular_enmascarado; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(celular_enmascarado) ON TABLE asistente.accion_pendiente TO rol_asistente;


--
-- Name: COLUMN accion_pendiente.entidad_destino; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(entidad_destino) ON TABLE asistente.accion_pendiente TO rol_asistente;


--
-- Name: COLUMN accion_pendiente.monto; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(monto) ON TABLE asistente.accion_pendiente TO rol_asistente;


--
-- Name: COLUMN accion_pendiente.requiere_codigo; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(requiere_codigo),UPDATE(requiere_codigo) ON TABLE asistente.accion_pendiente TO rol_asistente;


--
-- Name: COLUMN accion_pendiente.desafio_id; Type: ACL; Schema: asistente; Owner: -
--

GRANT UPDATE(desafio_id) ON TABLE asistente.accion_pendiente TO rol_asistente;


--
-- Name: COLUMN accion_pendiente.estado; Type: ACL; Schema: asistente; Owner: -
--

GRANT UPDATE(estado) ON TABLE asistente.accion_pendiente TO rol_asistente;


--
-- Name: COLUMN accion_pendiente.transferencia_id; Type: ACL; Schema: asistente; Owner: -
--

GRANT UPDATE(transferencia_id) ON TABLE asistente.accion_pendiente TO rol_asistente;


--
-- Name: COLUMN accion_pendiente.codigo_error; Type: ACL; Schema: asistente; Owner: -
--

GRANT UPDATE(codigo_error) ON TABLE asistente.accion_pendiente TO rol_asistente;


--
-- Name: COLUMN accion_pendiente.resuelta_en; Type: ACL; Schema: asistente; Owner: -
--

GRANT UPDATE(resuelta_en) ON TABLE asistente.accion_pendiente TO rol_asistente;


--
-- Name: COLUMN accion_pendiente.usuario_financiero_id; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(usuario_financiero_id) ON TABLE asistente.accion_pendiente TO rol_asistente;


--
-- Name: TABLE conversacion; Type: ACL; Schema: asistente; Owner: -
--

GRANT SELECT,INSERT,DELETE ON TABLE asistente.conversacion TO rol_asistente;


--
-- Name: COLUMN conversacion.titulo; Type: ACL; Schema: asistente; Owner: -
--

GRANT UPDATE(titulo) ON TABLE asistente.conversacion TO rol_asistente;


--
-- Name: COLUMN conversacion.estado; Type: ACL; Schema: asistente; Owner: -
--

GRANT UPDATE(estado) ON TABLE asistente.conversacion TO rol_asistente;


--
-- Name: COLUMN conversacion.cantidad_mensajes; Type: ACL; Schema: asistente; Owner: -
--

GRANT UPDATE(cantidad_mensajes) ON TABLE asistente.conversacion TO rol_asistente;


--
-- Name: COLUMN conversacion.ultimo_mensaje_en; Type: ACL; Schema: asistente; Owner: -
--

GRANT UPDATE(ultimo_mensaje_en) ON TABLE asistente.conversacion TO rol_asistente;


--
-- Name: COLUMN conversacion.ciclo_token; Type: ACL; Schema: asistente; Owner: -
--

GRANT UPDATE(ciclo_token) ON TABLE asistente.conversacion TO rol_asistente;


--
-- Name: COLUMN conversacion.ciclo_expira_en; Type: ACL; Schema: asistente; Owner: -
--

GRANT UPDATE(ciclo_expira_en) ON TABLE asistente.conversacion TO rol_asistente;


--
-- Name: COLUMN conversacion.usuario_financiero_id; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(usuario_financiero_id) ON TABLE asistente.conversacion TO rol_asistente;


--
-- Name: TABLE ejecucion_proveedor; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT ON TABLE asistente.ejecucion_proveedor TO rol_asistente;


--
-- Name: COLUMN llamada_funcion.llamada_id; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(llamada_id) ON TABLE asistente.llamada_funcion TO rol_asistente;


--
-- Name: COLUMN llamada_funcion.fase; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(fase) ON TABLE asistente.llamada_funcion TO rol_asistente;


--
-- Name: COLUMN llamada_funcion.usuario_id; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(usuario_id) ON TABLE asistente.llamada_funcion TO rol_asistente;


--
-- Name: COLUMN llamada_funcion.conversacion_id; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(conversacion_id) ON TABLE asistente.llamada_funcion TO rol_asistente;


--
-- Name: COLUMN llamada_funcion.mensaje_id; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(mensaje_id) ON TABLE asistente.llamada_funcion TO rol_asistente;


--
-- Name: COLUMN llamada_funcion.funcion; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(funcion) ON TABLE asistente.llamada_funcion TO rol_asistente;


--
-- Name: COLUMN llamada_funcion.argumentos_saneados; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(argumentos_saneados) ON TABLE asistente.llamada_funcion TO rol_asistente;


--
-- Name: COLUMN llamada_funcion.resultado; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(resultado) ON TABLE asistente.llamada_funcion TO rol_asistente;


--
-- Name: COLUMN llamada_funcion.duracion_ms; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(duracion_ms) ON TABLE asistente.llamada_funcion TO rol_asistente;


--
-- Name: COLUMN llamada_funcion.request_id; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(request_id) ON TABLE asistente.llamada_funcion TO rol_asistente;


--
-- Name: COLUMN llamada_funcion.version_instrucciones; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(version_instrucciones) ON TABLE asistente.llamada_funcion TO rol_asistente;


--
-- Name: TABLE mensaje; Type: ACL; Schema: asistente; Owner: -
--

GRANT SELECT,INSERT ON TABLE asistente.mensaje TO rol_asistente;


--
-- Name: COLUMN mensaje.accion_id; Type: ACL; Schema: asistente; Owner: -
--

GRANT INSERT(accion_id) ON TABLE asistente.mensaje TO rol_asistente;


--
-- Name: TABLE solicitud_texto; Type: ACL; Schema: asistente; Owner: -
--

GRANT SELECT,INSERT ON TABLE asistente.solicitud_texto TO rol_asistente;


--
-- Name: TABLE codigos_respaldo; Type: ACL; Schema: auth; Owner: -
--

GRANT SELECT,INSERT ON TABLE auth.codigos_respaldo TO rol_auth;


--
-- Name: COLUMN codigos_respaldo.usado_en; Type: ACL; Schema: auth; Owner: -
--

GRANT UPDATE(usado_en) ON TABLE auth.codigos_respaldo TO rol_auth;


--
-- Name: TABLE consentimientos; Type: ACL; Schema: auth; Owner: -
--

GRANT SELECT,INSERT ON TABLE auth.consentimientos TO rol_auth;


--
-- Name: SEQUENCE consentimientos_orden_seq; Type: ACL; Schema: auth; Owner: -
--

GRANT SELECT,USAGE ON SEQUENCE auth.consentimientos_orden_seq TO rol_auth;


--
-- Name: TABLE credenciales; Type: ACL; Schema: auth; Owner: -
--

GRANT SELECT,INSERT,UPDATE ON TABLE auth.credenciales TO rol_auth;


--
-- Name: TABLE desafios; Type: ACL; Schema: auth; Owner: -
--

GRANT SELECT,INSERT ON TABLE auth.desafios TO rol_auth;


--
-- Name: COLUMN desafios.codigo_hmac; Type: ACL; Schema: auth; Owner: -
--

GRANT UPDATE(codigo_hmac) ON TABLE auth.desafios TO rol_auth;


--
-- Name: COLUMN desafios.intentos; Type: ACL; Schema: auth; Owner: -
--

GRANT UPDATE(intentos) ON TABLE auth.desafios TO rol_auth;


--
-- Name: COLUMN desafios.reenvios; Type: ACL; Schema: auth; Owner: -
--

GRANT UPDATE(reenvios) ON TABLE auth.desafios TO rol_auth;


--
-- Name: COLUMN desafios.estado; Type: ACL; Schema: auth; Owner: -
--

GRANT UPDATE(estado) ON TABLE auth.desafios TO rol_auth;


--
-- Name: COLUMN desafios.expira_en; Type: ACL; Schema: auth; Owner: -
--

GRANT UPDATE(expira_en) ON TABLE auth.desafios TO rol_auth;


--
-- Name: COLUMN desafios.ultimo_envio_en; Type: ACL; Schema: auth; Owner: -
--

GRANT UPDATE(ultimo_envio_en) ON TABLE auth.desafios TO rol_auth;


--
-- Name: TABLE empresas; Type: ACL; Schema: auth; Owner: -
--

GRANT SELECT,INSERT,UPDATE ON TABLE auth.empresas TO rol_auth;


--
-- Name: TABLE eventos_autenticacion; Type: ACL; Schema: auth; Owner: -
--

GRANT SELECT,INSERT ON TABLE auth.eventos_autenticacion TO rol_auth;


--
-- Name: TABLE kyc_estados; Type: ACL; Schema: auth; Owner: -
--

GRANT SELECT,INSERT,UPDATE ON TABLE auth.kyc_estados TO rol_auth;


--
-- Name: TABLE kyc_verificaciones; Type: ACL; Schema: auth; Owner: -
--

GRANT SELECT,INSERT ON TABLE auth.kyc_verificaciones TO rol_auth;


--
-- Name: TABLE outbox; Type: ACL; Schema: auth; Owner: -
--

GRANT SELECT,INSERT ON TABLE auth.outbox TO rol_auth;


--
-- Name: COLUMN outbox.disponible_en; Type: ACL; Schema: auth; Owner: -
--

GRANT UPDATE(disponible_en) ON TABLE auth.outbox TO rol_auth;


--
-- Name: COLUMN outbox.intentos; Type: ACL; Schema: auth; Owner: -
--

GRANT UPDATE(intentos) ON TABLE auth.outbox TO rol_auth;


--
-- Name: COLUMN outbox.ultimo_error; Type: ACL; Schema: auth; Owner: -
--

GRANT UPDATE(ultimo_error) ON TABLE auth.outbox TO rol_auth;


--
-- Name: COLUMN outbox.despachado_en; Type: ACL; Schema: auth; Owner: -
--

GRANT UPDATE(despachado_en) ON TABLE auth.outbox TO rol_auth;


--
-- Name: COLUMN outbox.id_mensaje_proveedor; Type: ACL; Schema: auth; Owner: -
--

GRANT UPDATE(id_mensaje_proveedor) ON TABLE auth.outbox TO rol_auth;


--
-- Name: TABLE partes; Type: ACL; Schema: auth; Owner: -
--

GRANT SELECT,INSERT,UPDATE ON TABLE auth.partes TO rol_auth;


--
-- Name: TABLE personas; Type: ACL; Schema: auth; Owner: -
--

GRANT SELECT,INSERT,UPDATE ON TABLE auth.personas TO rol_auth;


--
-- Name: TABLE roles_de_parte; Type: ACL; Schema: auth; Owner: -
--

GRANT SELECT,INSERT,UPDATE ON TABLE auth.roles_de_parte TO rol_auth;


--
-- Name: TABLE sesiones; Type: ACL; Schema: auth; Owner: -
--

GRANT SELECT,INSERT ON TABLE auth.sesiones TO rol_auth;


--
-- Name: COLUMN sesiones.contexto_activo; Type: ACL; Schema: auth; Owner: -
--

GRANT UPDATE(contexto_activo) ON TABLE auth.sesiones TO rol_auth;


--
-- Name: COLUMN sesiones.ultimo_uso_en; Type: ACL; Schema: auth; Owner: -
--

GRANT UPDATE(ultimo_uso_en) ON TABLE auth.sesiones TO rol_auth;


--
-- Name: COLUMN sesiones.usado_en; Type: ACL; Schema: auth; Owner: -
--

GRANT UPDATE(usado_en) ON TABLE auth.sesiones TO rol_auth;


--
-- Name: COLUMN sesiones.revocada_en; Type: ACL; Schema: auth; Owner: -
--

GRANT UPDATE(revocada_en) ON TABLE auth.sesiones TO rol_auth;


--
-- Name: TABLE textos_consentimiento; Type: ACL; Schema: auth; Owner: -
--

GRANT SELECT ON TABLE auth.textos_consentimiento TO rol_auth;


--
-- Name: TABLE tokens_restablecimiento; Type: ACL; Schema: auth; Owner: -
--

GRANT SELECT,INSERT ON TABLE auth.tokens_restablecimiento TO rol_auth;


--
-- Name: COLUMN tokens_restablecimiento.usado_en; Type: ACL; Schema: auth; Owner: -
--

GRANT UPDATE(usado_en) ON TABLE auth.tokens_restablecimiento TO rol_auth;


--
-- Name: TABLE transiciones_credencial; Type: ACL; Schema: auth; Owner: -
--

GRANT SELECT,INSERT ON TABLE auth.transiciones_credencial TO rol_auth;


--
-- Name: TABLE cambio_clasificacion; Type: ACL; Schema: finanzas; Owner: -
--

GRANT SELECT,INSERT ON TABLE finanzas.cambio_clasificacion TO rol_finanzas;


--
-- Name: SEQUENCE cambio_clasificacion_id_seq; Type: ACL; Schema: finanzas; Owner: -
--

GRANT SELECT,USAGE ON SEQUENCE finanzas.cambio_clasificacion_id_seq TO rol_finanzas;


--
-- Name: TABLE categoria; Type: ACL; Schema: finanzas; Owner: -
--

GRANT SELECT,INSERT,UPDATE ON TABLE finanzas.categoria TO rol_finanzas;


--
-- Name: TABLE clasificacion_movimiento; Type: ACL; Schema: finanzas; Owner: -
--

GRANT SELECT,INSERT,UPDATE ON TABLE finanzas.clasificacion_movimiento TO rol_finanzas;


--
-- Name: TABLE componente_movimiento; Type: ACL; Schema: finanzas; Owner: -
--

GRANT SELECT,INSERT,UPDATE ON TABLE finanzas.componente_movimiento TO rol_finanzas;


--
-- Name: TABLE movimiento; Type: ACL; Schema: finanzas; Owner: -
--

GRANT SELECT,INSERT,UPDATE ON TABLE finanzas.movimiento TO rol_finanzas;


--
-- Name: TABLE periodo_presupuesto; Type: ACL; Schema: finanzas; Owner: -
--

GRANT SELECT,INSERT,UPDATE ON TABLE finanzas.periodo_presupuesto TO rol_finanzas;


--
-- Name: TABLE presupuesto; Type: ACL; Schema: finanzas; Owner: -
--

GRANT SELECT,INSERT,UPDATE ON TABLE finanzas.presupuesto TO rol_finanzas;


--
-- Name: TABLE regla_clasificacion; Type: ACL; Schema: finanzas; Owner: -
--

GRANT SELECT,INSERT,UPDATE ON TABLE finanzas.regla_clasificacion TO rol_finanzas;


--
-- Name: TABLE titular; Type: ACL; Schema: finanzas; Owner: -
--

GRANT SELECT ON TABLE finanzas.titular TO rol_finanzas;


--
-- Name: TABLE conciliacion_corrida; Type: ACL; Schema: nucleo; Owner: -
--

GRANT SELECT,INSERT ON TABLE nucleo.conciliacion_corrida TO rol_nucleo;


--
-- Name: TABLE cuenta; Type: ACL; Schema: nucleo; Owner: -
--

GRANT SELECT,INSERT ON TABLE nucleo.cuenta TO rol_nucleo;
GRANT SELECT ON TABLE nucleo.cuenta TO rol_finanzas;


--
-- Name: COLUMN cuenta.id; Type: ACL; Schema: nucleo; Owner: -
--

GRANT UPDATE(id) ON TABLE nucleo.cuenta TO rol_nucleo;


--
-- Name: TABLE deposito; Type: ACL; Schema: nucleo; Owner: -
--

GRANT SELECT,INSERT ON TABLE nucleo.deposito TO rol_nucleo;
GRANT SELECT ON TABLE nucleo.deposito TO rol_finanzas;


--
-- Name: COLUMN deposito.cus; Type: ACL; Schema: nucleo; Owner: -
--

GRANT UPDATE(cus) ON TABLE nucleo.deposito TO rol_nucleo;


--
-- Name: COLUMN deposito.estado; Type: ACL; Schema: nucleo; Owner: -
--

GRANT UPDATE(estado) ON TABLE nucleo.deposito TO rol_nucleo;


--
-- Name: COLUMN deposito.actualizada_en; Type: ACL; Schema: nucleo; Owner: -
--

GRANT UPDATE(actualizada_en) ON TABLE nucleo.deposito TO rol_nucleo;


--
-- Name: TABLE descuadre; Type: ACL; Schema: nucleo; Owner: -
--

GRANT SELECT,INSERT ON TABLE nucleo.descuadre TO rol_nucleo;


--
-- Name: COLUMN descuadre.resuelto_en; Type: ACL; Schema: nucleo; Owner: -
--

GRANT UPDATE(resuelto_en) ON TABLE nucleo.descuadre TO rol_nucleo;


--
-- Name: COLUMN descuadre.resolucion; Type: ACL; Schema: nucleo; Owner: -
--

GRANT UPDATE(resolucion) ON TABLE nucleo.descuadre TO rol_nucleo;


--
-- Name: TABLE detalle_transferencia; Type: ACL; Schema: nucleo; Owner: -
--

GRANT SELECT ON TABLE nucleo.detalle_transferencia TO rol_finanzas;
GRANT SELECT,INSERT ON TABLE nucleo.detalle_transferencia TO rol_nucleo;


--
-- Name: TABLE evento_custodio; Type: ACL; Schema: nucleo; Owner: -
--

GRANT SELECT,INSERT ON TABLE nucleo.evento_custodio TO rol_nucleo;


--
-- Name: COLUMN evento_custodio.resultado; Type: ACL; Schema: nucleo; Owner: -
--

GRANT UPDATE(resultado) ON TABLE nucleo.evento_custodio TO rol_nucleo;


--
-- Name: COLUMN evento_custodio.codigo_resultado; Type: ACL; Schema: nucleo; Owner: -
--

GRANT UPDATE(codigo_resultado) ON TABLE nucleo.evento_custodio TO rol_nucleo;


--
-- Name: COLUMN evento_custodio.procesado_en; Type: ACL; Schema: nucleo; Owner: -
--

GRANT UPDATE(procesado_en) ON TABLE nucleo.evento_custodio TO rol_nucleo;


--
-- Name: TABLE evento_transferencia; Type: ACL; Schema: nucleo; Owner: -
--

GRANT SELECT,INSERT ON TABLE nucleo.evento_transferencia TO rol_nucleo;


--
-- Name: SEQUENCE evento_transferencia_id_seq; Type: ACL; Schema: nucleo; Owner: -
--

GRANT SELECT,USAGE ON SEQUENCE nucleo.evento_transferencia_id_seq TO rol_nucleo;


--
-- Name: TABLE extracto_custodio; Type: ACL; Schema: nucleo; Owner: -
--

GRANT SELECT,INSERT ON TABLE nucleo.extracto_custodio TO rol_nucleo;


--
-- Name: TABLE journal_entry; Type: ACL; Schema: nucleo; Owner: -
--

GRANT SELECT,INSERT ON TABLE nucleo.journal_entry TO rol_nucleo;
GRANT SELECT ON TABLE nucleo.journal_entry TO rol_finanzas;


--
-- Name: TABLE outbox; Type: ACL; Schema: nucleo; Owner: -
--

GRANT SELECT,INSERT ON TABLE nucleo.outbox TO rol_nucleo;


--
-- Name: COLUMN outbox.disponible_en; Type: ACL; Schema: nucleo; Owner: -
--

GRANT UPDATE(disponible_en) ON TABLE nucleo.outbox TO rol_nucleo;


--
-- Name: COLUMN outbox.intentos; Type: ACL; Schema: nucleo; Owner: -
--

GRANT UPDATE(intentos) ON TABLE nucleo.outbox TO rol_nucleo;


--
-- Name: COLUMN outbox.ultimo_error; Type: ACL; Schema: nucleo; Owner: -
--

GRANT UPDATE(ultimo_error) ON TABLE nucleo.outbox TO rol_nucleo;


--
-- Name: COLUMN outbox.despachado_en; Type: ACL; Schema: nucleo; Owner: -
--

GRANT UPDATE(despachado_en) ON TABLE nucleo.outbox TO rol_nucleo;


--
-- Name: TABLE posting; Type: ACL; Schema: nucleo; Owner: -
--

GRANT SELECT,INSERT ON TABLE nucleo.posting TO rol_nucleo;
GRANT SELECT ON TABLE nucleo.posting TO rol_finanzas;


--
-- Name: TABLE regla_limite; Type: ACL; Schema: nucleo; Owner: -
--

GRANT SELECT ON TABLE nucleo.regla_limite TO rol_nucleo;


--
-- Name: TABLE transferencia; Type: ACL; Schema: nucleo; Owner: -
--

GRANT SELECT,INSERT ON TABLE nucleo.transferencia TO rol_nucleo;
GRANT SELECT ON TABLE nucleo.transferencia TO rol_finanzas;


--
-- Name: COLUMN transferencia.estado; Type: ACL; Schema: nucleo; Owner: -
--

GRANT UPDATE(estado) ON TABLE nucleo.transferencia TO rol_nucleo;


--
-- Name: COLUMN transferencia.actualizada_en; Type: ACL; Schema: nucleo; Owner: -
--

GRANT UPDATE(actualizada_en) ON TABLE nucleo.transferencia TO rol_nucleo;


--
-- Name: TABLE valor_uvt; Type: ACL; Schema: nucleo; Owner: -
--

GRANT SELECT ON TABLE nucleo.valor_uvt TO rol_nucleo;


--
-- Name: TABLE destinatario_favorito; Type: ACL; Schema: producto; Owner: -
--

GRANT SELECT,INSERT,UPDATE ON TABLE producto.destinatario_favorito TO rol_producto;


--
-- Name: TABLE notificacion; Type: ACL; Schema: producto; Owner: -
--

GRANT SELECT,INSERT,UPDATE ON TABLE producto.notificacion TO rol_producto;


--
-- Name: TABLE preferencia; Type: ACL; Schema: producto; Owner: -
--

GRANT SELECT,INSERT,UPDATE ON TABLE producto.preferencia TO rol_producto;


--
-- Name: TABLE tarjeta; Type: ACL; Schema: producto; Owner: -
--

GRANT SELECT,INSERT,UPDATE ON TABLE producto.tarjeta TO rol_producto;


--
-- Name: TABLE users; Type: ACL; Schema: public; Owner: -
--

GRANT SELECT ON TABLE public.users TO rol_api;


--
-- PostgreSQL database dump complete
--

\unrestrict u89teAYQRWOderh30vA29coaI9s4jjPeUv5sP5BWltrfqYhXdONxAoGjb3OO8Sp


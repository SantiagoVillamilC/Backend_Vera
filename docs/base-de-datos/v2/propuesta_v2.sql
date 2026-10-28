-- PROPUESTA V2. DDL de referencia, NO migración instalada ni cambio del API.
-- Aplicar únicamente después de las 42 migraciones actuales, en una base de ensayo.
-- En implementación se dividirá por módulo y fase; no modificar migraciones existentes.
BEGIN;
CREATE SCHEMA finanzas;
CREATE SCHEMA producto;
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- Conserva el resultado de resolver un destino: el directorio puede cambiar después.
CREATE TABLE nucleo.detalle_transferencia (
    transferencia_id uuid PRIMARY KEY REFERENCES nucleo.transferencia(id),
    actor_id uuid REFERENCES auth.partes(id),
    canal text NOT NULL CHECK (canal IN ('app','asistente','api','legado')),
    tipo_destino text NOT NULL CHECK (tipo_destino IN ('interna','bre_b','desconocido')),
    tipo_llave text CHECK (tipo_llave IN ('documento','celular','correo','alfanumerico','comercio')),
    destinatario_id uuid REFERENCES auth.partes(id),
    nombre_enmascarado text,
    entidad_destino text,
    referencia_externa text,
    request_id varchar(128),
    accion_ia_id uuid, -- Correlación sin FK: debe sobrevivir al borrado de conversaciones.
    confirmada_por_usuario_en timestamptz,
    creada_en timestamptz NOT NULL DEFAULT now(),
    CHECK ((tipo_destino = 'bre_b') = (tipo_llave IS NOT NULL)),
    CHECK (tipo_destino <> 'interna' OR destinatario_id IS NOT NULL)
);
CREATE INDEX detalle_transferencia_destino_idx ON nucleo.detalle_transferencia(destinatario_id,creada_en DESC);

CREATE TABLE nucleo.evento_transferencia (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    transferencia_id uuid NOT NULL REFERENCES nucleo.transferencia(id),
    estado_anterior text,
    estado_nuevo text NOT NULL CHECK (estado_nuevo IN ('creada','reservada','despachada','confirmada','fallida','indeterminada','compensada')),
    codigo_resultado varchar(80),
    request_id varchar(128),
    ocurrido_en timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX evento_transferencia_idx ON nucleo.evento_transferencia(transferencia_id,id);

-- Inbox del callback; jamás usar payload crudo ni tokens del custodio.
CREATE TABLE nucleo.evento_custodio (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    proveedor text NOT NULL,
    evento_externo_id text NOT NULL,
    deposito_id uuid REFERENCES nucleo.deposito(id),
    transferencia_id uuid REFERENCES nucleo.transferencia(id),
    huella bytea NOT NULL CHECK (octet_length(huella)=32),
    resultado text NOT NULL CHECK (resultado IN ('recibido','procesado','rechazado')),
    codigo_resultado varchar(80),
    ocurrido_en timestamptz NOT NULL,
    recibido_en timestamptz NOT NULL DEFAULT now(),
    procesado_en timestamptz,
    UNIQUE(proveedor,evento_externo_id),
    CHECK (num_nonnulls(deposito_id,transferencia_id)=1),
    CHECK ((resultado='recibido') = (procesado_en IS NULL))
);

-- Identidad financiera canónica: conserva los IDs text del núcleo sin cambiar su tipo.
-- Mapeo verificado explícitamente; no convertir titulares heredados a uuid a ciegas.
CREATE TABLE finanzas.titular (
    usuario_financiero_id text PRIMARY KEY,
    parte_id uuid NOT NULL UNIQUE REFERENCES auth.partes(id),
    creada_en timestamptz NOT NULL DEFAULT now(),
    UNIQUE(usuario_financiero_id,parte_id)
);

-- Proyección reconstruible de la misma agrupación que Historial: transferencia o entry.
-- Solo cuenta disponible. El saldo y los importes NO se copian aquí.
CREATE TABLE finanzas.movimiento (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    propietario_id uuid NOT NULL REFERENCES auth.partes(id),
    usuario_financiero_id text NOT NULL,
    cuenta_id text NOT NULL REFERENCES nucleo.cuenta(id),
    transferencia_id uuid REFERENCES nucleo.transferencia(id),
    entry_sin_transferencia_id bigint REFERENCES nucleo.journal_entry(id),
    ocurrido_en timestamptz NOT NULL,
    version_proyeccion integer NOT NULL DEFAULT 1 CHECK (version_proyeccion>0),
    proyectado_en timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY(usuario_financiero_id,propietario_id) REFERENCES finanzas.titular(usuario_financiero_id,parte_id),
    CHECK (num_nonnulls(transferencia_id,entry_sin_transferencia_id)=1),
    UNIQUE(id,propietario_id)
);
CREATE UNIQUE INDEX movimiento_transferencia_uk ON finanzas.movimiento(propietario_id,transferencia_id) WHERE transferencia_id IS NOT NULL;
CREATE UNIQUE INDEX movimiento_entry_uk ON finanzas.movimiento(propietario_id,entry_sin_transferencia_id) WHERE entry_sin_transferencia_id IS NOT NULL;
CREATE INDEX movimiento_periodo_idx ON finanzas.movimiento(propietario_id,ocurrido_en DESC,id DESC);

CREATE TABLE finanzas.componente_movimiento (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    movimiento_id uuid NOT NULL,
    propietario_id uuid NOT NULL,
    posting_id bigint NOT NULL UNIQUE REFERENCES nucleo.posting(id),
    motivo text NOT NULL CHECK (motivo IN ('deposito','transferencia_recibida','envio','gravamen','devolucion','devolucion_gravamen')),
    FOREIGN KEY(movimiento_id,propietario_id) REFERENCES finanzas.movimiento(id,propietario_id),
    UNIQUE(id,propietario_id)
);
CREATE INDEX componente_movimiento_idx ON finanzas.componente_movimiento(movimiento_id);

CREATE TABLE finanzas.categoria (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    propietario_id uuid REFERENCES auth.partes(id), -- NULL: catálogo común; UUID: categoría privada.
    codigo varchar(60) NOT NULL,
    nombre varchar(80) NOT NULL CHECK (length(btrim(nombre))>0),
    padre_id uuid REFERENCES finanzas.categoria(id),
    naturaleza text NOT NULL CHECK (naturaleza IN ('ingreso','gasto','traslado','ajuste')),
    icono varchar(60),
    color varchar(7) CHECK (color ~ '^#[0-9A-Fa-f]{6}$'),
    activa boolean NOT NULL DEFAULT true,
    creada_en timestamptz NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (propietario_id,codigo)
);
CREATE INDEX categoria_propietario_idx ON finanzas.categoria(propietario_id,padre_id);

-- Un componente puede seguir sin clasificar. Pendiente NO es ingreso ni gasto.
CREATE TABLE finanzas.clasificacion_movimiento (
    componente_id uuid PRIMARY KEY,
    propietario_id uuid NOT NULL,
    categoria_id uuid REFERENCES finanzas.categoria(id),
    naturaleza text NOT NULL DEFAULT 'pendiente' CHECK (naturaleza IN ('ingreso','gasto','traslado','ajuste','pendiente')),
    fuente text NOT NULL CHECK (fuente IN ('usuario','regla','ia','sistema')),
    confianza_ppm integer CHECK (confianza_ppm BETWEEN 0 AND 1000000),
    modelo varchar(120),
    version_clasificador varchar(80),
    version integer NOT NULL DEFAULT 1 CHECK (version>0),
    actor_id uuid REFERENCES auth.partes(id),
    actualizada_en timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY(componente_id,propietario_id) REFERENCES finanzas.componente_movimiento(id,propietario_id),
    CHECK ((naturaleza='pendiente') = (categoria_id IS NULL)),
    CHECK (fuente <> 'usuario' OR actor_id IS NOT NULL),
    CHECK (fuente <> 'ia' OR (modelo IS NOT NULL AND version_clasificador IS NOT NULL AND confianza_ppm IS NOT NULL))
);
CREATE INDEX clasificacion_categoria_idx ON finanzas.clasificacion_movimiento(propietario_id,categoria_id,componente_id);

CREATE TABLE finanzas.cambio_clasificacion (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    componente_id uuid NOT NULL REFERENCES finanzas.componente_movimiento(id),
    version integer NOT NULL,
    anterior jsonb,
    nueva jsonb NOT NULL,
    ocurrida_en timestamptz NOT NULL DEFAULT now(),
    UNIQUE(componente_id,version)
);

CREATE TABLE finanzas.regla_clasificacion (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    propietario_id uuid NOT NULL REFERENCES auth.partes(id),
    contraparte_hmac bytea NOT NULL CHECK (octet_length(contraparte_hmac)=32),
    version_clave integer NOT NULL CHECK (version_clave>0),
    motivo text NOT NULL CHECK (motivo IN ('deposito','transferencia_recibida','envio','gravamen','devolucion','devolucion_gravamen')),
    categoria_id uuid NOT NULL REFERENCES finanzas.categoria(id),
    activa boolean NOT NULL DEFAULT true,
    creada_en timestamptz NOT NULL DEFAULT now(),
    UNIQUE(propietario_id,contraparte_hmac,version_clave,motivo)
);

CREATE TABLE finanzas.presupuesto (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    propietario_id uuid NOT NULL REFERENCES auth.partes(id),
    categoria_id uuid REFERENCES finanzas.categoria(id), -- NULL: global, siempre gastos.
    nombre varchar(80) NOT NULL,
    periodicidad text NOT NULL CHECK (periodicidad IN ('semanal','mensual','anual','unico')),
    zona_horaria text NOT NULL DEFAULT 'America/Bogota' CHECK (zona_horaria='America/Bogota'),
    activo boolean NOT NULL DEFAULT true,
    creado_en timestamptz NOT NULL DEFAULT now(),
    UNIQUE(id,propietario_id)
);
CREATE TABLE finanzas.periodo_presupuesto (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    presupuesto_id uuid NOT NULL,
    propietario_id uuid NOT NULL,
    desde date NOT NULL,
    hasta_exclusivo date NOT NULL,
    limite_centavos bigint NOT NULL CHECK (limite_centavos>0),
    umbral_porcentaje smallint NOT NULL DEFAULT 80 CHECK (umbral_porcentaje BETWEEN 1 AND 100),
    FOREIGN KEY(presupuesto_id,propietario_id) REFERENCES finanzas.presupuesto(id,propietario_id),
    CHECK (desde<hasta_exclusivo),
    UNIQUE(presupuesto_id,desde),
    EXCLUDE USING gist (presupuesto_id WITH =, daterange(desde,hasta_exclusivo,'[)') WITH &&)
);
CREATE INDEX periodo_presupuesto_idx ON finanzas.periodo_presupuesto(propietario_id,desde,hasta_exclusivo);

CREATE TABLE producto.preferencia (
    parte_id uuid PRIMARY KEY REFERENCES auth.partes(id),
    idioma text NOT NULL DEFAULT 'es-CO',
    zona_horaria text NOT NULL DEFAULT 'America/Bogota',
    voz_preferida varchar(80),
    leer_respuestas boolean NOT NULL DEFAULT false,
    avisos_presupuesto boolean NOT NULL DEFAULT true,
    actualizada_en timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE producto.notificacion (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    propietario_id uuid NOT NULL REFERENCES auth.partes(id),
    tipo text NOT NULL CHECK (tipo IN ('transferencia','deposito','presupuesto','seguridad')),
    clave_deduplicacion text NOT NULL,
    referencia_id text NOT NULL,
    creada_en timestamptz NOT NULL DEFAULT now(),
    leida_en timestamptz,
    UNIQUE(propietario_id,tipo,clave_deduplicacion)
);
CREATE INDEX notificacion_reciente_idx ON producto.notificacion(propietario_id,creada_en DESC,id DESC);

CREATE TABLE producto.destinatario_favorito (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    propietario_id uuid NOT NULL REFERENCES auth.partes(id),
    destinatario_id uuid REFERENCES auth.partes(id),
    alias varchar(80) NOT NULL,
    tipo_llave text CHECK (tipo_llave IN ('documento','celular','correo','alfanumerico','comercio')),
    llave_cifrada bytea,
    llave_hmac bytea,
    version_clave integer,
    creado_en timestamptz NOT NULL DEFAULT now(),
    CHECK ((destinatario_id IS NOT NULL AND tipo_llave IS NULL AND llave_cifrada IS NULL AND llave_hmac IS NULL AND version_clave IS NULL)
       OR (destinatario_id IS NULL AND tipo_llave IS NOT NULL AND llave_cifrada IS NOT NULL AND llave_hmac IS NOT NULL AND version_clave IS NOT NULL AND octet_length(llave_hmac)=32 AND version_clave>0))
);
CREATE UNIQUE INDEX favorito_interno_uk ON producto.destinatario_favorito(propietario_id,destinatario_id) WHERE destinatario_id IS NOT NULL;
CREATE UNIQUE INDEX favorito_llave_uk ON producto.destinatario_favorito(propietario_id,tipo_llave,llave_hmac,version_clave) WHERE tipo_llave IS NOT NULL;

-- Reserva de diseño para Tarjetas (hoy WIP): solo referencias tokenizadas del emisor.
CREATE TABLE producto.tarjeta (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    propietario_id uuid NOT NULL REFERENCES auth.partes(id),
    cuenta_id text NOT NULL REFERENCES nucleo.cuenta(id),
    proveedor text NOT NULL,
    referencia_proveedor text NOT NULL,
    ultimos_cuatro char(4) NOT NULL CHECK (ultimos_cuatro ~ '^[0-9]{4}$'),
    marca varchar(40) NOT NULL,
    tipo text NOT NULL CHECK (tipo IN ('fisica','virtual')),
    estado text NOT NULL CHECK (estado IN ('solicitada','activa','bloqueada','cancelada','vencida')),
    vence_mes smallint NOT NULL CHECK (vence_mes BETWEEN 1 AND 12),
    vence_anio smallint NOT NULL CHECK (vence_anio BETWEEN 2000 AND 9999),
    creada_en timestamptz NOT NULL DEFAULT now(),
    UNIQUE(proveedor,referencia_proveedor)
);
CREATE FUNCTION producto.validar_cuenta_tarjeta() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
    IF NOT EXISTS(SELECT 1 FROM nucleo.cuenta c JOIN finanzas.titular t
       ON t.usuario_financiero_id=c.titular_id
       WHERE c.id=NEW.cuenta_id AND t.parte_id=NEW.propietario_id
       AND c.id='pasivo:usuario:'||c.titular_id||':disponible' AND c.moneda='COP') THEN
        RAISE EXCEPTION 'Cuenta de tarjeta ajena o no disponible' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION producto.validar_cuenta_tarjeta() FROM PUBLIC;
CREATE TRIGGER tarjeta_cuenta_valida BEFORE INSERT OR UPDATE ON producto.tarjeta
    FOR EACH ROW EXECUTE FUNCTION producto.validar_cuenta_tarjeta();

-- Intentos reales de proveedor para diagnosticar latencia, bloqueo y cuota.
-- No almacenar prompts, audio, API key ni respuestas crudas.
CREATE TABLE asistente.ejecucion_proveedor (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    turno_id uuid NOT NULL,
    usuario_id uuid NOT NULL REFERENCES auth.partes(id),
    usuario_financiero_id text NOT NULL,
    conversacion_id uuid, -- Sin FK; métricas mínimas sobreviven al historial.
    mensaje_id uuid,
    operacion text NOT NULL CHECK (operacion IN ('chat','transcripcion','sintesis')),
    proveedor varchar(80) NOT NULL,
    modelo varchar(120) NOT NULL,
    version_instrucciones varchar(80),
    intento smallint NOT NULL CHECK (intento>0),
    resultado text NOT NULL CHECK (resultado IN ('ok','cuota','seguridad','timeout','cancelada','error')),
    codigo_proveedor varchar(80),
    reintentar_en timestamptz,
    tokens_entrada bigint CHECK (tokens_entrada>=0),
    tokens_salida bigint CHECK (tokens_salida>=0),
    latencia_ms bigint NOT NULL CHECK (latencia_ms>=0),
    primer_audio_ms bigint CHECK (primer_audio_ms>=0 AND primer_audio_ms<=latencia_ms),
    request_id varchar(128) NOT NULL,
    iniciada_en timestamptz NOT NULL,
    UNIQUE(turno_id,operacion,intento)
);
CREATE INDEX ejecucion_proveedor_fecha_idx ON asistente.ejecucion_proveedor(iniciada_en);
CREATE INDEX ejecucion_proveedor_turno_idx ON asistente.ejecucion_proveedor(usuario_id,turno_id);

-- Integridad de proyecciones: dueño, subcuenta disponible y grupo real del asiento.
CREATE FUNCTION finanzas.validar_movimiento() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER movimiento_valido BEFORE INSERT OR UPDATE ON finanzas.movimiento FOR EACH ROW EXECUTE FUNCTION finanzas.validar_movimiento();

CREATE FUNCTION finanzas.validar_componente() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER componente_valido BEFORE INSERT OR UPDATE ON finanzas.componente_movimiento FOR EACH ROW EXECUTE FUNCTION finanzas.validar_componente();

CREATE FUNCTION finanzas.validar_categoria() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER categoria_valida BEFORE INSERT OR UPDATE ON finanzas.categoria FOR EACH ROW EXECUTE FUNCTION finanzas.validar_categoria();

CREATE FUNCTION finanzas.validar_categoria_visible() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER clasificacion_visible BEFORE INSERT OR UPDATE ON finanzas.clasificacion_movimiento FOR EACH ROW EXECUTE FUNCTION finanzas.validar_categoria_visible();
CREATE TRIGGER regla_visible BEFORE INSERT OR UPDATE ON finanzas.regla_clasificacion FOR EACH ROW EXECUTE FUNCTION finanzas.validar_categoria_visible();
CREATE TRIGGER presupuesto_visible BEFORE INSERT OR UPDATE ON finanzas.presupuesto FOR EACH ROW EXECUTE FUNCTION finanzas.validar_categoria_visible();

CREATE FUNCTION finanzas.auditar_clasificacion() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER clasificacion_auditada AFTER INSERT OR UPDATE ON finanzas.clasificacion_movimiento FOR EACH ROW EXECUTE FUNCTION finanzas.auditar_clasificacion();

-- Roles del diseño: lecturas del núcleo para proyección, nunca escritura del dinero.
CREATE ROLE rol_finanzas NOLOGIN;
CREATE ROLE rol_producto NOLOGIN;
GRANT rol_finanzas,rol_producto TO rol_api WITH INHERIT FALSE,SET TRUE;
GRANT USAGE ON SCHEMA finanzas,nucleo TO rol_finanzas;
GRANT USAGE ON SCHEMA producto TO rol_producto;
GRANT SELECT ON nucleo.cuenta,nucleo.posting,nucleo.journal_entry,nucleo.transferencia,nucleo.deposito TO rol_finanzas;
GRANT SELECT ON nucleo.detalle_transferencia TO rol_finanzas;
GRANT SELECT,INSERT,UPDATE ON ALL TABLES IN SCHEMA finanzas TO rol_finanzas;
REVOKE UPDATE ON finanzas.cambio_clasificacion FROM rol_finanzas;
REVOKE INSERT,UPDATE ON finanzas.titular FROM rol_finanzas;
GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA finanzas TO rol_finanzas;
GRANT SELECT,INSERT,UPDATE ON ALL TABLES IN SCHEMA producto TO rol_producto;
GRANT SELECT,INSERT ON nucleo.detalle_transferencia,nucleo.evento_transferencia,nucleo.evento_custodio TO rol_nucleo;
GRANT UPDATE(resultado,codigo_resultado,procesado_en) ON nucleo.evento_custodio TO rol_nucleo;
GRANT USAGE,SELECT ON SEQUENCE nucleo.evento_transferencia_id_seq TO rol_nucleo;
GRANT INSERT ON asistente.ejecucion_proveedor TO rol_asistente;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA finanzas FROM PUBLIC;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA finanzas TO rol_finanzas;

-- Contexto financiero fijado por el backend, jamás tomado del body ni del modelo.
-- SET LOCAL / set_config(..., true) dentro de la misma transacción que SET LOCAL ROLE.
-- Sin contexto no hay filas; el dueño de las migraciones conserva su acceso administrativo.
ALTER TABLE finanzas.titular ENABLE ROW LEVEL SECURITY;
CREATE POLICY titular_propio ON finanzas.titular TO rol_finanzas
    USING (parte_id::text=current_setting('vera.parte_id',true));
ALTER TABLE finanzas.categoria ENABLE ROW LEVEL SECURITY;
CREATE POLICY categoria_lectura ON finanzas.categoria FOR SELECT TO rol_finanzas
    USING (propietario_id IS NULL OR propietario_id::text=current_setting('vera.parte_id',true));
CREATE POLICY categoria_alta ON finanzas.categoria FOR INSERT TO rol_finanzas
    WITH CHECK (propietario_id::text=current_setting('vera.parte_id',true));
CREATE POLICY categoria_edicion ON finanzas.categoria FOR UPDATE TO rol_finanzas
    USING (propietario_id::text=current_setting('vera.parte_id',true))
    WITH CHECK (propietario_id::text=current_setting('vera.parte_id',true));
DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['movimiento','componente_movimiento','clasificacion_movimiento','regla_clasificacion','presupuesto','periodo_presupuesto'] LOOP
        EXECUTE format('ALTER TABLE finanzas.%I ENABLE ROW LEVEL SECURITY',t);
        EXECUTE format('CREATE POLICY propietario ON finanzas.%I TO rol_finanzas USING (propietario_id::text=current_setting(''vera.parte_id'',true)) WITH CHECK (propietario_id::text=current_setting(''vera.parte_id'',true))',t);
    END LOOP;
    FOREACH t IN ARRAY ARRAY['notificacion','destinatario_favorito','tarjeta'] LOOP
        EXECUTE format('ALTER TABLE producto.%I ENABLE ROW LEVEL SECURITY',t);
        EXECUTE format('CREATE POLICY propietario ON producto.%I TO rol_producto USING (propietario_id::text=current_setting(''vera.parte_id'',true)) WITH CHECK (propietario_id::text=current_setting(''vera.parte_id'',true))',t);
    END LOOP;
END $$;
ALTER TABLE finanzas.cambio_clasificacion ENABLE ROW LEVEL SECURITY;
CREATE POLICY cambio_propio ON finanzas.cambio_clasificacion TO rol_finanzas
    USING (EXISTS(SELECT 1 FROM finanzas.componente_movimiento c WHERE c.id=componente_id))
    WITH CHECK (EXISTS(SELECT 1 FROM finanzas.componente_movimiento c WHERE c.id=componente_id));
ALTER TABLE producto.preferencia ENABLE ROW LEVEL SECURITY;
CREATE POLICY preferencia_propia ON producto.preferencia TO rol_producto
    USING (parte_id::text=current_setting('vera.parte_id',true))
    WITH CHECK (parte_id::text=current_setting('vera.parte_id',true));
COMMIT;

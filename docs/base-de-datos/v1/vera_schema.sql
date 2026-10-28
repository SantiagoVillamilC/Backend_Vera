-- ============================================================================
-- VERA · Esquema de base de datos
-- Prototipo de billetera digital inteligente con agente de IA
-- PostgreSQL 16 + pgvector
-- ----------------------------------------------------------------------------
-- Ejecutar sobre una base vacia:
--    psql -f vera_schema.sql -d vera
--
-- Estructura del archivo
--   Parte 1 · Esquemas, tipos y tablas   (generado desde vera_modelo_datos.dbml)
--   Parte 2 · Restricciones de integridad
--   Parte 3 · Reglas de negocio con disparadores
--   Parte 4 · Indices de consulta
--   Parte 5 · Vistas
--   Parte 6 · Datos base
--
-- Esquemas
--   identidad     · cuentas, credenciales, dispositivos y verificacion
--   contabilidad  · libro por partida doble: cuentas, asientos y apuntes
--   finanzas      · transferencias, categorias y presupuestos
--   ia            · agente conversacional y su memoria
--   auditoria     · bitacora transversal
--
-- Convenciones
--   · Nombres de tablas, columnas y tipos en espanol, en minuscula y sin
--     tildes, para no depender de comillas dobles en cada consulta.
--   · Las columnas de auditoria se conservan en ingles por convencion tecnica:
--     created_on, created_by, updated_on, updated_by e is_active.
--   · Los montos son BIGINT en pesos colombianos enteros. Nunca punto flotante.
--   · Toda tabla lleva las cinco columnas de auditoria, salvo las de solo
--     anexado (contabilidad.asiento, contabilidad.apunte, ia.mensaje y
--     auditoria.evento_auditoria), que solo llevan created_on y created_by.
--   · created_by y updated_by referencian identidad.usuario(id). La llave
--     foranea no se declara de forma deliberada: son columnas de bitacora que
--     deben sobrevivir al cierre de una cuenta. NULL identifica al sistema.
--
-- Donde vive cada secreto
--   · Contrasena  → identidad.credencial_usuario.hash_contrasena (Argon2id,
--                   con algoritmo, memoria_kib, iteraciones y paralelismo).
--   · Codigo OTP  → identidad.desafio_verificacion.hash_codigo (SHA-256).
--   · Codigos de recuperacion → identidad.codigo_recuperacion.hash_codigo,
--                   agrupados en identidad.lote_codigo_recuperacion.
--   · Token de renovacion → identidad.token_renovacion.hash_token.
--   · Permiso de restablecimiento → identidad.permiso_un_solo_uso.hash_token.
--   Ningun secreto se almacena en claro en ninguna tabla.
-- ============================================================================

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS vector;

-- SQL dump generated using DBML (dbml.dbdiagram.io)
-- Database: PostgreSQL
-- Generated at: 2026-09-18T20:08:59.848Z

CREATE SCHEMA "identidad";

CREATE SCHEMA "contabilidad";

CREATE SCHEMA "finanzas";

CREATE SCHEMA "ia";

CREATE SCHEMA "auditoria";

CREATE TYPE "tipo_cuenta" AS ENUM (
  'persona',
  'empresa'
);

CREATE TYPE "estado_usuario" AS ENUM (
  'pendiente_verificacion',
  'activo',
  'suspendido',
  'bloqueado',
  'cerrado'
);

CREATE TYPE "plataforma_dispositivo" AS ENUM (
  'android',
  'ios',
  'otra'
);

CREATE TYPE "proposito_desafio" AS ENUM (
  'verificacion_celular',
  'dispositivo_no_reconocido',
  'cambio_contrasena',
  'restablecimiento_contrasena',
  'cambio_celular',
  'recuperacion_cuenta',
  'confirmacion_operacion'
);

CREATE TYPE "estado_desafio" AS ENUM (
  'pendiente',
  'verificado',
  'expirado',
  'agotado',
  'cancelado'
);

CREATE TYPE "canal_envio" AS ENUM (
  'whatsapp',
  'sms',
  'correo_electronico'
);

CREATE TYPE "estado_envio" AS ENUM (
  'en_cola',
  'enviado',
  'entregado',
  'fallido'
);

CREATE TYPE "proposito_permiso" AS ENUM (
  'restablecimiento_contrasena',
  'recuperacion_cuenta',
  'confirmacion_operacion'
);

CREATE TYPE "estado_permiso" AS ENUM (
  'emitido',
  'consumido',
  'expirado',
  'revocado'
);

CREATE TYPE "tipo_cuenta_contable" AS ENUM (
  'activo',
  'pasivo',
  'patrimonio',
  'ingreso',
  'gasto'
);

CREATE TYPE "naturaleza" AS ENUM (
  'debito',
  'credito'
);

CREATE TYPE "proposito_cuenta" AS ENUM (
  'usuario_disponible',
  'usuario_retenido',
  'sistema_semilla',
  'sistema_transitoria',
  'sistema_comisiones'
);

CREATE TYPE "naturaleza_contable" AS ENUM (
  'debito',
  'credito'
);

CREATE TYPE "estado_transferencia" AS ENUM (
  'pendiente',
  'completada',
  'rechazada',
  'reversada'
);

CREATE TYPE "origen_operacion" AS ENUM (
  'aplicacion_movil',
  'agente_ia',
  'sistema'
);

CREATE TYPE "estado_idempotencia" AS ENUM (
  'en_proceso',
  'completada',
  'fallido'
);

CREATE TYPE "origen_clasificacion" AS ENUM (
  'regla_sistema',
  'agente_ia',
  'usuario'
);

CREATE TYPE "periodicidad_presupuesto" AS ENUM (
  'semanal',
  'mensual',
  'personalizada'
);

CREATE TYPE "estado_conversacion" AS ENUM (
  'activo',
  'archivado'
);

CREATE TYPE "rol_mensaje" AS ENUM (
  'usuario',
  'asistente',
  'herramienta'
);

CREATE TYPE "estado_invocacion" AS ENUM (
  'espera_confirmacion',
  'exitoso',
  'fallido',
  'denegado'
);

CREATE TYPE "origen_memoria" AS ENUM (
  'mensaje',
  'resumen_movimiento',
  'hallazgo'
);

CREATE TYPE "estado_hallazgo" AS ENUM (
  'nuevo',
  'reconocido',
  'descartado'
);

CREATE TYPE "tipo_actor" AS ENUM (
  'usuario',
  'sistema',
  'agente_ia'
);

CREATE TYPE "ambito_limite" AS ENUM (
  'registro',
  'solicitud_codigo',
  'verificacion_codigo',
  'ingreso_sesion',
  'consulta_destinatario',
  'creacion_transferencia'
);

CREATE TABLE "identidad"."usuario" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "celular_e164" varchar(16) UNIQUE NOT NULL,
  "fecha_verificacion_celular" timestamptz,
  "correo_electronico" varchar(254) UNIQUE NOT NULL,
  "fecha_verificacion_correo" timestamptz,
  "tipo_cuenta" tipo_cuenta NOT NULL DEFAULT 'persona',
  "estado" estado_usuario NOT NULL DEFAULT 'pendiente_verificacion',
  "intentos_fallidos_ingreso" smallint NOT NULL DEFAULT 0,
  "bloqueado_hasta" timestamptz,
  "sesiones_invalidas_antes_de" timestamptz,
  "fecha_aceptacion_politica_datos" timestamptz,
  "fecha_consentimiento_whatsapp" timestamptz,
  "idioma" varchar(10) NOT NULL DEFAULT 'es-CO',
  "zona_horaria" varchar(64) NOT NULL DEFAULT 'America/Bogota',
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "identidad"."credencial_usuario" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "usuario_id" uuid NOT NULL,
  "algoritmo" varchar(32) NOT NULL DEFAULT 'argon2id',
  "hash_contrasena" text NOT NULL,
  "memoria_kib" integer NOT NULL,
  "iteraciones" smallint NOT NULL,
  "paralelismo" smallint NOT NULL,
  "es_vigente" boolean NOT NULL DEFAULT true,
  "vigente_desde" timestamptz NOT NULL DEFAULT (now()),
  "fecha_reemplazo" timestamptz,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "identidad"."dispositivo_usuario" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "usuario_id" uuid NOT NULL,
  "identificador_dispositivo" varchar(128) NOT NULL,
  "nombre_visible" varchar(80),
  "plataforma" plataforma_dispositivo NOT NULL,
  "version_sistema_operativo" varchar(40),
  "version_aplicacion" varchar(20),
  "modelo" varchar(80),
  "es_reconocido" boolean NOT NULL DEFAULT false,
  "fecha_reconocimiento" timestamptz,
  "desbloqueo_rapido_activo" boolean NOT NULL DEFAULT false,
  "fecha_activacion_desbloqueo" timestamptz,
  "sesiones_invalidas_antes_de" timestamptz,
  "ultima_actividad" timestamptz,
  "ultima_ip" inet,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "identidad"."token_renovacion" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "usuario_id" uuid NOT NULL,
  "dispositivo_usuario_id" uuid NOT NULL,
  "familia_id" uuid NOT NULL,
  "hash_token" char(64) UNIQUE NOT NULL,
  "fecha_emision" timestamptz NOT NULL DEFAULT (now()),
  "vence_por_inactividad" timestamptz NOT NULL,
  "vence_absoluto" timestamptz NOT NULL,
  "rotado_a_id" uuid,
  "fecha_uso" timestamptz,
  "fecha_revocacion" timestamptz,
  "motivo_revocacion" varchar(60),
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "identidad"."desafio_verificacion" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "usuario_id" uuid NOT NULL,
  "dispositivo_usuario_id" uuid,
  "proposito" proposito_desafio NOT NULL,
  "destino_e164" varchar(16) NOT NULL,
  "motivo_operacion" varchar(60),
  "hash_codigo" char(64) NOT NULL,
  "estado" estado_desafio NOT NULL DEFAULT 'pendiente',
  "fecha_expiracion" timestamptz NOT NULL,
  "intentos_usados" smallint NOT NULL DEFAULT 0,
  "intentos_maximos" smallint NOT NULL DEFAULT 3,
  "reenvios_usados" smallint NOT NULL DEFAULT 0,
  "reenvios_maximos" smallint NOT NULL DEFAULT 3,
  "fecha_ultimo_envio" timestamptz,
  "fecha_verificacion" timestamptz,
  "ip_solicitud" inet,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "identidad"."envio_desafio" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "desafio_verificacion_id" uuid NOT NULL,
  "numero_intento" smallint NOT NULL,
  "canal" canal_envio NOT NULL DEFAULT 'whatsapp',
  "proveedor" varchar(40) NOT NULL DEFAULT 'whatsapp_cloud_api',
  "id_mensaje_proveedor" varchar(120),
  "estado" estado_envio NOT NULL DEFAULT 'en_cola',
  "codigo_error" varchar(40),
  "detalle_error" text,
  "fecha_envio" timestamptz,
  "fecha_entrega" timestamptz,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "identidad"."permiso_un_solo_uso" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "usuario_id" uuid NOT NULL,
  "desafio_origen_id" uuid,
  "proposito" proposito_permiso NOT NULL,
  "hash_token" char(64) UNIQUE NOT NULL,
  "estado" estado_permiso NOT NULL DEFAULT 'emitido',
  "fecha_expiracion" timestamptz NOT NULL,
  "fecha_consumo" timestamptz,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "identidad"."lote_codigo_recuperacion" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "usuario_id" uuid NOT NULL,
  "fecha_generacion" timestamptz NOT NULL DEFAULT (now()),
  "fecha_invalidacion" timestamptz,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "identidad"."codigo_recuperacion" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "lote_codigo_recuperacion_id" uuid NOT NULL,
  "numero_orden" smallint NOT NULL,
  "hash_codigo" char(64) NOT NULL,
  "fecha_consumo" timestamptz,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "identidad"."contador_limite_solicitudes" (
  "id" BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
  "ambito" ambito_limite NOT NULL,
  "sujeto" varchar(160) NOT NULL,
  "inicio_ventana" timestamptz NOT NULL,
  "duracion_ventana_segundos" integer NOT NULL,
  "conteo" integer NOT NULL DEFAULT 0,
  "violaciones_consecutivas" smallint NOT NULL DEFAULT 0,
  "bloqueado_hasta" timestamptz,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "identidad"."tipo_documento" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "codigo" varchar(6) UNIQUE NOT NULL,
  "nombre" varchar(80) NOT NULL,
  "descripcion" varchar(240),
  "aplica_persona" boolean NOT NULL DEFAULT true,
  "aplica_empresa" boolean NOT NULL DEFAULT false,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "identidad"."persona_natural" (
  "usuario_id" uuid PRIMARY KEY,
  "nombre_completo" varchar(160) NOT NULL,
  "tipo_documento_id" uuid NOT NULL,
  "numero_documento" varchar(20) NOT NULL,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "identidad"."empresa" (
  "usuario_id" uuid PRIMARY KEY,
  "razon_social" varchar(200) NOT NULL,
  "nit" varchar(15) UNIQUE NOT NULL,
  "digito_verificacion" smallint NOT NULL,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "identidad"."representante_legal" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "empresa_id" uuid NOT NULL,
  "nombre_completo" varchar(160) NOT NULL,
  "tipo_documento_id" uuid NOT NULL,
  "numero_documento" varchar(20) NOT NULL,
  "es_vigente" boolean NOT NULL DEFAULT true,
  "vigente_desde" date NOT NULL,
  "fecha_fin" date,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "identidad"."politica_limite" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "ambito" ambito_limite NOT NULL,
  "sujeto_limite" varchar(20) NOT NULL,
  "maximo_solicitudes" integer NOT NULL,
  "ventana_segundos" integer NOT NULL,
  "bloqueo_inicial_segundos" integer NOT NULL DEFAULT 30,
  "bloqueo_maximo_segundos" integer NOT NULL DEFAULT 3600,
  "factor_crecimiento" numeric(3,1) NOT NULL DEFAULT 2,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "contabilidad"."cuenta_contable" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "codigo" varchar(120) UNIQUE NOT NULL,
  "tipo_cuenta" tipo_cuenta_contable NOT NULL,
  "naturaleza" naturaleza NOT NULL,
  "proposito" proposito_cuenta NOT NULL,
  "usuario_propietario_id" uuid,
  "moneda" char(3) NOT NULL DEFAULT 'COP',
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "contabilidad"."asiento" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "numero_asiento" BIGINT GENERATED BY DEFAULT AS IDENTITY UNIQUE NOT NULL,
  "tipo_transaccion_id" uuid NOT NULL,
  "descripcion" varchar(240) NOT NULL,
  "moneda" char(3) NOT NULL DEFAULT 'COP',
  "fecha_hecho" timestamptz NOT NULL,
  "fecha_registro" timestamptz NOT NULL DEFAULT (now()),
  "asiento_reversado_id" uuid,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid
);

CREATE TABLE "contabilidad"."apunte" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "numero_apunte" BIGINT GENERATED BY DEFAULT AS IDENTITY UNIQUE NOT NULL,
  "asiento_id" uuid NOT NULL,
  "cuenta_contable_id" uuid NOT NULL,
  "orden_en_asiento" smallint NOT NULL,
  "naturaleza_apunte" naturaleza_contable NOT NULL,
  "monto_cop" bigint NOT NULL,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid
);

CREATE TABLE "contabilidad"."corte_saldo" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "cuenta_contable_id" uuid UNIQUE NOT NULL,
  "saldo_cop" bigint NOT NULL DEFAULT 0,
  "ultimo_numero_apunte" bigint NOT NULL DEFAULT 0,
  "version_fila" bigint NOT NULL DEFAULT 1,
  "fecha_calculo" timestamptz NOT NULL DEFAULT (now()),
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid
);

CREATE TABLE "finanzas"."tipo_transaccion" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "codigo" varchar(40) UNIQUE NOT NULL,
  "nombre" varchar(80) NOT NULL,
  "descripcion" varchar(240),
  "afecta_disponible" boolean NOT NULL DEFAULT true,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "finanzas"."clave_idempotencia" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "usuario_id" uuid NOT NULL,
  "operacion" varchar(60) NOT NULL,
  "valor_clave" varchar(128) NOT NULL,
  "huella_solicitud" char(64) NOT NULL,
  "estado" estado_idempotencia NOT NULL DEFAULT 'en_proceso',
  "codigo_respuesta" smallint,
  "cuerpo_respuesta" jsonb,
  "fecha_expiracion" timestamptz NOT NULL,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "finanzas"."transferencia" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "referencia_publica" varchar(24) UNIQUE NOT NULL,
  "usuario_origen_id" uuid NOT NULL,
  "usuario_destino_id" uuid NOT NULL,
  "celular_origen_e164" varchar(16) NOT NULL,
  "celular_destino_e164" varchar(16) NOT NULL,
  "monto_cop" bigint NOT NULL,
  "moneda" char(3) NOT NULL DEFAULT 'COP',
  "mensaje" varchar(140),
  "estado" estado_transferencia NOT NULL DEFAULT 'pendiente',
  "codigo_motivo_rechazo" varchar(40),
  "asiento_id" uuid,
  "clave_idempotencia_id" uuid NOT NULL,
  "origen" origen_operacion NOT NULL DEFAULT 'aplicacion_movil',
  "invocacion_herramienta_id" uuid,
  "fecha_confirmacion" timestamptz,
  "fecha_hecho" timestamptz NOT NULL DEFAULT (now()),
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "finanzas"."categoria_transaccion" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "codigo" varchar(40) UNIQUE NOT NULL,
  "categoria_padre_id" uuid,
  "nombre" varchar(80) NOT NULL,
  "descripcion" varchar(240),
  "icono" varchar(40),
  "color" char(7),
  "es_del_sistema" boolean NOT NULL DEFAULT true,
  "usuario_propietario_id" uuid,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "finanzas"."clasificacion_movimiento" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "asiento_id" uuid UNIQUE NOT NULL,
  "categoria_transaccion_id" uuid NOT NULL,
  "origen" origen_clasificacion NOT NULL,
  "confianza" numeric(5,4),
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "finanzas"."presupuesto" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "usuario_id" uuid NOT NULL,
  "categoria_transaccion_id" uuid,
  "nombre" varchar(80) NOT NULL,
  "periodicidad" periodicidad_presupuesto NOT NULL DEFAULT 'mensual',
  "fecha_inicio" date NOT NULL,
  "fecha_fin" date NOT NULL,
  "monto_limite_cop" bigint NOT NULL,
  "umbral_alerta_porcentaje" smallint NOT NULL DEFAULT 80,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "ia"."conversacion" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "usuario_id" uuid NOT NULL,
  "titulo" varchar(120),
  "estado" estado_conversacion NOT NULL DEFAULT 'activo',
  "cantidad_mensajes" integer NOT NULL DEFAULT 0,
  "fecha_inicio_conversacion" timestamptz NOT NULL DEFAULT (now()),
  "fecha_ultimo_mensaje" timestamptz,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "ia"."mensaje" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "conversacion_id" uuid NOT NULL,
  "numero_orden" integer NOT NULL,
  "rol" rol_mensaje NOT NULL,
  "contenido" text NOT NULL,
  "nombre_modelo" varchar(60),
  "tokens_entrada" integer,
  "tokens_salida" integer,
  "latencia_ms" integer,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid
);

CREATE TABLE "ia"."herramienta" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "codigo" varchar(60) UNIQUE NOT NULL,
  "nombre" varchar(80) NOT NULL,
  "descripcion" varchar(400) NOT NULL,
  "esquema_entrada" jsonb NOT NULL,
  "modifica_datos" boolean NOT NULL DEFAULT false,
  "exige_confirmacion_usuario" boolean NOT NULL DEFAULT false,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "ia"."invocacion_herramienta" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "mensaje_id" uuid NOT NULL,
  "herramienta_id" uuid NOT NULL,
  "argumentos" jsonb NOT NULL,
  "estado" estado_invocacion NOT NULL,
  "resumen_resultado" text,
  "codigo_error" varchar(40),
  "duracion_ms" integer,
  "fecha_confirmacion_usuario" timestamptz,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "ia"."memoria_vectorial" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "usuario_id" uuid NOT NULL,
  "origen" origen_memoria NOT NULL,
  "origen_id" uuid NOT NULL,
  "contenido" text NOT NULL,
  "vector_embebido" vector(1536) NOT NULL,
  "nombre_modelo" varchar(60) NOT NULL,
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "ia"."hallazgo" (
  "id" uuid PRIMARY KEY DEFAULT (gen_random_uuid()),
  "usuario_id" uuid NOT NULL,
  "categoria_transaccion_id" uuid,
  "codigo_hallazgo" varchar(40) NOT NULL,
  "titulo" varchar(120) NOT NULL,
  "cuerpo" text NOT NULL,
  "fecha_inicio" date,
  "fecha_fin" date,
  "relevancia" numeric(5,4),
  "estado" estado_hallazgo NOT NULL DEFAULT 'nuevo',
  "fecha_reconocimiento" timestamptz,
  "modelo_generador" varchar(60),
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid,
  "updated_on" timestamptz,
  "updated_by" uuid,
  "is_active" boolean NOT NULL DEFAULT true
);

CREATE TABLE "auditoria"."evento_auditoria" (
  "id" BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
  "codigo_evento" varchar(60) NOT NULL,
  "tipo_actor" tipo_actor NOT NULL,
  "usuario_actor_id" uuid,
  "dispositivo_usuario_id" uuid,
  "esquema_entidad" varchar(20),
  "tabla_entidad" varchar(60),
  "entidad_id" uuid,
  "direccion_ip" inet,
  "agente_usuario" varchar(240),
  "solicitud_id" uuid,
  "contenido_evento" jsonb,
  "fecha_evento" timestamptz NOT NULL DEFAULT (now()),
  "created_on" timestamptz NOT NULL DEFAULT (now()),
  "created_by" uuid
);

CREATE UNIQUE INDEX "ux_usuario_celular" ON "identidad"."usuario" ("celular_e164");

CREATE UNIQUE INDEX "ux_usuario_correo" ON "identidad"."usuario" ("correo_electronico");

CREATE INDEX "ix_usuario_estado" ON "identidad"."usuario" ("estado");

CREATE INDEX "ix_credencial_vigente" ON "identidad"."credencial_usuario" ("usuario_id", "es_vigente");

CREATE UNIQUE INDEX "ux_dispositivo_por_usuario" ON "identidad"."dispositivo_usuario" ("usuario_id", "identificador_dispositivo");

CREATE INDEX "ix_dispositivo_usuario" ON "identidad"."dispositivo_usuario" ("usuario_id");

CREATE UNIQUE INDEX "ux_token_hash" ON "identidad"."token_renovacion" ("hash_token");

CREATE INDEX "ix_token_alcance" ON "identidad"."token_renovacion" ("usuario_id", "dispositivo_usuario_id");

CREATE INDEX "ix_token_familia" ON "identidad"."token_renovacion" ("familia_id");

CREATE INDEX "ix_desafio_busqueda" ON "identidad"."desafio_verificacion" ("usuario_id", "proposito", "estado");

CREATE INDEX "ix_desafio_expiracion" ON "identidad"."desafio_verificacion" ("fecha_expiracion");

CREATE UNIQUE INDEX "ux_envio_intento" ON "identidad"."envio_desafio" ("desafio_verificacion_id", "numero_intento");

CREATE UNIQUE INDEX "ux_permiso_token" ON "identidad"."permiso_un_solo_uso" ("hash_token");

CREATE INDEX "ix_permiso_busqueda" ON "identidad"."permiso_un_solo_uso" ("usuario_id", "proposito", "estado");

CREATE INDEX "ix_lote_recuperacion_usuario" ON "identidad"."lote_codigo_recuperacion" ("usuario_id");

CREATE UNIQUE INDEX "ux_codigo_recuperacion_orden" ON "identidad"."codigo_recuperacion" ("lote_codigo_recuperacion_id", "numero_orden");

CREATE UNIQUE INDEX "ux_limite_ventana" ON "identidad"."contador_limite_solicitudes" ("ambito", "sujeto", "inicio_ventana");

CREATE INDEX "ix_limite_bloqueado" ON "identidad"."contador_limite_solicitudes" ("bloqueado_hasta");

CREATE UNIQUE INDEX "ux_tipo_documento_codigo" ON "identidad"."tipo_documento" ("codigo");

CREATE UNIQUE INDEX "ux_persona_documento" ON "identidad"."persona_natural" ("tipo_documento_id", "numero_documento");

CREATE UNIQUE INDEX "ux_empresa_nit" ON "identidad"."empresa" ("nit");

CREATE INDEX "ix_representante_vigente" ON "identidad"."representante_legal" ("empresa_id", "es_vigente");

CREATE UNIQUE INDEX "ux_politica_limite" ON "identidad"."politica_limite" ("ambito", "sujeto_limite", "ventana_segundos");

CREATE UNIQUE INDEX "ux_cuenta_contable_codigo" ON "contabilidad"."cuenta_contable" ("codigo");

CREATE UNIQUE INDEX "ux_cuenta_contable_propietario_proposito" ON "contabilidad"."cuenta_contable" ("usuario_propietario_id", "proposito");

CREATE INDEX "ix_cuenta_contable_tipo" ON "contabilidad"."cuenta_contable" ("tipo_cuenta");

CREATE UNIQUE INDEX "ux_asiento_numero" ON "contabilidad"."asiento" ("numero_asiento");

CREATE INDEX "ix_asiento_fecha_hecho" ON "contabilidad"."asiento" ("fecha_hecho");

CREATE INDEX "ix_asiento_reverso" ON "contabilidad"."asiento" ("asiento_reversado_id");

CREATE UNIQUE INDEX "ux_apunte_numero" ON "contabilidad"."apunte" ("numero_apunte");

CREATE UNIQUE INDEX "ux_apunte_orden" ON "contabilidad"."apunte" ("asiento_id", "orden_en_asiento");

CREATE INDEX "ix_apunte_cuenta_cursor" ON "contabilidad"."apunte" ("cuenta_contable_id", "numero_apunte");

CREATE UNIQUE INDEX "ux_corte_saldo_cuenta" ON "contabilidad"."corte_saldo" ("cuenta_contable_id");

CREATE UNIQUE INDEX "ux_tipo_transaccion_codigo" ON "finanzas"."tipo_transaccion" ("codigo");

CREATE UNIQUE INDEX "ux_clave_idempotencia" ON "finanzas"."clave_idempotencia" ("usuario_id", "operacion", "valor_clave");

CREATE INDEX "ix_idempotencia_expiracion" ON "finanzas"."clave_idempotencia" ("fecha_expiracion");

CREATE UNIQUE INDEX "ux_transferencia_referencia" ON "finanzas"."transferencia" ("referencia_publica");

CREATE INDEX "ix_transferencia_origen" ON "finanzas"."transferencia" ("usuario_origen_id", "created_on");

CREATE INDEX "ix_transferencia_destino" ON "finanzas"."transferencia" ("usuario_destino_id", "created_on");

CREATE INDEX "ix_transferencia_estado" ON "finanzas"."transferencia" ("estado");

CREATE UNIQUE INDEX "ux_categoria_codigo" ON "finanzas"."categoria_transaccion" ("codigo");

CREATE INDEX "ix_categoria_padre" ON "finanzas"."categoria_transaccion" ("categoria_padre_id");

CREATE UNIQUE INDEX "ux_clasificacion_asiento" ON "finanzas"."clasificacion_movimiento" ("asiento_id");

CREATE INDEX "ix_clasificacion_categoria" ON "finanzas"."clasificacion_movimiento" ("categoria_transaccion_id");

CREATE INDEX "ix_presupuesto_periodo" ON "finanzas"."presupuesto" ("usuario_id", "fecha_inicio", "fecha_fin");

CREATE INDEX "ix_conversacion_reciente" ON "ia"."conversacion" ("usuario_id", "fecha_ultimo_mensaje");

CREATE UNIQUE INDEX "ux_mensaje_orden" ON "ia"."mensaje" ("conversacion_id", "numero_orden");

CREATE UNIQUE INDEX "ux_herramienta_codigo" ON "ia"."herramienta" ("codigo");

CREATE INDEX "ix_invocacion_mensaje" ON "ia"."invocacion_herramienta" ("mensaje_id");

CREATE INDEX "ix_invocacion_herramienta_estado" ON "ia"."invocacion_herramienta" ("herramienta_id", "estado");

CREATE INDEX "ix_memoria_alcance" ON "ia"."memoria_vectorial" ("usuario_id", "origen");

CREATE INDEX "ix_hallazgo_usuario_estado" ON "ia"."hallazgo" ("usuario_id", "estado");

CREATE INDEX "ix_auditoria_actor" ON "auditoria"."evento_auditoria" ("usuario_actor_id", "fecha_evento");

CREATE INDEX "ix_auditoria_entidad" ON "auditoria"."evento_auditoria" ("tabla_entidad", "entidad_id");

CREATE INDEX "ix_auditoria_codigo_evento" ON "auditoria"."evento_auditoria" ("codigo_evento");

CREATE INDEX "ix_auditoria_solicitud" ON "auditoria"."evento_auditoria" ("solicitud_id");

COMMENT ON TABLE "identidad"."usuario" IS 'Datos comunes a los dos tipos de cuenta. Los datos propios de cada uno viven en identidad.persona_natural y en identidad.empresa, en relacion uno a uno. Un disparador diferido verifica al cerrar la transaccion que exista la fila del subtipo que corresponde a tipo_cuenta. ATENCION: HU-BE-AUT-01 dice que no se solicita documento de identidad por tratarse de un entorno simulado, mientras que el documento de endpoints exige tipoDocumento y numeroDocumento. El modelo soporta ambos, pero el equipo debe cerrar la contradiccion.';

COMMENT ON COLUMN "identidad"."usuario"."celular_e164" IS 'Identificador de la cuenta. Formato E.164, HU-BE-AUT-01';

COMMENT ON COLUMN "identidad"."usuario"."fecha_verificacion_celular" IS 'NULL mientras el celular no se verifica';

COMMENT ON COLUMN "identidad"."usuario"."correo_electronico" IS 'Dato de contacto en las HU; identificador de ingreso en el documento de endpoints. Ver nota de la tabla';

COMMENT ON COLUMN "identidad"."usuario"."tipo_cuenta" IS 'Discrimina el subtipo: los datos propios viven en persona_natural o en empresa';

COMMENT ON COLUMN "identidad"."usuario"."sesiones_invalidas_antes_de" IS 'Marca de revocacion global. Todo token de renovacion emitido antes deja de ser valido, HU-BE-AUT-05';

COMMENT ON COLUMN "identidad"."usuario"."fecha_aceptacion_politica_datos" IS 'Ley 1581 de 2012';

COMMENT ON COLUMN "identidad"."usuario"."fecha_consentimiento_whatsapp" IS 'Consentimiento explicito para el canal de mensajeria';

COMMENT ON TABLE "identidad"."credencial_usuario" IS 'Historico de contrasenas. Solo una fila con es_vigente verdadero por cuenta. Permite impedir la reutilizacion de las ultimas contrasenas.';

COMMENT ON COLUMN "identidad"."credencial_usuario"."hash_contrasena" IS 'Codificacion PHC completa, incluye sal y parametros';

COMMENT ON TABLE "identidad"."dispositivo_usuario" IS 'El sistema nunca almacena ni procesa huellas ni rostros. Solo guarda la preferencia de desbloqueo rapido.';

COMMENT ON COLUMN "identidad"."dispositivo_usuario"."identificador_dispositivo" IS 'Identificador generado por el cliente y persistido en almacenamiento seguro';

COMMENT ON COLUMN "identidad"."dispositivo_usuario"."es_reconocido" IS 'Verdadero tras superar un desafio de dispositivo no reconocido';

COMMENT ON COLUMN "identidad"."dispositivo_usuario"."desbloqueo_rapido_activo" IS 'Preferencia de desbloqueo con huella o rostro, por cuenta y dispositivo, HU-BE-AUT-04';

COMMENT ON COLUMN "identidad"."dispositivo_usuario"."sesiones_invalidas_antes_de" IS 'Marca de revocacion acotada a este dispositivo, HU-BE-AUT-05';

COMMENT ON TABLE "identidad"."token_renovacion" IS 'ADICION respecto de HU-BE-AUT-05, que resuelve la revocacion solo con marcas de instante. Sin esta tabla la rotacion no permite detectar el uso repetido de un token ya rotado, que es la senal de robo. Si el equipo decide conservar el diseno original, esta tabla se elimina y las marcas sesiones_invalidas_antes_de siguen siendo suficientes para cerrar sesion.';

COMMENT ON COLUMN "identidad"."token_renovacion"."familia_id" IS 'Cadena de rotacion. Se conserva entre renovaciones sucesivas';

COMMENT ON COLUMN "identidad"."token_renovacion"."hash_token" IS 'SHA-256 del token. El valor en claro nunca se persiste';

COMMENT ON COLUMN "identidad"."token_renovacion"."vence_por_inactividad" IS '15 dias de inactividad';

COMMENT ON COLUMN "identidad"."token_renovacion"."vence_absoluto" IS '45 dias desde el inicio de la cadena';

COMMENT ON COLUMN "identidad"."token_renovacion"."rotado_a_id" IS 'Token que lo reemplazo en la rotacion';

COMMENT ON COLUMN "identidad"."desafio_verificacion"."dispositivo_usuario_id" IS 'Dispositivo que origino la solicitud';

COMMENT ON COLUMN "identidad"."desafio_verificacion"."motivo_operacion" IS 'Campo motivo de /auth/otp/request, solo cuando proposito es confirmacion_operacion. Se valida contra una lista cerrada en el backend';

COMMENT ON COLUMN "identidad"."desafio_verificacion"."hash_codigo" IS 'El codigo de 6 digitos nunca se persiste en claro';

COMMENT ON COLUMN "identidad"."desafio_verificacion"."fecha_expiracion" IS 'Vigencia de 5 minutos, HU-BE-AUT-03';

COMMENT ON COLUMN "identidad"."desafio_verificacion"."intentos_maximos" IS 'Tres intentos por codigo, HU-BE-AUT-03 y documento de endpoints';

COMMENT ON COLUMN "identidad"."desafio_verificacion"."fecha_ultimo_envio" IS 'Base del enfriamiento de 60 segundos entre reenvios';

COMMENT ON TABLE "identidad"."envio_desafio" IS 'Un desafio admite hasta tres reenvios, cada uno con su propio intento de entrega y su propio resultado.';

COMMENT ON TABLE "identidad"."permiso_un_solo_uso" IS 'Permiso de un solo uso y vida corta que autoriza exclusivamente el cambio de contrasena. No es un token de sesion.';

COMMENT ON COLUMN "identidad"."permiso_un_solo_uso"."desafio_origen_id" IS 'Desafio que habilito la emision del permiso';

COMMENT ON COLUMN "identidad"."permiso_un_solo_uso"."fecha_expiracion" IS 'Vigencia maxima de 10 minutos, HU-BE-AUT-06 y HU-BE-AUT-07';

COMMENT ON TABLE "identidad"."lote_codigo_recuperacion" IS 'Lote de ocho codigos de recuperacion. Se muestran una unica vez al usuario, HU-BE-AUT-07.';

COMMENT ON COLUMN "identidad"."lote_codigo_recuperacion"."fecha_invalidacion" IS 'Se invalida al generar un lote nuevo';

COMMENT ON COLUMN "identidad"."codigo_recuperacion"."hash_codigo" IS 'Almacenamiento derivado. El codigo en claro nunca se persiste';

COMMENT ON TABLE "identidad"."contador_limite_solicitudes" IS 'Racionamiento y espera creciente exigidos en HU-BE-AUT-01, HU-BE-AUT-02, HU-BE-AUT-03 y HU-BE-NF-06. Vive en la base porque el presupuesto no contempla un almacen en memoria dedicado.';

COMMENT ON COLUMN "identidad"."contador_limite_solicitudes"."sujeto" IS 'Sujeto del limite: celular, direccion IP o identificador de cuenta';

COMMENT ON COLUMN "identidad"."contador_limite_solicitudes"."violaciones_consecutivas" IS 'Cada violacion multiplica la espera por factor_crecimiento, hasta bloqueo_maximo_segundos';

COMMENT ON TABLE "identidad"."tipo_documento" IS 'Catalogo. Se modela como tabla y no como enumerado porque Colombia agrega tipos de documento con frecuencia (PEP y PPT para poblacion migrante). Agregar uno es insertar una fila, no una migracion de esquema.';

COMMENT ON COLUMN "identidad"."tipo_documento"."codigo" IS 'CC, CE, PA, PEP, PPT';

COMMENT ON TABLE "identidad"."persona_natural" IS 'Subtipo de usuario cuando tipo_cuenta es persona.';

COMMENT ON COLUMN "identidad"."persona_natural"."usuario_id" IS 'Llave primaria y foranea a la vez: relacion uno a uno con identidad.usuario';

COMMENT ON COLUMN "identidad"."persona_natural"."nombre_completo" IS 'Normalizado en NFC, conforme a LDML Person Names';

COMMENT ON TABLE "identidad"."empresa" IS 'Subtipo de usuario cuando tipo_cuenta es empresa. El digito de verificacion se guarda aparte del NIT y una restriccion lo recalcula, de modo que la base rechaza un NIT mal digitado.';

COMMENT ON COLUMN "identidad"."empresa"."usuario_id" IS 'Llave primaria y foranea a la vez: relacion uno a uno con identidad.usuario';

COMMENT ON COLUMN "identidad"."empresa"."nit" IS 'Solo los digitos del NIT, sin el verificador y sin separadores';

COMMENT ON COLUMN "identidad"."empresa"."digito_verificacion" IS 'Validado contra el algoritmo de modulo 11 de la DIAN';

COMMENT ON TABLE "identidad"."representante_legal" IS 'Tabla propia y no columnas dentro de empresa, porque el representante legal cambia y la empresa sigue siendo la misma. Una restriccion permite un solo representante vigente por empresa y conserva los anteriores.';

COMMENT ON TABLE "identidad"."politica_limite" IS 'Parametriza el racionamiento: 5 codigos por hora y por cuenta, 20 por hora y por origen, y 10 intentos fallidos en 24 horas. Una misma combinacion de ambito y sujeto admite varias ventanas, por eso la ventana hace parte de la llave. Vive en datos para poder ajustarlo sin desplegar codigo.';

COMMENT ON COLUMN "identidad"."politica_limite"."sujeto_limite" IS 'cuenta, origen_ip o celular';

COMMENT ON COLUMN "identidad"."politica_limite"."bloqueo_inicial_segundos" IS 'Primera espera al superar el limite';

COMMENT ON COLUMN "identidad"."politica_limite"."bloqueo_maximo_segundos" IS 'Techo de la espera. HU-BE-AUT-03 pide de 30 segundos hasta 1 hora, nunca un bloqueo permanente';

COMMENT ON COLUMN "identidad"."politica_limite"."factor_crecimiento" IS 'Multiplicador aplicado en cada violacion consecutiva';

COMMENT ON TABLE "contabilidad"."cuenta_contable" IS 'Cada usuario tiene exactamente dos subcuentas de pasivo: disponible y retenido. Las cuentas del sistema aportan la contrapartida que hace cuadrar cada asiento.';

COMMENT ON COLUMN "contabilidad"."cuenta_contable"."codigo" IS 'Ejemplo: pasivo:usuario:{uuid}:disponible';

COMMENT ON COLUMN "contabilidad"."cuenta_contable"."usuario_propietario_id" IS 'NULL en las cuentas del sistema';

COMMENT ON TABLE "contabilidad"."asiento" IS 'Diario de solo anexado. Un asiento no se edita ni se borra: la correccion es un asiento nuevo que apunta al original mediante asiento_reversado_id. Por eso la tabla no lleva updated_on ni is_active.';

COMMENT ON COLUMN "contabilidad"."asiento"."numero_asiento" IS 'Secuencia monotona. Da orden estable cuando dos asientos comparten fecha';

COMMENT ON COLUMN "contabilidad"."asiento"."fecha_hecho" IS 'Cuando ocurrio el hecho';

COMMENT ON COLUMN "contabilidad"."asiento"."fecha_registro" IS 'Cuando lo supo el sistema';

COMMENT ON COLUMN "contabilidad"."asiento"."asiento_reversado_id" IS 'Asiento que este corrige, en sentido contrario';

COMMENT ON TABLE "contabilidad"."apunte" IS 'Apunte contable. Es el registro que ve el usuario en su historial. Nunca se actualiza ni se borra.';

COMMENT ON COLUMN "contabilidad"."apunte"."numero_apunte" IS 'Cursor de paginacion del historial, HU-BE-NF-05 criterio 10';

COMMENT ON COLUMN "contabilidad"."apunte"."monto_cop" IS 'Entero de pesos con signo desde la perspectiva del pasivo. La suma de los apuntes de un asiento es cero';

COMMENT ON TABLE "contabilidad"."corte_saldo" IS 'Corte de saldo. Es una cache derivada del diario, nunca la fuente de verdad. Se puede reconstruir por completo sumando los apuntes.';

COMMENT ON COLUMN "contabilidad"."corte_saldo"."ultimo_numero_apunte" IS 'Marca de agua: hasta que apunte esta calculado el saldo';

COMMENT ON COLUMN "contabilidad"."corte_saldo"."version_fila" IS 'Bloqueo optimista para serializar operaciones concurrentes sobre la misma cuenta';

COMMENT ON COLUMN "finanzas"."tipo_transaccion"."codigo" IS 'transferencia_interna, carga_saldo_simulado, retencion, liberacion, reverso';

COMMENT ON TABLE "finanzas"."clave_idempotencia" IS 'Garantiza que un doble toque o un reintento por perdida de conexion devuelvan el resultado de la primera operacion, HU-BE-NF-06 criterio 6.';

COMMENT ON COLUMN "finanzas"."clave_idempotencia"."operacion" IS 'Por ejemplo transferencia.crear';

COMMENT ON COLUMN "finanzas"."clave_idempotencia"."valor_clave" IS 'Clave de intencion enviada por el cliente';

COMMENT ON COLUMN "finanzas"."clave_idempotencia"."huella_solicitud" IS 'Hash del cuerpo. Detecta la misma clave con una peticion distinta';

COMMENT ON TABLE "finanzas"."transferencia" IS 'Intencion de transferencia intraentidad. El dinero lo mueve el asiento del libro, no esta tabla: aqui vive el estado de la operacion y su trazabilidad.';

COMMENT ON COLUMN "finanzas"."transferencia"."referencia_publica" IS 'Referencia legible que se muestra al usuario';

COMMENT ON COLUMN "finanzas"."transferencia"."celular_origen_e164" IS 'Copia historica. El celular de la cuenta puede cambiar despues';

COMMENT ON COLUMN "finanzas"."transferencia"."monto_cop" IS 'Entero positivo de pesos colombianos';

COMMENT ON COLUMN "finanzas"."transferencia"."codigo_motivo_rechazo" IS 'saldo_insuficiente, destinatario_no_existe, envio_a_si_mismo, monto_invalido';

COMMENT ON COLUMN "finanzas"."transferencia"."asiento_id" IS 'Asiento que materializo el movimiento. NULL si la transferencia fue rechazada';

COMMENT ON COLUMN "finanzas"."transferencia"."origen" IS 'Distingue una transferencia iniciada por el agente de IA de una iniciada en la interfaz';

COMMENT ON COLUMN "finanzas"."transferencia"."invocacion_herramienta_id" IS 'Invocacion del agente que la origino, cuando aplica';

COMMENT ON COLUMN "finanzas"."transferencia"."fecha_confirmacion" IS 'Confirmacion explicita del usuario antes de ejecutar';

COMMENT ON COLUMN "finanzas"."categoria_transaccion"."categoria_padre_id" IS 'Jerarquia de dos niveles: categoria y subcategoria';

COMMENT ON COLUMN "finanzas"."categoria_transaccion"."es_del_sistema" IS 'Falso en las categorias que crea el usuario';

COMMENT ON COLUMN "finanzas"."categoria_transaccion"."usuario_propietario_id" IS 'NULL en las categorias del catalogo base';

COMMENT ON TABLE "finanzas"."clasificacion_movimiento" IS 'La clasificacion vive fuera del diario porque el usuario puede recategorizar un movimiento, y el asiento contable es inmutable.';

COMMENT ON COLUMN "finanzas"."clasificacion_movimiento"."confianza" IS 'Confianza del clasificador cuando la asigna una regla o el agente';

COMMENT ON TABLE "finanzas"."presupuesto" IS 'Sostiene la pregunta que origina el proyecto: si una compra cabe o no en lo que queda del mes. Anticipa la epica de presupuesto, aun no redactada.';

COMMENT ON COLUMN "finanzas"."presupuesto"."categoria_transaccion_id" IS 'NULL en un presupuesto global del periodo';

COMMENT ON COLUMN "finanzas"."presupuesto"."monto_limite_cop" IS 'Entero positivo de pesos';

COMMENT ON TABLE "ia"."mensaje" IS 'Historial de la conversacion. Es de solo anexado: un turno ya emitido no se edita.';

COMMENT ON TABLE "ia"."herramienta" IS 'Conjunto cerrado y predefinido de funciones que el agente puede invocar. El modelo nunca genera SQL: solo puede pedir una de estas herramientas, y el backend inyecta la identidad del usuario autenticado antes de ejecutarla. Desactivar una herramienta con is_active la retira del agente sin desplegar codigo.';

COMMENT ON COLUMN "ia"."herramienta"."codigo" IS 'consultar_saldo, listar_movimientos, resumir_gastos, resolver_destinatario, enviar_dinero';

COMMENT ON COLUMN "ia"."herramienta"."esquema_entrada" IS 'Esquema JSON de los argumentos que el modelo puede enviar';

COMMENT ON COLUMN "ia"."invocacion_herramienta"."argumentos" IS 'Argumentos propuestos por el modelo, ya validados contra esquema_entrada';

COMMENT ON COLUMN "ia"."invocacion_herramienta"."fecha_confirmacion_usuario" IS 'Solo en herramientas que modifican dinero';

COMMENT ON TABLE "ia"."memoria_vectorial" IS 'Memoria conversacional del copiloto. La busqueda por similitud siempre se acota al usuario autenticado.';

COMMENT ON COLUMN "ia"."memoria_vectorial"."origen_id" IS 'Identificador del objeto de origen segun el valor de origen';

COMMENT ON COLUMN "ia"."memoria_vectorial"."vector_embebido" IS 'Extension pgvector. Indice HNSW con distancia coseno';

COMMENT ON TABLE "ia"."hallazgo" IS 'Patrones y observaciones que el agente deriva del historial. Es el apoyo a la toma de decisiones del objetivo general.';

COMMENT ON COLUMN "ia"."hallazgo"."codigo_hallazgo" IS 'pico_de_gasto, cobro_recurrente, riesgo_presupuesto, oportunidad_ahorro';

COMMENT ON TABLE "auditoria"."evento_auditoria" IS 'Bitacora de solo anexado. Cubre el requisito de trazabilidad y auditoria del alcance del anteproyecto.';

COMMENT ON COLUMN "auditoria"."evento_auditoria"."codigo_evento" IS 'usuario.registrado, sesion.abierta, transferencia.completada, ia.herramienta_denegada';

COMMENT ON COLUMN "auditoria"."evento_auditoria"."solicitud_id" IS 'Correlaciona todos los eventos de una misma peticion HTTP';

COMMENT ON COLUMN "auditoria"."evento_auditoria"."contenido_evento" IS 'Contexto ya redactado. El celular se enmascara y jamas se registran codigos ni contrasenas';

ALTER TABLE "identidad"."persona_natural" ADD FOREIGN KEY ("usuario_id") REFERENCES "identidad"."usuario" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "identidad"."persona_natural" ADD FOREIGN KEY ("tipo_documento_id") REFERENCES "identidad"."tipo_documento" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "identidad"."empresa" ADD FOREIGN KEY ("usuario_id") REFERENCES "identidad"."usuario" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "identidad"."representante_legal" ADD FOREIGN KEY ("empresa_id") REFERENCES "identidad"."empresa" ("usuario_id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "identidad"."representante_legal" ADD FOREIGN KEY ("tipo_documento_id") REFERENCES "identidad"."tipo_documento" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "identidad"."credencial_usuario" ADD FOREIGN KEY ("usuario_id") REFERENCES "identidad"."usuario" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "identidad"."dispositivo_usuario" ADD FOREIGN KEY ("usuario_id") REFERENCES "identidad"."usuario" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "identidad"."token_renovacion" ADD FOREIGN KEY ("usuario_id") REFERENCES "identidad"."usuario" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "identidad"."token_renovacion" ADD FOREIGN KEY ("dispositivo_usuario_id") REFERENCES "identidad"."dispositivo_usuario" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "identidad"."token_renovacion" ADD FOREIGN KEY ("rotado_a_id") REFERENCES "identidad"."token_renovacion" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "identidad"."desafio_verificacion" ADD FOREIGN KEY ("usuario_id") REFERENCES "identidad"."usuario" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "identidad"."desafio_verificacion" ADD FOREIGN KEY ("dispositivo_usuario_id") REFERENCES "identidad"."dispositivo_usuario" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "identidad"."envio_desafio" ADD FOREIGN KEY ("desafio_verificacion_id") REFERENCES "identidad"."desafio_verificacion" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "identidad"."permiso_un_solo_uso" ADD FOREIGN KEY ("usuario_id") REFERENCES "identidad"."usuario" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "identidad"."permiso_un_solo_uso" ADD FOREIGN KEY ("desafio_origen_id") REFERENCES "identidad"."desafio_verificacion" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "identidad"."lote_codigo_recuperacion" ADD FOREIGN KEY ("usuario_id") REFERENCES "identidad"."usuario" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "identidad"."codigo_recuperacion" ADD FOREIGN KEY ("lote_codigo_recuperacion_id") REFERENCES "identidad"."lote_codigo_recuperacion" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "contabilidad"."cuenta_contable" ADD FOREIGN KEY ("usuario_propietario_id") REFERENCES "identidad"."usuario" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "contabilidad"."asiento" ADD FOREIGN KEY ("tipo_transaccion_id") REFERENCES "finanzas"."tipo_transaccion" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "contabilidad"."asiento" ADD FOREIGN KEY ("asiento_reversado_id") REFERENCES "contabilidad"."asiento" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "contabilidad"."apunte" ADD FOREIGN KEY ("asiento_id") REFERENCES "contabilidad"."asiento" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "contabilidad"."apunte" ADD FOREIGN KEY ("cuenta_contable_id") REFERENCES "contabilidad"."cuenta_contable" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "contabilidad"."corte_saldo" ADD FOREIGN KEY ("cuenta_contable_id") REFERENCES "contabilidad"."cuenta_contable" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "finanzas"."clave_idempotencia" ADD FOREIGN KEY ("usuario_id") REFERENCES "identidad"."usuario" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "finanzas"."transferencia" ADD FOREIGN KEY ("usuario_origen_id") REFERENCES "identidad"."usuario" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "finanzas"."transferencia" ADD FOREIGN KEY ("usuario_destino_id") REFERENCES "identidad"."usuario" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "finanzas"."transferencia" ADD FOREIGN KEY ("asiento_id") REFERENCES "contabilidad"."asiento" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "finanzas"."transferencia" ADD FOREIGN KEY ("clave_idempotencia_id") REFERENCES "finanzas"."clave_idempotencia" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "finanzas"."transferencia" ADD FOREIGN KEY ("invocacion_herramienta_id") REFERENCES "ia"."invocacion_herramienta" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "finanzas"."categoria_transaccion" ADD FOREIGN KEY ("categoria_padre_id") REFERENCES "finanzas"."categoria_transaccion" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "finanzas"."categoria_transaccion" ADD FOREIGN KEY ("usuario_propietario_id") REFERENCES "identidad"."usuario" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "finanzas"."clasificacion_movimiento" ADD FOREIGN KEY ("asiento_id") REFERENCES "contabilidad"."asiento" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "finanzas"."clasificacion_movimiento" ADD FOREIGN KEY ("categoria_transaccion_id") REFERENCES "finanzas"."categoria_transaccion" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "finanzas"."presupuesto" ADD FOREIGN KEY ("usuario_id") REFERENCES "identidad"."usuario" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "finanzas"."presupuesto" ADD FOREIGN KEY ("categoria_transaccion_id") REFERENCES "finanzas"."categoria_transaccion" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "ia"."conversacion" ADD FOREIGN KEY ("usuario_id") REFERENCES "identidad"."usuario" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "ia"."mensaje" ADD FOREIGN KEY ("conversacion_id") REFERENCES "ia"."conversacion" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "ia"."invocacion_herramienta" ADD FOREIGN KEY ("mensaje_id") REFERENCES "ia"."mensaje" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "ia"."invocacion_herramienta" ADD FOREIGN KEY ("herramienta_id") REFERENCES "ia"."herramienta" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "ia"."memoria_vectorial" ADD FOREIGN KEY ("usuario_id") REFERENCES "identidad"."usuario" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "ia"."hallazgo" ADD FOREIGN KEY ("usuario_id") REFERENCES "identidad"."usuario" ("id") ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "ia"."hallazgo" ADD FOREIGN KEY ("categoria_transaccion_id") REFERENCES "finanzas"."categoria_transaccion" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "auditoria"."evento_auditoria" ADD FOREIGN KEY ("usuario_actor_id") REFERENCES "identidad"."usuario" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "auditoria"."evento_auditoria" ADD FOREIGN KEY ("dispositivo_usuario_id") REFERENCES "identidad"."dispositivo_usuario" ("id") DEFERRABLE INITIALLY IMMEDIATE;

-- ============================================================================
-- PARTE 2 · Restricciones de integridad que el diagrama no puede expresar
-- ============================================================================

-- --- Identidad -------------------------------------------------------------

ALTER TABLE "identidad"."usuario"
  ADD CONSTRAINT ck_usuario_celular_e164
      CHECK (celular_e164 ~ '^\+[1-9][0-9]{7,14}$'),
  ADD CONSTRAINT ck_usuario_correo
      CHECK (position('@' in correo_electronico) > 1),
  ADD CONSTRAINT ck_usuario_intentos_fallidos
      CHECK (intentos_fallidos_ingreso >= 0);

-- Una sola contrasena vigente por cuenta.
CREATE UNIQUE INDEX ux_credencial_una_vigente
  ON "identidad"."credencial_usuario" (usuario_id)
  WHERE es_vigente;

ALTER TABLE "identidad"."desafio_verificacion"
  ADD CONSTRAINT ck_desafio_intentos
      CHECK (intentos_usados >= 0 AND intentos_usados <= intentos_maximos),
  ADD CONSTRAINT ck_desafio_reenvios
      CHECK (reenvios_usados >= 0 AND reenvios_usados <= reenvios_maximos),
  ADD CONSTRAINT ck_desafio_expiracion
      CHECK (fecha_expiracion > created_on),
  ADD CONSTRAINT ck_desafio_motivo
      CHECK ((proposito = 'confirmacion_operacion' AND motivo_operacion IS NOT NULL)
          OR (proposito <> 'confirmacion_operacion' AND motivo_operacion IS NULL));

ALTER TABLE "identidad"."permiso_un_solo_uso"
  ADD CONSTRAINT ck_permiso_expiracion
      CHECK (fecha_expiracion > created_on);

ALTER TABLE "identidad"."codigo_recuperacion"
  ADD CONSTRAINT ck_codigo_recuperacion_orden
      CHECK (numero_orden BETWEEN 1 AND 8);

-- Un solo lote de codigos de recuperacion vigente por cuenta.
CREATE UNIQUE INDEX ux_lote_recuperacion_vigente
  ON "identidad"."lote_codigo_recuperacion" (usuario_id)
  WHERE fecha_invalidacion IS NULL;

ALTER TABLE "identidad"."token_renovacion"
  ADD CONSTRAINT ck_token_ventana_vigencia
      CHECK (vence_por_inactividad <= vence_absoluto);


-- --- Tipos de cuenta: persona natural y empresa ----------------------------

ALTER TABLE "identidad"."persona_natural"
  ADD CONSTRAINT ck_persona_documento
      CHECK (numero_documento ~ '^[A-Za-z0-9-]{5,20}$');

ALTER TABLE "identidad"."empresa"
  ADD CONSTRAINT ck_empresa_nit_digitos
      CHECK (nit ~ '^[0-9]{6,15}$'),
  ADD CONSTRAINT ck_empresa_digito_verificacion
      CHECK (digito_verificacion BETWEEN 0 AND 9);

ALTER TABLE "identidad"."representante_legal"
  ADD CONSTRAINT ck_representante_documento
      CHECK (numero_documento ~ '^[A-Za-z0-9-]{5,20}$'),
  ADD CONSTRAINT ck_representante_vigencia
      CHECK (fecha_fin IS NULL OR fecha_fin >= vigente_desde),
  ADD CONSTRAINT ck_representante_vigente_sin_fin
      CHECK (NOT es_vigente OR fecha_fin IS NULL);

-- Un solo representante legal vigente por empresa.
CREATE UNIQUE INDEX ux_representante_uno_vigente
  ON "identidad"."representante_legal" (empresa_id)
  WHERE es_vigente;

ALTER TABLE "identidad"."politica_limite"
  ADD CONSTRAINT ck_politica_maximo
      CHECK (maximo_solicitudes > 0),
  ADD CONSTRAINT ck_politica_ventana
      CHECK (ventana_segundos > 0),
  ADD CONSTRAINT ck_politica_bloqueo
      CHECK (bloqueo_inicial_segundos > 0
         AND bloqueo_maximo_segundos >= bloqueo_inicial_segundos
         AND factor_crecimiento >= 1.0),
  ADD CONSTRAINT ck_politica_sujeto
      CHECK (sujeto_limite IN ('cuenta', 'origen_ip', 'celular'));

-- --- Libro contable --------------------------------------------------------

ALTER TABLE "contabilidad"."cuenta_contable"
  ADD CONSTRAINT ck_cuenta_contable_moneda
      CHECK (moneda = 'COP'),
  ADD CONSTRAINT ck_cuenta_contable_propietario
      CHECK (
        (proposito IN ('usuario_disponible', 'usuario_retenido') AND usuario_propietario_id IS NOT NULL)
        OR
        (proposito NOT IN ('usuario_disponible', 'usuario_retenido') AND usuario_propietario_id IS NULL)
      );

-- El indice generado permite varias cuentas de sistema con el mismo proposito
-- porque en PostgreSQL dos NULL no colisionan. Se reemplaza por dos indices
-- parciales que si cierran esa puerta.
DROP INDEX IF EXISTS "contabilidad"."ux_cuenta_contable_propietario_proposito";

CREATE UNIQUE INDEX ux_cuenta_contable_usuario_proposito
  ON "contabilidad"."cuenta_contable" (usuario_propietario_id, proposito)
  WHERE usuario_propietario_id IS NOT NULL;

CREATE UNIQUE INDEX ux_cuenta_contable_sistema_proposito
  ON "contabilidad"."cuenta_contable" (proposito)
  WHERE usuario_propietario_id IS NULL;

ALTER TABLE "contabilidad"."asiento"
  ADD CONSTRAINT ck_asiento_moneda
      CHECK (moneda = 'COP'),
  ADD CONSTRAINT ck_asiento_no_se_reversa_solo
      CHECK (asiento_reversado_id IS NULL OR asiento_reversado_id <> id);

ALTER TABLE "contabilidad"."apunte"
  ADD CONSTRAINT ck_apunte_monto_no_cero
      CHECK (monto_cop <> 0),
  ADD CONSTRAINT ck_apunte_signo_naturaleza
      CHECK (
        (naturaleza_apunte = 'debito'  AND monto_cop > 0) OR
        (naturaleza_apunte = 'credito' AND monto_cop < 0)
      ),
  ADD CONSTRAINT ck_apunte_orden
      CHECK (orden_en_asiento >= 1);

ALTER TABLE "contabilidad"."corte_saldo"
  ADD CONSTRAINT ck_corte_marca_agua
      CHECK (ultimo_numero_apunte >= 0),
  ADD CONSTRAINT ck_corte_version
      CHECK (version_fila >= 1);

-- --- Nucleo financiero -----------------------------------------------------

ALTER TABLE "finanzas"."transferencia"
  ADD CONSTRAINT ck_transferencia_monto_positivo
      CHECK (monto_cop > 0),
  ADD CONSTRAINT ck_transferencia_no_a_si_mismo
      CHECK (usuario_origen_id <> usuario_destino_id),
  ADD CONSTRAINT ck_transferencia_moneda
      CHECK (moneda = 'COP'),
  ADD CONSTRAINT ck_transferencia_completada_con_asiento
      CHECK (estado <> 'completada' OR asiento_id IS NOT NULL),
  ADD CONSTRAINT ck_transferencia_rechazada_con_motivo
      CHECK (estado <> 'rechazada' OR codigo_motivo_rechazo IS NOT NULL);

ALTER TABLE "finanzas"."categoria_transaccion"
  ADD CONSTRAINT ck_categoria_no_es_su_padre
      CHECK (categoria_padre_id IS NULL OR categoria_padre_id <> id),
  ADD CONSTRAINT ck_categoria_propietario
      CHECK ((es_del_sistema AND usuario_propietario_id IS NULL) OR (NOT es_del_sistema AND usuario_propietario_id IS NOT NULL));

ALTER TABLE "finanzas"."clasificacion_movimiento"
  ADD CONSTRAINT ck_clasificacion_confianza
      CHECK (confianza IS NULL OR (confianza >= 0 AND confianza <= 1));

ALTER TABLE "finanzas"."presupuesto"
  ADD CONSTRAINT ck_presupuesto_limite_positivo
      CHECK (monto_limite_cop > 0),
  ADD CONSTRAINT ck_presupuesto_periodo
      CHECK (fecha_fin >= fecha_inicio),
  ADD CONSTRAINT ck_presupuesto_umbral
      CHECK (umbral_alerta_porcentaje BETWEEN 1 AND 100);

ALTER TABLE "finanzas"."clave_idempotencia"
  ADD CONSTRAINT ck_idempotencia_expiracion
      CHECK (fecha_expiracion > created_on);

-- --- Agente de IA ----------------------------------------------------------

ALTER TABLE "ia"."mensaje"
  ADD CONSTRAINT ck_mensaje_orden
      CHECK (numero_orden >= 1),
  ADD CONSTRAINT ck_mensaje_tokens
      CHECK ((tokens_entrada IS NULL OR tokens_entrada >= 0)
         AND (tokens_salida IS NULL OR tokens_salida >= 0));

ALTER TABLE "ia"."herramienta"
  ADD CONSTRAINT ck_herramienta_exige_confirmacion
      CHECK (NOT modifica_datos OR exige_confirmacion_usuario);

ALTER TABLE "ia"."hallazgo"
  ADD CONSTRAINT ck_hallazgo_relevancia
      CHECK (relevancia IS NULL OR (relevancia >= 0 AND relevancia <= 1));

-- ============================================================================
-- PARTE 3 · Reglas de negocio implementadas con disparadores
-- ============================================================================

-- --- 3.1 Sello automatico de updated_on ------------------------------------

CREATE OR REPLACE FUNCTION auditoria.fn_sellar_updated_on()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  NEW.updated_on := now();
  RETURN NEW;
END;
$$;

DO $$
DECLARE r record;
BEGIN
  FOR r IN
    SELECT c.table_schema, c.table_name
    FROM information_schema.columns c
    WHERE c.column_name = 'updated_on'
      AND c.table_schema IN ('identidad', 'contabilidad', 'finanzas', 'ia', 'auditoria')
  LOOP
    EXECUTE format(
      'CREATE TRIGGER tg_sellar_updated_on BEFORE UPDATE ON %I.%I
         FOR EACH ROW EXECUTE FUNCTION auditoria.fn_sellar_updated_on()',
      r.table_schema, r.table_name);
  END LOOP;
END;
$$;

-- --- 3.2 Inmutabilidad del diario y de la bitacora -------------------------
-- HU-BE-NF-05 criterio 11 y HU-BE-NF-06 criterio 8: un movimiento no se edita
-- ni se borra. La correccion es un asiento nuevo en sentido contrario.

CREATE OR REPLACE FUNCTION auditoria.fn_solo_anexado()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION
    'La tabla %.% es de solo anexado y no admite %. Registre un asiento en sentido contrario.',
    TG_TABLE_SCHEMA, TG_TABLE_NAME, TG_OP
    USING ERRCODE = 'restrict_violation';
END;
$$;

CREATE TRIGGER tg_asiento_solo_anexado
  BEFORE UPDATE OR DELETE ON "contabilidad"."asiento"
  FOR EACH ROW EXECUTE FUNCTION auditoria.fn_solo_anexado();

CREATE TRIGGER tg_apunte_solo_anexado
  BEFORE UPDATE OR DELETE ON "contabilidad"."apunte"
  FOR EACH ROW EXECUTE FUNCTION auditoria.fn_solo_anexado();

CREATE TRIGGER tg_mensaje_solo_anexado
  BEFORE UPDATE OR DELETE ON "ia"."mensaje"
  FOR EACH ROW EXECUTE FUNCTION auditoria.fn_solo_anexado();

CREATE TRIGGER tg_evento_solo_anexado
  BEFORE UPDATE OR DELETE ON "auditoria"."evento_auditoria"
  FOR EACH ROW EXECUTE FUNCTION auditoria.fn_solo_anexado();

-- --- 3.3 Partida doble: todo asiento suma cero -----------------------------
-- Se evalua al cerrar la transaccion, no apunte por apunte, porque un asiento
-- se construye con dos o mas inserciones sucesivas.

CREATE OR REPLACE FUNCTION contabilidad.fn_asiento_debe_cuadrar()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  v_total    bigint;
  v_apuntes int;
BEGIN
  SELECT coalesce(sum(monto_cop), 0), count(*)
    INTO v_total, v_apuntes
    FROM "contabilidad"."apunte"
   WHERE asiento_id = NEW.asiento_id;

  IF v_apuntes < 2 THEN
    RAISE EXCEPTION 'El asiento % tiene % apunte(s): la partida doble exige al menos dos.',
      NEW.asiento_id, v_apuntes
      USING ERRCODE = 'check_violation';
  END IF;

  IF v_total <> 0 THEN
    RAISE EXCEPTION 'El asiento % no cuadra: la suma de sus apuntes es % y debe ser cero.',
      NEW.asiento_id, v_total
      USING ERRCODE = 'check_violation';
  END IF;

  RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER tg_asiento_debe_cuadrar
  AFTER INSERT ON "contabilidad"."apunte"
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION contabilidad.fn_asiento_debe_cuadrar();

-- --- 3.4 Un apunte no puede mezclar monedas ni cuentas inactivas -----------

CREATE OR REPLACE FUNCTION contabilidad.fn_validar_cuenta_apunte()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE v_activa boolean;
BEGIN
  SELECT is_active INTO v_activa
    FROM "contabilidad"."cuenta_contable"
   WHERE id = NEW.cuenta_contable_id;

  IF NOT v_activa THEN
    RAISE EXCEPTION 'La cuenta contable % esta cerrada y no admite apuntes.',
      NEW.cuenta_contable_id
      USING ERRCODE = 'check_violation';
  END IF;

  RETURN NEW;
END;
$$;

CREATE TRIGGER tg_validar_cuenta_apunte
  BEFORE INSERT ON "contabilidad"."apunte"
  FOR EACH ROW EXECUTE FUNCTION contabilidad.fn_validar_cuenta_apunte();

-- --- 3.5 Mantenimiento del corte de saldo ----------------------------------
-- El corte es una cache: se recalcula sola con cada apunte y siempre puede
-- reconstruirse desde el diario con contabilidad.fn_reconstruir_saldo().

CREATE OR REPLACE FUNCTION contabilidad.fn_aplicar_apunte_a_corte()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  INSERT INTO "contabilidad"."corte_saldo"
      (cuenta_contable_id, saldo_cop, ultimo_numero_apunte, version_fila, fecha_calculo, created_by)
  VALUES
      (NEW.cuenta_contable_id, NEW.monto_cop, NEW.numero_apunte, 1, now(), NEW.created_by)
  ON CONFLICT (cuenta_contable_id) DO UPDATE
     SET saldo_cop         = "corte_saldo".saldo_cop + EXCLUDED.saldo_cop,
         ultimo_numero_apunte = GREATEST("corte_saldo".ultimo_numero_apunte,
                                        EXCLUDED.ultimo_numero_apunte),
         version_fila         = "corte_saldo".version_fila + 1,
         fecha_calculo         = now();
  RETURN NULL;
END;
$$;

CREATE TRIGGER tg_aplicar_apunte_a_corte
  AFTER INSERT ON "contabilidad"."apunte"
  FOR EACH ROW EXECUTE FUNCTION contabilidad.fn_aplicar_apunte_a_corte();

CREATE OR REPLACE FUNCTION contabilidad.fn_reconstruir_saldo(p_cuenta_id uuid)
RETURNS bigint
LANGUAGE plpgsql
AS $$
DECLARE
  v_saldo bigint;
  v_ultimo    bigint;
BEGIN
  SELECT coalesce(sum(monto_cop), 0), coalesce(max(numero_apunte), 0)
    INTO v_saldo, v_ultimo
    FROM "contabilidad"."apunte"
   WHERE cuenta_contable_id = p_cuenta_id;

  INSERT INTO "contabilidad"."corte_saldo"
      (cuenta_contable_id, saldo_cop, ultimo_numero_apunte, fecha_calculo)
  VALUES (p_cuenta_id, v_saldo, v_ultimo, now())
  ON CONFLICT (cuenta_contable_id) DO UPDATE
     SET saldo_cop         = EXCLUDED.saldo_cop,
         ultimo_numero_apunte = EXCLUDED.ultimo_numero_apunte,
         version_fila         = "corte_saldo".version_fila + 1,
         fecha_calculo         = now();

  RETURN v_saldo;
END;
$$;

-- --- 3.6 Apertura automatica de las subcuentas del usuario -----------------

CREATE OR REPLACE FUNCTION contabilidad.fn_abrir_cuentas_usuario()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  INSERT INTO "contabilidad"."cuenta_contable"
      (codigo, tipo_cuenta, naturaleza, proposito, usuario_propietario_id, created_by)
  VALUES
      ('pasivo:usuario:' || NEW.id || ':disponible', 'pasivo', 'credito',
       'usuario_disponible', NEW.id, NEW.created_by),
      ('pasivo:usuario:' || NEW.id || ':retenido', 'pasivo', 'credito',
       'usuario_retenido', NEW.id, NEW.created_by);
  RETURN NULL;
END;
$$;

CREATE TRIGGER tg_abrir_cuentas_usuario
  AFTER INSERT ON "identidad"."usuario"
  FOR EACH ROW EXECUTE FUNCTION contabilidad.fn_abrir_cuentas_usuario();

-- --- 3.7 El saldo disponible nunca queda negativo --------------------------
-- Impide el doble gasto ante dos transferencias simultaneas desde la misma
-- cuenta (HU-BE-NF-06, definicion de terminado).

CREATE OR REPLACE FUNCTION contabilidad.fn_sin_sobregiro()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  v_proposito "proposito_cuenta";
  v_saldo bigint;
BEGIN
  SELECT a.proposito, s.saldo_cop
    INTO v_proposito, v_saldo
    FROM "contabilidad"."cuenta_contable" a
    JOIN "contabilidad"."corte_saldo" s ON s.cuenta_contable_id = a.id
   WHERE a.id = NEW.cuenta_contable_id
   FOR UPDATE OF s;

  -- Las cuentas de pasivo del usuario viven en negativo: un saldo a favor de
  -- 50.000 pesos se representa como -50000. Que el saldo supere cero significa
  -- que el usuario debe dinero, es decir, que gasto mas de lo que tenia.
  IF v_proposito IN ('usuario_disponible', 'usuario_retenido') AND v_saldo > 0 THEN
    RAISE EXCEPTION 'Saldo insuficiente en la cuenta %.', NEW.cuenta_contable_id
      USING ERRCODE = 'check_violation';
  END IF;

  RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER tg_sin_sobregiro
  AFTER INSERT ON "contabilidad"."apunte"
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION contabilidad.fn_sin_sobregiro();


-- --- 3.8 Digito de verificacion del NIT ------------------------------------
-- Algoritmo de modulo 11 de la DIAN. Evita que una empresa quede registrada
-- con un NIT mal digitado, que despues es muy costoso de corregir.

CREATE OR REPLACE FUNCTION identidad.fn_digito_verificacion_nit(p_nit text)
RETURNS smallint
LANGUAGE plpgsql
IMMUTABLE
AS $$
DECLARE
  v_pesos int[] := ARRAY[3,7,13,17,19,23,29,37,41,43,47,53,59,67,71];
  v_suma  int := 0;
  v_resto int;
  v_len   int := length(p_nit);
  i       int;
BEGIN
  IF p_nit !~ '^[0-9]+$' OR v_len > 15 THEN
    RAISE EXCEPTION 'El NIT % no es una cadena valida de digitos.', p_nit
      USING ERRCODE = 'check_violation';
  END IF;

  FOR i IN 1..v_len LOOP
    -- Se recorre de derecha a izquierda aplicando el peso correspondiente.
    v_suma := v_suma + substr(p_nit, v_len - i + 1, 1)::int * v_pesos[i];
  END LOOP;

  v_resto := v_suma % 11;
  RETURN CASE WHEN v_resto IN (0, 1) THEN v_resto ELSE 11 - v_resto END;
END;
$$;

ALTER TABLE "identidad"."empresa"
  ADD CONSTRAINT ck_empresa_nit_valido
      CHECK (digito_verificacion = identidad.fn_digito_verificacion_nit(nit));

-- --- 3.9 Coherencia entre el tipo de cuenta y su subtipo -------------------
-- Una cuenta marcada como persona debe tener su fila en persona_natural, y una
-- marcada como empresa la suya en empresa. Se evalua al cerrar la transaccion
-- porque el usuario y su subtipo se insertan en pasos sucesivos.

CREATE OR REPLACE FUNCTION identidad.fn_validar_subtipo_usuario()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  v_tipo    "tipo_cuenta";
  v_persona boolean;
  v_empresa boolean;
BEGIN
  SELECT tipo_cuenta INTO v_tipo FROM "identidad"."usuario" WHERE id = NEW.id;
  IF v_tipo IS NULL THEN
    RETURN NULL;  -- la cuenta se elimino en la misma transaccion
  END IF;

  SELECT EXISTS (SELECT 1 FROM "identidad"."persona_natural" WHERE usuario_id = NEW.id),
         EXISTS (SELECT 1 FROM "identidad"."empresa"         WHERE usuario_id = NEW.id)
    INTO v_persona, v_empresa;

  IF v_tipo = 'persona' AND NOT v_persona THEN
    RAISE EXCEPTION 'La cuenta % es de tipo persona y no tiene su fila en identidad.persona_natural.', NEW.id
      USING ERRCODE = 'check_violation';
  END IF;

  IF v_tipo = 'empresa' AND NOT v_empresa THEN
    RAISE EXCEPTION 'La cuenta % es de tipo empresa y no tiene su fila en identidad.empresa.', NEW.id
      USING ERRCODE = 'check_violation';
  END IF;

  IF v_persona AND v_empresa THEN
    RAISE EXCEPTION 'La cuenta % no puede ser persona y empresa al mismo tiempo.', NEW.id
      USING ERRCODE = 'check_violation';
  END IF;

  RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER tg_validar_subtipo_usuario
  AFTER INSERT ON "identidad"."usuario"
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION identidad.fn_validar_subtipo_usuario();


-- --- 3.10 Espera creciente del racionamiento ------------------------------
-- Devuelve cuantos segundos debe esperar un sujeto que acaba de superar su
-- limite. Crece de forma geometrica desde bloqueo_inicial_segundos hasta
-- bloqueo_maximo_segundos y nunca se convierte en un bloqueo permanente.

CREATE OR REPLACE FUNCTION identidad.fn_espera_creciente(
  p_ambito  "ambito_limite",
  p_sujeto  varchar,
  p_ventana integer,
  p_violaciones smallint)
RETURNS integer
LANGUAGE plpgsql
STABLE
AS $$
DECLARE
  v_inicial int;
  v_maximo  int;
  v_factor  numeric;
  v_espera  numeric;
BEGIN
  SELECT bloqueo_inicial_segundos, bloqueo_maximo_segundos, factor_crecimiento
    INTO v_inicial, v_maximo, v_factor
    FROM "identidad"."politica_limite"
   WHERE ambito = p_ambito
     AND sujeto_limite = p_sujeto
     AND ventana_segundos = p_ventana
     AND is_active;

  IF v_inicial IS NULL THEN
    RETURN 0;  -- sin politica definida, no se aplica espera
  END IF;

  v_espera := v_inicial * power(v_factor, GREATEST(p_violaciones - 1, 0));
  RETURN LEAST(v_espera, v_maximo)::int;
END;
$$;

-- ============================================================================
-- PARTE 4 · Indices adicionales de consulta
-- ============================================================================

-- Historial del usuario: recorre sus dos subcuentas en orden descendente.
CREATE INDEX ix_apunte_historial
  ON "contabilidad"."apunte" (cuenta_contable_id, numero_apunte DESC);

-- Busqueda por similitud de la memoria conversacional (pgvector, coseno).
CREATE INDEX ix_memoria_hnsw
  ON "ia"."memoria_vectorial"
  USING hnsw (vector_embebido vector_cosine_ops);

-- Purga de desafios y permisos vencidos.
CREATE INDEX ix_desafio_pendiente
  ON "identidad"."desafio_verificacion" (fecha_expiracion)
  WHERE estado = 'pendiente';

CREATE INDEX ix_permiso_emitido
  ON "identidad"."permiso_un_solo_uso" (fecha_expiracion)
  WHERE estado = 'emitido';

-- Tokens de renovacion vigentes.
CREATE INDEX ix_token_vigente
  ON "identidad"."token_renovacion" (usuario_id, vence_absoluto)
  WHERE fecha_revocacion IS NULL AND fecha_uso IS NULL;

-- ============================================================================
-- PARTE 5 · Vistas de consulta
-- ============================================================================

-- Saldo disponible de cada usuario, ya invertido al signo que espera la
-- aplicacion: lo que entra se ve positivo y lo que sale, negativo.
CREATE OR REPLACE VIEW finanzas.v_saldo_usuario AS
SELECT
  a.usuario_propietario_id                       AS usuario_id,
  a.proposito,
  (-1 * coalesce(s.saldo_cop, 0))::bigint AS saldo_cop,
  a.moneda,
  s.fecha_calculo
FROM "contabilidad"."cuenta_contable" a
LEFT JOIN "contabilidad"."corte_saldo" s ON s.cuenta_contable_id = a.id
WHERE a.usuario_propietario_id IS NOT NULL
  AND a.is_active;

-- Historial de movimientos: agrupa en una sola fila los apuntes de un mismo
-- asiento que tocan las dos subcuentas del usuario (HU-BE-NF-05 criterio 9).
CREATE OR REPLACE VIEW finanzas.v_movimiento_usuario AS
SELECT
  a.usuario_propietario_id                        AS usuario_id,
  e.id                                   AS asiento_id,
  max(p.numero_apunte)                  AS posicion_cursor,
  e.descripcion,
  t.codigo                                 AS codigo_tipo_transaccion,
  c.codigo                                 AS codigo_categoria,
  (-1 * sum(p.monto_cop))::bigint       AS monto_cop,
  e.moneda,
  e.fecha_hecho,
  e.fecha_registro
FROM "contabilidad"."apunte" p
JOIN "contabilidad"."cuenta_contable" a  ON a.id = p.cuenta_contable_id
JOIN "contabilidad"."asiento" e   ON e.id = p.asiento_id
JOIN "finanzas"."tipo_transaccion" t ON t.id = e.tipo_transaccion_id
LEFT JOIN "finanzas"."clasificacion_movimiento" mc
       ON mc.asiento_id = e.id AND mc.is_active
LEFT JOIN "finanzas"."categoria_transaccion" c ON c.id = mc.categoria_transaccion_id
WHERE a.usuario_propietario_id IS NOT NULL
GROUP BY a.usuario_propietario_id, e.id, e.descripcion, t.codigo, c.codigo,
         e.moneda, e.fecha_hecho, e.fecha_registro;


-- Cuenta con su nombre para mostrar, sin que el cliente tenga que saber si es
-- persona o empresa. Es lo que devuelven /auth/register y /auth/login/verify-otp.
CREATE OR REPLACE VIEW identidad.v_usuario AS
SELECT
  u.id,
  u.tipo_cuenta,
  u.celular_e164,
  u.correo_electronico,
  u.estado,
  coalesce(p.nombre_completo, e.razon_social)            AS nombre_mostrado,
  p.nombre_completo,
  e.razon_social,
  CASE WHEN e.nit IS NOT NULL
       THEN e.nit || '-' || e.digito_verificacion::text
  END                                                    AS nit_completo,
  u.created_on                                           AS fecha_creacion
FROM "identidad"."usuario" u
LEFT JOIN "identidad"."persona_natural" p ON p.usuario_id = u.id
LEFT JOIN "identidad"."empresa"         e ON e.usuario_id = u.id
WHERE u.is_active;

-- ============================================================================
-- PARTE 6 · Datos base
-- ============================================================================


INSERT INTO "identidad"."tipo_documento" (codigo, nombre, descripcion, aplica_persona, aplica_empresa) VALUES
  ('CC',  'Cedula de ciudadania',        'Documento de identidad de nacionales colombianos', true,  false),
  ('CE',  'Cedula de extranjeria',       'Documento de identidad de extranjeros residentes', true,  false),
  ('PA',  'Pasaporte',                   'Documento de viaje',                               true,  false),
  ('NIT', 'Numero de identificacion tributaria', 'Identificacion de personas juridicas',     false, true);

-- Espera creciente de 30 segundos hasta 1 hora, nunca bloqueo permanente
-- (HU-BE-AUT-03, criterio 9). El acumulado de 10 intentos fallidos en 24 horas
-- convive con el limite horario porque la ventana hace parte de la llave.
INSERT INTO "identidad"."politica_limite"
  (ambito, sujeto_limite, maximo_solicitudes, ventana_segundos,
   bloqueo_inicial_segundos, bloqueo_maximo_segundos, factor_crecimiento) VALUES
  ('registro',               'origen_ip', 20,  3600, 30, 3600, 2.0),
  ('solicitud_codigo',       'cuenta',     5,  3600, 30, 3600, 2.0),
  ('solicitud_codigo',       'origen_ip', 20,  3600, 30, 3600, 2.0),
  ('verificacion_codigo',    'cuenta',     5,  3600, 30, 3600, 2.0),
  ('verificacion_codigo',    'origen_ip', 20,  3600, 30, 3600, 2.0),
  ('verificacion_codigo',    'cuenta',    10, 86400, 60, 3600, 2.0),
  ('ingreso_sesion',         'cuenta',    10,   900, 30, 3600, 2.0),
  ('consulta_destinatario',  'cuenta',    30,  3600, 30,  600, 2.0),
  ('creacion_transferencia', 'cuenta',    20,  3600, 30,  600, 2.0);

INSERT INTO "finanzas"."tipo_transaccion" (codigo, nombre, descripcion, afecta_disponible) VALUES
  ('transferencia_interna', 'Transferencia interna',  'Envio de dinero entre dos usuarios de la billetera', true),
  ('carga_saldo_simulado', 'Carga de saldo simulado','Ingreso de dinero virtual desde la cuenta semilla',  true),
  ('retencion',              'Retencion',              'Traslado de disponible a retenido',                  true),
  ('liberacion',           'Liberacion',             'Traslado de retenido a disponible',                  true),
  ('reverso',          'Reverso',                'Correccion de un asiento previo en sentido contrario', true);

INSERT INTO "contabilidad"."cuenta_contable" (codigo, tipo_cuenta, naturaleza, proposito) VALUES
  ('activo:sistema:semilla',    'activo',  'debito',  'sistema_semilla'),
  ('activo:sistema:transitoria','activo',  'debito',  'sistema_transitoria'),
  ('ingreso:sistema:comisiones','ingreso', 'credito', 'sistema_comisiones');

INSERT INTO "finanzas"."categoria_transaccion" (codigo, nombre, descripcion) VALUES
  ('alimentacion',    'Alimentacion',     'Mercado, restaurantes y domicilios'),
  ('transporte',      'Transporte',       'Transporte publico, combustible y aplicaciones de movilidad'),
  ('vivienda',        'Vivienda',         'Arriendo, administracion y mantenimiento'),
  ('servicios',       'Servicios',        'Energia, agua, gas, internet y telefonia'),
  ('salud',           'Salud',            'Consultas, medicamentos y seguros'),
  ('educacion',       'Educacion',        'Matriculas, cursos y material de estudio'),
  ('entretenimiento', 'Entretenimiento',  'Suscripciones, salidas y ocio'),
  ('compras',         'Compras',          'Ropa, tecnologia y hogar'),
  ('transferencias',  'Transferencias',   'Envios y recepciones entre usuarios'),
  ('ingresos',        'Ingresos',         'Entradas de dinero'),
  ('ahorro',          'Ahorro',           'Dinero apartado para una meta'),
  ('otros',           'Otros',            'Movimientos sin clasificar');

INSERT INTO "ia"."herramienta"
  (codigo, nombre, descripcion, esquema_entrada, modifica_datos, exige_confirmacion_usuario) VALUES
  ('consultar_saldo', 'Consultar saldo',
   'Devuelve el saldo disponible y el retenido del usuario autenticado.',
   '{"type":"object","properties":{},"additionalProperties":false}'::jsonb,
   false, false),

  ('listar_movimientos', 'Listar movimientos',
   'Devuelve los movimientos del usuario en un rango de fechas, con paginacion por cursor.',
   '{"type":"object","properties":{"desde":{"type":"string","format":"date"},"hasta":{"type":"string","format":"date"},"cursor":{"type":"integer"},"limite":{"type":"integer","maximum":50}},"additionalProperties":false}'::jsonb,
   false, false),

  ('resumir_gastos', 'Resumir gastos',
   'Agrupa el gasto del usuario por categoria en un periodo y devuelve los totales.',
   '{"type":"object","properties":{"desde":{"type":"string","format":"date"},"hasta":{"type":"string","format":"date"},"agrupar_por":{"type":"string","enum":["categoria","mes"]}},"additionalProperties":false}'::jsonb,
   false, false),

  ('evaluar_si_alcanza', 'Evaluar si alcanza',
   'Compara un gasto propuesto contra el saldo disponible y los presupuestos vigentes del periodo.',
   '{"type":"object","properties":{"monto_cop":{"type":"integer","minimum":1},"codigo_categoria":{"type":"string"}},"required":["monto_cop"],"additionalProperties":false}'::jsonb,
   false, false),

  ('resolver_destinatario', 'Resolver destinatario',
   'Indica si un numero de celular corresponde a un usuario de la billetera y devuelve su nombre enmascarado.',
   '{"type":"object","properties":{"celular_e164":{"type":"string"}},"required":["celular_e164"],"additionalProperties":false}'::jsonb,
   false, false),

  ('enviar_dinero', 'Enviar dinero',
   'Crea una transferencia interna. Exige confirmacion explicita del usuario antes de ejecutarse.',
   '{"type":"object","properties":{"celular_e164":{"type":"string"},"monto_cop":{"type":"integer","minimum":1},"mensaje":{"type":"string","maxLength":140},"clave_idempotencia":{"type":"string"}},"required":["celular_e164","monto_cop","clave_idempotencia"],"additionalProperties":false}'::jsonb,
   true, true);

-- ============================================================================
-- Fin del esquema
-- ============================================================================

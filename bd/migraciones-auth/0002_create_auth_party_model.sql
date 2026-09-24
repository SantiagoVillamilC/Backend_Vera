-- Modelo de partes, roles y credenciales de auth (AUT-A1-01). No toca public.users ni el núcleo.

-- +goose Up
CREATE TABLE auth.partes (
    id        uuid PRIMARY KEY,
    tipo      text NOT NULL CHECK (tipo IN ('persona','empresa')),
    creada_en timestamptz NOT NULL DEFAULT now()
);

-- La fila es opcional: una parte heredada de users no tiene documento, así que no lleva fila acá.
CREATE TABLE auth.personas (
    parte_id       uuid PRIMARY KEY REFERENCES auth.partes(id),
    tipo_documento text NOT NULL CHECK (tipo_documento IN ('CC','CE','PA')),
    documento      text NOT NULL,
    nombre         text NOT NULL,
    perfil_nombre  text,
    nacida_en      date,
    CONSTRAINT personas_documento_uk UNIQUE (tipo_documento, documento)
);

CREATE TABLE auth.empresas (
    parte_id     uuid PRIMARY KEY REFERENCES auth.partes(id),
    nit          text NOT NULL,
    razon_social text NOT NULL,
    CONSTRAINT empresas_nit_uk UNIQUE (nit)
);

-- El representante cubre varias empresas con una sola credencial: una fila por (parte, rol, representada).
CREATE TABLE auth.roles_de_parte (
    id              bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
    parte_id        uuid NOT NULL REFERENCES auth.partes(id),
    rol             text NOT NULL CHECK (rol IN ('representante_legal','administrador','beneficiario_final')),
    representada_id uuid NOT NULL REFERENCES auth.partes(id),
    creado_en       timestamptz NOT NULL DEFAULT now(),
    revocado_en     timestamptz
);

CREATE UNIQUE INDEX roles_de_parte_vigente_uk
    ON auth.roles_de_parte (parte_id, rol, representada_id)
    WHERE revocado_en IS NULL;

CREATE TABLE auth.credenciales (
    id                    uuid PRIMARY KEY,
    parte_id              uuid NOT NULL REFERENCES auth.partes(id),
    correo                text NOT NULL,
    celular               text NOT NULL,
    celular_verificado_en timestamptz,
    correo_verificado_en  timestamptz,
    celular_cambiado_en   timestamptz,
    valor_derivado        text,
    estado                text NOT NULL CHECK (estado IN ('pendiente_verificacion','activo','suspendido','cerrado','sin_login')),
    creada_en             timestamptz NOT NULL DEFAULT now(),
    actualizada_en        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT credenciales_parte_id_uk UNIQUE (parte_id),
    -- sin_login no tiene contraseña; cualquier otro estado exige el valor derivado.
    CONSTRAINT credenciales_valor_derivado_requerido CHECK (estado = 'sin_login' OR valor_derivado IS NOT NULL)
);

-- Índices parciales: cerrar una cuenta libera su correo y su celular para otra.
CREATE UNIQUE INDEX credenciales_correo_uk ON auth.credenciales (correo) WHERE estado <> 'cerrado';
CREATE UNIQUE INDEX credenciales_celular_uk ON auth.credenciales (celular) WHERE estado <> 'cerrado';

CREATE TABLE auth.transiciones_credencial (
    id              bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
    credencial_id   uuid NOT NULL REFERENCES auth.credenciales(id),
    estado_anterior text,
    estado_nuevo    text NOT NULL,
    motivo          text,
    ocurrida_en     timestamptz NOT NULL DEFAULT now()
);

GRANT SELECT, INSERT, UPDATE ON auth.partes, auth.personas, auth.empresas, auth.roles_de_parte, auth.credenciales TO rol_auth;
GRANT SELECT, INSERT ON auth.transiciones_credencial TO rol_auth;

-- Historial append-only: ni UPDATE, ni DELETE, ni TRUNCATE.
REVOKE UPDATE, DELETE, TRUNCATE ON auth.transiciones_credencial FROM rol_auth;

-- +goose Down
DROP TABLE auth.transiciones_credencial;
DROP TABLE auth.credenciales;
DROP TABLE auth.roles_de_parte;
DROP TABLE auth.empresas;
DROP TABLE auth.personas;
DROP TABLE auth.partes;

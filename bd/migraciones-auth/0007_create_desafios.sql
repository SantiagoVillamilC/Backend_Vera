-- Desafíos OTP: código derivado, entrega y vigencia corta (AUT-A2-06.1).

-- +goose Up
CREATE TABLE auth.desafios (
    id                  uuid PRIMARY KEY,
    parte_id            uuid REFERENCES auth.partes(id),
    credencial_id       uuid,
    proposito           text NOT NULL CHECK (proposito IN ('registro','login','recuperacion','cambio_numero','cambio_numero_viejo')),
    celular_destino     text NOT NULL,
    codigo_hmac         bytea NOT NULL,
    intentos            integer NOT NULL DEFAULT 0,
    reenvios            integer NOT NULL DEFAULT 0,
    estado              text NOT NULL CHECK (estado IN ('pendiente','verificado','invalidado','expirado')),
    creado_en           timestamptz NOT NULL,
    expira_en           timestamptz NOT NULL,
    ultimo_envio_en     timestamptz NOT NULL,
    contexto_solicitado text
);

-- Índice parcial: el barrido de vigencia solo mira los desafíos pendientes.
CREATE INDEX desafios_pendientes ON auth.desafios (expira_en) WHERE estado = 'pendiente';

GRANT SELECT, INSERT ON auth.desafios TO rol_auth;
-- UPDATE acotado a las columnas mutables del desafío; sin DELETE.
GRANT UPDATE (intentos, reenvios, estado, codigo_hmac, expira_en, ultimo_envio_en) ON auth.desafios TO rol_auth;
REVOKE DELETE, TRUNCATE ON auth.desafios FROM rol_auth;

-- +goose Down
DROP TABLE auth.desafios;

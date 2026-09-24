-- Consentimientos del registro y su catalogo versionado de textos (AUT-A1-05.1).

-- +goose Up
CREATE TABLE auth.textos_consentimiento (
    tipo          text NOT NULL CHECK (tipo IN ('tratamiento_datos','whatsapp_optin','biometricos')),
    version       text NOT NULL,
    texto         text NOT NULL,
    vigente_desde timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tipo, version)
);

-- Fila append-only: cada consentimiento copia el texto exacto que la parte acepto.
CREATE TABLE auth.consentimientos (
    id            uuid PRIMARY KEY,
    parte_id      uuid NOT NULL REFERENCES auth.partes(id),
    tipo          text NOT NULL CHECK (tipo IN ('tratamiento_datos','whatsapp_optin','biometricos')),
    version_texto text NOT NULL,
    texto         text NOT NULL,
    aceptado      boolean NOT NULL,
    accion        text NOT NULL,
    actor         uuid,
    origen        text NOT NULL,
    user_agent    text NOT NULL DEFAULT '',
    metodo        text NOT NULL,
    ocurrido_en   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX consentimientos_parte ON auth.consentimientos (parte_id, tipo, ocurrido_en);

-- Catalogo de lectura para auth; el texto real lo fija el humano y se siembra abajo.
GRANT SELECT ON auth.textos_consentimiento TO rol_auth;

GRANT SELECT, INSERT ON auth.consentimientos TO rol_auth;

-- El consentimiento es append-only: ni UPDATE, ni DELETE, ni TRUNCATE para rol_auth.
REVOKE UPDATE, DELETE, TRUNCATE ON auth.consentimientos FROM rol_auth;

-- +goose StatementBegin
CREATE FUNCTION auth.consentimientos_inmutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'auth.consentimientos es append-only: % no permitido', TG_OP
        USING ERRCODE = 'restrict_violation';
END $$;
-- +goose StatementEnd

CREATE TRIGGER consentimientos_inmutable
BEFORE UPDATE OR DELETE ON auth.consentimientos
FOR EACH ROW EXECUTE FUNCTION auth.consentimientos_inmutable();

-- Marcadores explicitos: el humano debe reemplazarlos por los textos legales reales.
INSERT INTO auth.textos_consentimiento (tipo, version, texto) VALUES
    ('tratamiento_datos', 'v1', '[TEXTO MARCADOR PENDIENTE - AUT-A1-05.1] Autorizo el tratamiento de mis datos personales.'),
    ('whatsapp_optin', 'v1', '[TEXTO MARCADOR PENDIENTE - AUT-A1-05.1] Autorizo recibir el codigo de verificacion por WhatsApp.'),
    ('biometricos', 'v1', '[TEXTO MARCADOR PENDIENTE - AUT-A1-05.1] Autorizo el uso de mis datos biometricos.');

-- +goose Down
DROP TRIGGER IF EXISTS consentimientos_inmutable ON auth.consentimientos;
DROP FUNCTION IF EXISTS auth.consentimientos_inmutable();
DROP TABLE auth.consentimientos;
DROP TABLE auth.textos_consentimiento;

-- Sesiones de auth: refresh opaco, familia de rotación y revocación por fila (AUT-A2-01).

-- +goose Up
CREATE TABLE auth.sesiones (
    id              uuid PRIMARY KEY,
    familia_id      uuid NOT NULL,
    parte_id        uuid NOT NULL REFERENCES auth.partes(id),
    contexto_activo text NOT NULL DEFAULT '',
    dispositivo     text NOT NULL DEFAULT '',
    refresh_hash    bytea NOT NULL UNIQUE,
    creada_en       timestamptz NOT NULL DEFAULT now(),
    ultimo_uso_en   timestamptz NOT NULL DEFAULT now(),
    usado_en        timestamptz,
    revocada_en     timestamptz,
    vence_en        timestamptz NOT NULL
);

CREATE INDEX sesiones_familia ON auth.sesiones (familia_id);
CREATE INDEX sesiones_parte_vigente ON auth.sesiones (parte_id, revocada_en);

GRANT SELECT, INSERT ON auth.sesiones TO rol_auth;
-- UPDATE acotado a las columnas mutables de la sesión; sin DELETE ni reescritura de identidad.
GRANT UPDATE (ultimo_uso_en, usado_en, revocada_en, contexto_activo) ON auth.sesiones TO rol_auth;

-- +goose Down
DROP TABLE auth.sesiones;

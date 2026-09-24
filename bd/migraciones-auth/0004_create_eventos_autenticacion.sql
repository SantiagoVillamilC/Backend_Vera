-- Trail de auditoría de autenticación: inmutable por el sistema y sin columna donde quepa un secreto (AUT-A1-04).

-- +goose Up
CREATE TABLE auth.eventos_autenticacion (
    id             bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
    tipo           text NOT NULL,
    actor_parte_id uuid,
    credencial_id  uuid,
    resultado      text NOT NULL,
    ocurrido_en    timestamptz NOT NULL DEFAULT now(),
    origen         text NOT NULL,
    contexto       text NOT NULL DEFAULT '',
    sesion_id      uuid,
    detalle        jsonb NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX eventos_autenticacion_actor ON auth.eventos_autenticacion (actor_parte_id, ocurrido_en);
CREATE INDEX eventos_autenticacion_tipo ON auth.eventos_autenticacion (tipo, ocurrido_en);

GRANT SELECT, INSERT ON auth.eventos_autenticacion TO rol_auth;

-- El trail es append-only: ni UPDATE, ni DELETE, ni TRUNCATE para rol_auth.
REVOKE UPDATE, DELETE, TRUNCATE ON auth.eventos_autenticacion FROM rol_auth;

-- +goose StatementBegin
CREATE FUNCTION auth.eventos_autenticacion_inmutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'auth.eventos_autenticacion es append-only: % no permitido', TG_OP
        USING ERRCODE = 'restrict_violation';
END $$;
-- +goose StatementEnd

CREATE TRIGGER eventos_autenticacion_inmutable
BEFORE UPDATE OR DELETE ON auth.eventos_autenticacion
FOR EACH ROW EXECUTE FUNCTION auth.eventos_autenticacion_inmutable();

-- +goose Down
DROP TRIGGER IF EXISTS eventos_autenticacion_inmutable ON auth.eventos_autenticacion;
DROP FUNCTION IF EXISTS auth.eventos_autenticacion_inmutable();
DROP TABLE auth.eventos_autenticacion;

-- Fase 0 de auth: esquema auth y rol_auth NOLOGIN, con su tabla de versión propia.

-- +goose Up
CREATE SCHEMA auth;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'rol_auth') THEN
        CREATE ROLE rol_auth NOLOGIN;
    END IF;
END $$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA auth TO rol_auth;
GRANT rol_auth TO rol_api WITH INHERIT FALSE, SET TRUE;

-- +goose Down
REVOKE rol_auth FROM rol_api;
REVOKE USAGE ON SCHEMA auth FROM rol_auth;
DROP SCHEMA auth;
DROP ROLE IF EXISTS rol_auth;

-- Contexto de inteligencia: esquema propio y rol propio.
--
-- El rol de inteligencia lee el ledger y no lo escribe. La copia enriquecida vive en su esquema y
-- se puede borrar y reconstruir sin tocar nucleo. La membresía va con INHERIT FALSE y SET TRUE por
-- el mismo motivo que en nucleo: el único camino al privilegio es el SET LOCAL ROLE del helper.

-- +goose Up
CREATE SCHEMA inteligencia;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'rol_inteligencia') THEN
        CREATE ROLE rol_inteligencia NOLOGIN;
    END IF;
END $$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA inteligencia TO rol_inteligencia;
GRANT rol_inteligencia TO rol_api WITH INHERIT FALSE, SET TRUE;

GRANT USAGE ON SCHEMA nucleo TO rol_inteligencia;
GRANT SELECT ON nucleo.cuenta, nucleo.journal_entry, nucleo.posting TO rol_inteligencia;
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON nucleo.cuenta, nucleo.journal_entry, nucleo.posting FROM rol_inteligencia;

-- +goose Down
REVOKE SELECT ON nucleo.cuenta, nucleo.journal_entry, nucleo.posting FROM rol_inteligencia;
REVOKE USAGE ON SCHEMA nucleo FROM rol_inteligencia;
REVOKE rol_inteligencia FROM rol_api;
REVOKE USAGE ON SCHEMA inteligencia FROM rol_inteligencia;
DROP SCHEMA inteligencia;
-- Las migraciones de auth posteriores a 0021 concedieron nombres al rol y siguen aplicadas cuando se
-- derriba inteligencia, así que el rol conserva privilegios que este Down no otorgó. DROP OWNED los
-- retira todos antes de borrar el rol; sin él, DROP ROLE falla con 2BP01 y el rollback se queda a
-- medias.
DROP OWNED BY rol_inteligencia;
DROP ROLE IF EXISTS rol_inteligencia;

-- Fase 0 del núcleo financiero: el esquema nucleo y los dos roles con los que se trabaja.
--
-- Hay dos roles, a propósito:
--
--   * rol_nucleo (NOLOGIN) es el rol del ledger. La aplicación no conecta con él: cada transacción
--     del núcleo baja a este rol con SET LOCAL ROLE (internal/nucleo/transaccion.go), y eso es lo
--     que hace real el REVOKE de NF-A1-01 en vez de una convención de código.
--   * rol_api (LOGIN) es el rol base del pool de cmd/api. No es dueño de nada ni es superusuario,
--     y es miembro de rol_nucleo para poder bajar a él.
--
-- La membresía se otorga con INHERIT FALSE a propósito: con la herencia por defecto, rol_api recibe
-- los privilegios de rol_nucleo sin ejecutar SET ROLE, y una consulta calificada (nucleo.tabla)
-- directa sobre el pool correría con los privilegios del ledger. Con INHERIT FALSE, el único camino
-- de privilegio es el SET LOCAL ROLE del helper, que es el cuello de botella que la unidad promete.
--
-- El rol que corre goose es otro, el dueño del esquema: por eso la aplicación no puede reotorgarse
-- los permisos que el diseño le va a revocar. El pool de la API conecta con APP_DATABASE_URL, que
-- lleva rol_api, mientras cmd/migrate sigue con DATABASE_URL, que lleva ese rol dueño.
--
-- Acá solo se otorga el USAGE del esquema y los privilegios mínimos de la aplicación sobre
-- public.users (el pool es compartido con el módulo de auth). Los GRANT SELECT, INSERT por tabla
-- del núcleo llegan con cada tabla (NF-A1-01 en adelante), junto con el REVOKE de UPDATE, DELETE y
-- TRUNCATE.
--
-- Los roles son objetos del clúster, no de la base: por eso cada CREATE ROLE corre tras consultar
-- pg_roles por rolname. Aplicar esta migración sobre una segunda base del mismo Postgres debe
-- reutilizar los roles existentes en vez de fallar con "role ... already exists".

-- +goose Up
CREATE SCHEMA nucleo;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'rol_nucleo') THEN
        CREATE ROLE rol_nucleo NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'rol_api') THEN
        CREATE ROLE rol_api LOGIN;
    END IF;
END $$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA nucleo TO rol_nucleo;
GRANT rol_nucleo TO rol_api WITH INHERIT FALSE, SET TRUE;
GRANT SELECT, INSERT ON public.users TO rol_api;

-- +goose Down
REVOKE SELECT, INSERT ON public.users FROM rol_api;
REVOKE rol_nucleo FROM rol_api;
REVOKE USAGE ON SCHEMA nucleo FROM rol_nucleo;
DROP SCHEMA nucleo;
DROP ROLE IF EXISTS rol_api;
DROP ROLE IF EXISTS rol_nucleo;

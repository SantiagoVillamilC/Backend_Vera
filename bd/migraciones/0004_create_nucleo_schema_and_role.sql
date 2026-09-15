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
-- El rol que corre goose es otro, el dueño del esquema: por eso la aplicación no puede reotorgarse
-- los permisos que el diseño le va a revocar. El pool de la API conecta con APP_DATABASE_URL, que
-- lleva rol_api, mientras cmd/migrate sigue con DATABASE_URL, que lleva ese rol dueño.
--
-- Acá solo se otorga el USAGE del esquema y los privilegios mínimos de la aplicación sobre
-- public.users (el pool es compartido con el módulo de auth). Los GRANT SELECT, INSERT por tabla
-- del núcleo llegan con cada tabla (NF-A1-01 en adelante), junto con el REVOKE de UPDATE, DELETE y
-- TRUNCATE.

-- +goose Up
CREATE SCHEMA nucleo;
CREATE ROLE rol_nucleo NOLOGIN;
CREATE ROLE rol_api LOGIN;
GRANT USAGE ON SCHEMA nucleo TO rol_nucleo;
GRANT rol_nucleo TO rol_api;
GRANT SELECT, INSERT ON public.users TO rol_api;

-- +goose Down
REVOKE SELECT, INSERT ON public.users FROM rol_api;
REVOKE rol_nucleo FROM rol_api;
REVOKE USAGE ON SCHEMA nucleo FROM rol_nucleo;
DROP SCHEMA nucleo;
DROP ROLE rol_api;
DROP ROLE rol_nucleo;

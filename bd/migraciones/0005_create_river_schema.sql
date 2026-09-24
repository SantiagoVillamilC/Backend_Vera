-- rivermigrate asume que el schema "river" ya existe (Config{Schema: "river"}), ningún
-- goose migration lo creaba: "migrate up" cortaba con "schema river does not exist"
-- (SQLSTATE 3F000), verificado corriendo la migracion contra una base nueva (SCRUM-100).
-- USAGE a rol_api acá: concesionesRiver (river.go) otorga GRANT por tabla, pero sin
-- USAGE ON SCHEMA ninguno de esos GRANT sirve.

-- +goose Up
CREATE SCHEMA river;
GRANT USAGE ON SCHEMA river TO rol_api;

-- +goose Down
REVOKE USAGE ON SCHEMA river FROM rol_api;
DROP SCHEMA river;

-- Fase 0 del núcleo financiero: el esquema nucleo y el rol con el que la aplicación lo consulta.
--
-- El rol que corre goose crea este esquema y queda como su dueño, distinto de rol_nucleo. Es la
-- separación que el capítulo 5 del ledger exige: el rol de la aplicación no puede ser dueño de
-- los objetos (si lo fuera, el REVOKE de NF-A1-01 sería decorativo, porque el dueño se reotorga
-- los permisos que quiera).
--
-- rol_nucleo no hace login: el único pool de la aplicación conecta con el rol base que viaja en
-- DATABASE_URL y baja a rol_nucleo con SET LOCAL ROLE dentro de cada transacción
-- (internal/nucleo/transaccion.go). El GRANT de membresía es lo que hace posible ese SET ROLE
-- cuando el rol base no es superusuario; se otorga a CURRENT_USER, el rol que corre goose, que
-- en esta fase es el mismo del pool.
--
-- Acá solo se otorga el USAGE del esquema. Los GRANT SELECT, INSERT por tabla llegan con cada
-- tabla (NF-A1-01 en adelante), junto con el REVOKE de UPDATE, DELETE y TRUNCATE.

-- +goose Up
CREATE SCHEMA nucleo;
CREATE ROLE rol_nucleo NOLOGIN;
GRANT USAGE ON SCHEMA nucleo TO rol_nucleo;
GRANT rol_nucleo TO CURRENT_USER;

-- +goose Down
REVOKE rol_nucleo FROM CURRENT_USER;
REVOKE USAGE ON SCHEMA nucleo FROM rol_nucleo;
DROP SCHEMA nucleo;
DROP ROLE rol_nucleo;

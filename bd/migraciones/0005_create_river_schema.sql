-- Fase 0 del núcleo financiero: el esquema propio de las tablas de River, el planificador de
-- trabajos que dispara DespacharOutbox y Conciliar.
--
-- Va en un esquema aparte (`river`), no en `nucleo` ni en `public`: las tablas de River son
-- infraestructura del disparador, no el ledger, y `nucleo` queda reservado para las tablas que la
-- frontera del módulo protege. cmd/migrate crea el esquema acá y después corre las migraciones de
-- River con su propio migrador.
--
-- Los privilegios sobre las tablas y secuencias de River no se conceden acá: las tablas todavía no
-- existen cuando corre esta migración. cmd/migrate los concede a rol_api apenas termina de aplicar
-- las migraciones de River.

-- +goose Up
CREATE SCHEMA river;
GRANT USAGE ON SCHEMA river TO rol_api;

-- +goose Down
DROP SCHEMA river;

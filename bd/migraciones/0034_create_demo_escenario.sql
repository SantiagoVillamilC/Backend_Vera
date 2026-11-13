-- Manifiesto del escenario de demo que escribe cmd/semilla-escenario.
--
-- Una fila por usuario de demo conserva la fecha simulada, los asientos y las transferencias de la
-- siembra para que una segunda corrida reutilice exactamente lo mismo. Antes la tabla se creaba con
-- CREATE TABLE IF NOT EXISTS dentro de la transacción de siembra: el esquema quedaba fuera de las
-- migraciones y, con una tabla de otra forma ya presente, el IF NOT EXISTS seguía de largo y la
-- consulta siguiente fallaba sin decir por qué.
--
-- Las bases donde el comando ya corrió tienen la tabla con esta misma forma, así que la migración
-- la adopta. Si la forma es otra, la migración se detiene con un mensaje que nombra la tabla en vez
-- de dejar el SELECT del comando fallando más adelante. La tabla vive en public porque es del
-- escenario local y no la escriben ni rol_inteligencia ni rol_api.

-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE
    reconocidas int;
    totales     int;
BEGIN
    IF to_regclass('public.demo_escenario') IS NULL THEN
        CREATE TABLE public.demo_escenario (
            usuario_id     uuid PRIMARY KEY,
            fecha          timestamptz NOT NULL,
            asientos       bigint[] NOT NULL,
            transferencias uuid[] NOT NULL
        );
        RETURN;
    END IF;
    SELECT count(*) FILTER (
               WHERE (column_name = 'usuario_id' AND udt_name = 'uuid')
                  OR (column_name = 'fecha' AND udt_name = 'timestamptz')
                  OR (column_name = 'asientos' AND udt_name = '_int8')
                  OR (column_name = 'transferencias' AND udt_name = '_uuid')
           ),
           count(*)
      INTO reconocidas, totales
      FROM information_schema.columns
     WHERE table_schema = 'public' AND table_name = 'demo_escenario';
    IF reconocidas <> 4 OR totales <> 4 THEN
        RAISE EXCEPTION 'public.demo_escenario existe con otra forma: revisa la tabla antes de migrar';
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE public.demo_escenario;

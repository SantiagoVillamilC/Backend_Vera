-- Destino persistido de cada fila de la copia enriquecida.
--
-- Sin estas columnas el destino de una correccion solo se puede rederivar releyendo el ledger, y
-- no hay indice que sostenga la busqueda de todas las filas de un mismo destino. Son columnas
-- derivadas: la copia se puede borrar y reconstruir. Las filas escritas antes de esta migracion
-- quedan con las tres en nulo y se llenan al reconstruir la copia.
--
-- contraparte_llave guarda la llave del destino, que para una persona es el identificador de la
-- otra parte y no el de una cuenta. Ninguna consulta de lectura la selecciona.

-- +goose Up
ALTER TABLE inteligencia.movimiento_enriquecido
    ADD COLUMN tipo_destino text,
    ADD COLUMN contraparte_llave text,
    ADD COLUMN contraparte_nombre_normalizado text;

ALTER TABLE inteligencia.movimiento_enriquecido
    ADD CONSTRAINT movimiento_enriquecido_tipo_destino_valido
        CHECK (tipo_destino IS NULL OR tipo_destino IN ('comercio', 'persona')),
    ADD CONSTRAINT movimiento_enriquecido_destino_completo
        CHECK (tipo_destino IS NOT NULL
               OR (contraparte_llave IS NULL
                   AND comercio_mid IS NULL
                   AND contraparte_nombre_normalizado IS NULL)),
    ADD CONSTRAINT movimiento_enriquecido_llave_no_vacia
        CHECK (contraparte_llave IS NULL OR btrim(contraparte_llave) <> ''),
    ADD CONSTRAINT movimiento_enriquecido_nombre_normalizado_valido
        CHECK (contraparte_nombre_normalizado IS NULL
               OR (btrim(contraparte_nombre_normalizado) <> ''
                   AND contraparte_nombre_normalizado = btrim(contraparte_nombre_normalizado))),
    ADD CONSTRAINT movimiento_enriquecido_nombre_solo_de_comercio
        CHECK (contraparte_nombre_normalizado IS NULL OR tipo_destino = 'comercio');

CREATE INDEX movimiento_enriquecido_por_destino_llave
    ON inteligencia.movimiento_enriquecido (usuario_id, tipo_destino, contraparte_llave)
    WHERE contraparte_llave IS NOT NULL;
CREATE INDEX movimiento_enriquecido_por_destino_mid
    ON inteligencia.movimiento_enriquecido (usuario_id, tipo_destino, comercio_mid)
    WHERE comercio_mid IS NOT NULL;
CREATE INDEX movimiento_enriquecido_por_destino_nombre
    ON inteligencia.movimiento_enriquecido (usuario_id, tipo_destino, contraparte_nombre_normalizado)
    WHERE contraparte_nombre_normalizado IS NOT NULL;

-- El privilegio de tabla otorgado en 0023 alcanza a las columnas nuevas. No se crea tabla y
-- rol_inteligencia sigue sin ALTER, sin REFERENCES y sin TRUNCATE.

-- +goose Down
DROP INDEX IF EXISTS inteligencia.movimiento_enriquecido_por_destino_nombre;
DROP INDEX IF EXISTS inteligencia.movimiento_enriquecido_por_destino_mid;
DROP INDEX IF EXISTS inteligencia.movimiento_enriquecido_por_destino_llave;

ALTER TABLE inteligencia.movimiento_enriquecido
    DROP CONSTRAINT movimiento_enriquecido_nombre_solo_de_comercio,
    DROP CONSTRAINT movimiento_enriquecido_nombre_normalizado_valido,
    DROP CONSTRAINT movimiento_enriquecido_llave_no_vacia,
    DROP CONSTRAINT movimiento_enriquecido_destino_completo,
    DROP CONSTRAINT movimiento_enriquecido_tipo_destino_valido,
    DROP COLUMN contraparte_nombre_normalizado,
    DROP COLUMN contraparte_llave,
    DROP COLUMN tipo_destino;

-- Escalón de la cascada que decidió cada fila de la copia enriquecida.
--
-- El origen no alcanza para reconstruir la decisión: cuando el clasificador se abstiene la fila se
-- guarda con sin_categoria aunque el escalón que la miró fue el 6. Persistir el escalón deja que la
-- app muestre qué peldaño decidió sin volver a correr la cascada. Es una columna derivada: la copia
-- se puede borrar y reconstruir.
--
-- Los orígenes llm y manual salen del contrato: la caché del LLM nunca se llenó y manual no tenía
-- escritor. Las filas que quedaran con ellos se reescriben al escalón que les corresponde antes de
-- apretar el CHECK, para que la migración no dependa de que la base esté vacía.

-- +goose Up
UPDATE inteligencia.movimiento_enriquecido
   SET origen_categoria = CASE origen_categoria
                              WHEN 'llm' THEN 'tfidf'
                              WHEN 'manual' THEN 'memoria'
                              ELSE origen_categoria
                          END
 WHERE origen_categoria IN ('llm', 'manual');

ALTER TABLE inteligencia.movimiento_enriquecido
    ADD COLUMN escalon smallint;

UPDATE inteligencia.movimiento_enriquecido
   SET escalon = CASE origen_categoria
                     WHEN 'memoria' THEN 1
                     WHEN 'contraparte_interna' THEN 2
                     WHEN 'mcc' THEN 3
                     WHEN 'catalogo' THEN 4
                     WHEN 'lexico' THEN 5
                     WHEN 'tfidf' THEN 6
                     ELSE 7
                 END;

ALTER TABLE inteligencia.movimiento_enriquecido
    ALTER COLUMN escalon SET NOT NULL,
    ADD CONSTRAINT movimiento_enriquecido_escalon_valido CHECK (escalon BETWEEN 1 AND 7);

ALTER TABLE inteligencia.movimiento_enriquecido
    DROP CONSTRAINT movimiento_enriquecido_origen_categoria_check,
    ADD CONSTRAINT movimiento_enriquecido_origen_categoria_check CHECK (origen_categoria IN (
            'memoria', 'contraparte_interna', 'mcc', 'catalogo',
            'lexico', 'tfidf', 'sin_categoria'));

-- El privilegio de tabla otorgado en 0023 alcanza a la columna nueva. No se crea tabla y
-- rol_inteligencia sigue sin ALTER, sin REFERENCES y sin TRUNCATE.

-- +goose Down
ALTER TABLE inteligencia.movimiento_enriquecido
    DROP CONSTRAINT movimiento_enriquecido_origen_categoria_check,
    ADD CONSTRAINT movimiento_enriquecido_origen_categoria_check CHECK (origen_categoria IN (
            'memoria', 'contraparte_interna', 'mcc', 'catalogo',
            'lexico', 'tfidf', 'llm', 'sin_categoria', 'manual')),
    DROP CONSTRAINT movimiento_enriquecido_escalon_valido,
    DROP COLUMN escalon;

-- Catálogo curado de marcas del escalón 4, derivado del v3 de la herramienta de evaluación.
--
-- La llave primaria es el nombre ya normalizado, tal como lo busca la cascada: así el cargador
-- idempotente y la búsqueda comparten la misma forma y ninguna de las dos depende de comparar
-- texto en tiempo de consulta. La llave Bre-B es opcional porque el archivo curado no la trae.
-- El origen dice de dónde salió cada fila y es lo que permite borrar lo que ya no está en el
-- archivo sin tocar las filas de otra procedencia. La versión es solo la etiqueta del archivo que
-- la escribió: una fila retirada conserva la versión vieja, así que el borrado no puede filtrarla.

-- +goose Up
CREATE TABLE inteligencia.catalogo_marca (
    nombre_normalizado text PRIMARY KEY CHECK (btrim(nombre_normalizado) <> ''),
    categoria          text NOT NULL REFERENCES inteligencia.categoria (codigo),
    llave              text UNIQUE CHECK (llave IS NULL OR btrim(llave) <> ''),
    origen             text NOT NULL CHECK (btrim(origen) <> ''),
    version            text NOT NULL CHECK (btrim(version) <> ''),
    creada_en          timestamptz NOT NULL DEFAULT now()
);

-- La llave ya trae su índice por la restricción UNIQUE; el nombre lo cubre la primaria.
CREATE INDEX catalogo_marca_por_categoria ON inteligencia.catalogo_marca (categoria);

GRANT SELECT, INSERT, UPDATE, DELETE ON inteligencia.catalogo_marca TO rol_inteligencia;
REVOKE TRUNCATE ON inteligencia.catalogo_marca FROM rol_inteligencia;

-- +goose Down
DROP TABLE inteligencia.catalogo_marca;

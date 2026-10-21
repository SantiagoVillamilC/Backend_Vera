-- Copia de lectura del ledger y memoria privada de correcciones.
--
-- La copia se puede borrar y reconstruir: no es la verdad contable y no escribe en nucleo. El monto
-- se guarda en centavos y con signo, con la dirección aparte. La categoría tiene clave foránea al
-- catálogo cerrado, así que una categoría inventada no entra. La memoria tiene clave tipada por
-- usuario, tipo de destino, nivel y valor, y una fila puede traer categoría, nombre o los dos.

-- +goose Up
CREATE TABLE inteligencia.movimiento_enriquecido (
    usuario_id           text NOT NULL CHECK (btrim(usuario_id) <> ''),
    transaccion_id       uuid NOT NULL,
    entry_ancla          bigint NOT NULL,
    ocurrido_en          timestamptz NOT NULL,
    moneda               char(3) NOT NULL DEFAULT 'COP',
    direccion            text NOT NULL CHECK (direccion IN ('entrada', 'salida')),
    monto_centavos       bigint NOT NULL CHECK (monto_centavos <> 0),
    contraparte          text NOT NULL CHECK (btrim(contraparte) <> ''),
    contraparte_resuelta boolean NOT NULL,
    descripcion          text,
    categoria            text NOT NULL REFERENCES inteligencia.categoria (codigo),
    origen_categoria     text NOT NULL CHECK (origen_categoria IN (
                             'memoria', 'contraparte_interna', 'mcc', 'catalogo',
                             'lexico', 'tfidf', 'llm', 'sin_categoria', 'manual')),
    comercio_mid         text,
    comercio_nombre      text,
    mcc                  text,
    version_modelo       text NOT NULL CHECK (btrim(version_modelo) <> ''),
    calculado_en         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (usuario_id, transaccion_id)
);

CREATE INDEX movimiento_enriquecido_por_usuario
    ON inteligencia.movimiento_enriquecido (usuario_id, ocurrido_en DESC, transaccion_id DESC);
CREATE INDEX movimiento_enriquecido_por_categoria
    ON inteligencia.movimiento_enriquecido (usuario_id, categoria, ocurrido_en);
CREATE INDEX movimiento_enriquecido_por_asiento
    ON inteligencia.movimiento_enriquecido (entry_ancla);

CREATE TABLE inteligencia.memoria_correccion (
    usuario_id     text NOT NULL CHECK (btrim(usuario_id) <> ''),
    tipo_destino   text NOT NULL CHECK (tipo_destino IN ('comercio', 'persona')),
    nivel          text NOT NULL CHECK (nivel IN ('llave', 'mid', 'nombre')),
    valor          text NOT NULL CHECK (btrim(valor) <> ''),
    categoria      text REFERENCES inteligencia.categoria (codigo),
    nombre         text,
    actualizado_en timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (usuario_id, tipo_destino, nivel, valor),
    CHECK (categoria IS NOT NULL OR (nombre IS NOT NULL AND btrim(nombre) <> ''))
);

GRANT SELECT, INSERT, UPDATE, DELETE ON inteligencia.movimiento_enriquecido TO rol_inteligencia;
GRANT SELECT, INSERT, UPDATE, DELETE ON inteligencia.memoria_correccion TO rol_inteligencia;
REVOKE TRUNCATE ON inteligencia.movimiento_enriquecido, inteligencia.memoria_correccion FROM rol_inteligencia;

-- +goose Down
DROP TABLE inteligencia.memoria_correccion;
DROP TABLE inteligencia.movimiento_enriquecido;

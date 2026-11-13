-- Votos por destino y categoría, y promoción a conocimiento compartido (R-08).
--
-- El voto es por destino (tipado: comercio o persona; nivel llave, mid o nombre normalizado) y por
-- usuario. La clave única hace que cambiar de opinión reemplace el voto en vez de sumar otro. La
-- promoción guarda la categoría que alcanzó K=2 usuarios distintos; no expone quién votó y no
-- recalcula filas ya enriquecidas.

-- +goose Up
CREATE TABLE inteligencia.voto_destino (
    tipo_destino text NOT NULL CHECK (tipo_destino IN ('comercio', 'persona')),
    nivel        text NOT NULL CHECK (nivel IN ('llave', 'mid', 'nombre')),
    valor        text NOT NULL CHECK (btrim(valor) <> ''),
    usuario_id   text NOT NULL CHECK (btrim(usuario_id) <> ''),
    categoria    text NOT NULL REFERENCES inteligencia.categoria (codigo),
    creado_en    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tipo_destino, nivel, valor, usuario_id)
);

CREATE TABLE inteligencia.promocion_destino (
    tipo_destino text NOT NULL CHECK (tipo_destino IN ('comercio', 'persona')),
    nivel        text NOT NULL CHECK (nivel IN ('llave', 'mid', 'nombre')),
    valor        text NOT NULL CHECK (btrim(valor) <> ''),
    categoria    text NOT NULL REFERENCES inteligencia.categoria (codigo),
    promovido_en timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tipo_destino, nivel, valor)
);

GRANT SELECT, INSERT, UPDATE, DELETE ON inteligencia.voto_destino TO rol_inteligencia;
GRANT SELECT, INSERT, UPDATE, DELETE ON inteligencia.promocion_destino TO rol_inteligencia;
REVOKE TRUNCATE ON inteligencia.voto_destino, inteligencia.promocion_destino FROM rol_inteligencia;

-- +goose Down
DROP TABLE inteligencia.promocion_destino;
DROP TABLE inteligencia.voto_destino;

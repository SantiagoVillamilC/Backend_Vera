-- Avisos, su entrega y el cursor del lote de enriquecimiento.
--
-- El aviso se deduplica por usuario, tipo, clave y período. La entrega va aparte para que la
-- decisión no dependa del canal. El cursor es una fila única y avanza en la misma transacción que
-- el lote.

-- +goose Up
CREATE TABLE inteligencia.aviso (
    id                  uuid PRIMARY KEY,
    usuario_id          text NOT NULL CHECK (btrim(usuario_id) <> ''),
    tipo                text NOT NULL CHECK (btrim(tipo) <> ''),
    clave               text NOT NULL CHECK (btrim(clave) <> ''),
    periodo             date NOT NULL,
    severidad_centavos  bigint NOT NULL CHECK (severidad_centavos >= 0),
    prioridad           smallint NOT NULL CHECK (prioridad > 0),
    texto               text NOT NULL CHECK (btrim(texto) <> ''),
    texto_verificado    boolean NOT NULL DEFAULT false,
    transaccion_id      uuid,
    creado_en           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT aviso_dedupe_unico UNIQUE (usuario_id, tipo, clave, periodo)
);

CREATE INDEX aviso_por_usuario
    ON inteligencia.aviso (usuario_id, periodo DESC, prioridad, severidad_centavos DESC);

CREATE TABLE inteligencia.aviso_entrega (
    aviso_id   uuid NOT NULL REFERENCES inteligencia.aviso (id) ON DELETE CASCADE,
    canal      text NOT NULL CHECK (btrim(canal) <> ''),
    estado     text NOT NULL CHECK (estado IN ('pendiente', 'enviado', 'fallido', 'descartado')),
    enviado_en timestamptz,
    PRIMARY KEY (aviso_id, canal)
);

CREATE TABLE inteligencia.cursor_enriquecimiento (
    id              smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    ultimo_entry_id bigint NOT NULL DEFAULT 0 CHECK (ultimo_entry_id >= 0),
    actualizado_en  timestamptz NOT NULL DEFAULT now()
);

INSERT INTO inteligencia.cursor_enriquecimiento (id, ultimo_entry_id) VALUES (1, 0);

GRANT SELECT, INSERT, UPDATE, DELETE ON inteligencia.aviso TO rol_inteligencia;
GRANT SELECT, INSERT, UPDATE, DELETE ON inteligencia.aviso_entrega TO rol_inteligencia;
GRANT SELECT, UPDATE ON inteligencia.cursor_enriquecimiento TO rol_inteligencia;
REVOKE TRUNCATE ON inteligencia.aviso, inteligencia.aviso_entrega, inteligencia.cursor_enriquecimiento
    FROM rol_inteligencia;

-- +goose Down
DROP TABLE inteligencia.cursor_enriquecimiento;
DROP TABLE inteligencia.aviso_entrega;
DROP TABLE inteligencia.aviso;

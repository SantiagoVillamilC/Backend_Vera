-- Descarte de un aviso.
--
-- La tabla aviso no tenía estado y aviso_entrega es por canal, así que marcar el descarte como una
-- entrega habría hecho del descarte un resultado de entrega y habría permitido estados distintos por
-- canal para un mismo aviso. El descarte es del aviso y se marca aquí.

-- +goose Up
ALTER TABLE inteligencia.aviso
    ADD COLUMN descartado_en timestamptz;

ALTER TABLE inteligencia.aviso
    ADD CONSTRAINT aviso_descarte_no_anterior_al_alta
        CHECK (descartado_en IS NULL OR descartado_en >= creado_en);

CREATE INDEX aviso_vigente_por_usuario
    ON inteligencia.aviso (usuario_id, creado_en DESC)
    WHERE descartado_en IS NULL;

-- El privilegio de tabla de 0024 alcanza a la columna nueva. Se repite la revocación de TRUNCATE
-- para dejar explícito que el rol no puede vaciar la tabla.
REVOKE TRUNCATE ON inteligencia.aviso FROM rol_inteligencia;

-- +goose Down
DROP INDEX IF EXISTS inteligencia.aviso_vigente_por_usuario;
ALTER TABLE inteligencia.aviso
    DROP CONSTRAINT IF EXISTS aviso_descarte_no_anterior_al_alta;
ALTER TABLE inteligencia.aviso
    DROP COLUMN IF EXISTS descartado_en;

-- +goose Up
CREATE INDEX aviso_vigente_por_usuario_periodo
    ON inteligencia.aviso (usuario_id, periodo, prioridad, severidad_centavos DESC, clave, id)
    WHERE descartado_en IS NULL;

CREATE INDEX aviso_descartado_por_usuario_periodo
    ON inteligencia.aviso (usuario_id, periodo, descartado_en DESC)
    WHERE descartado_en IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS inteligencia.aviso_descartado_por_usuario_periodo;
DROP INDEX IF EXISTS inteligencia.aviso_vigente_por_usuario_periodo;

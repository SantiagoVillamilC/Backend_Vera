-- regla_limite del capítulo 11: tipo separa la regla que rechaza del gravamen y tarifa_ppm guarda el 4x1000 (HU-NF-06, criterio 13).

-- +goose Up
ALTER TABLE nucleo.regla_limite
    ADD COLUMN tipo text NOT NULL DEFAULT 'rechaza',
    ADD COLUMN tarifa_ppm bigint NOT NULL DEFAULT 0,
    ADD CONSTRAINT regla_limite_tipo_valido CHECK (tipo IN ('rechaza', 'grava')),
    ADD CONSTRAINT regla_limite_tarifa_coherente CHECK (
        (tipo = 'rechaza' AND tarifa_ppm = 0) OR (tipo = 'grava' AND tarifa_ppm > 0)
    );

-- Tope exento de 65 UVT mensuales y tarifa del 4x1000 (4000 ppm), según el Concepto DIAN 1466 de 2017.
INSERT INTO nucleo.regla_limite (id, nivel_kyc, ventana, tope_uvt_centesimas, conteo_maximo, vigente_desde, fuente, tipo, tarifa_ppm) VALUES
    ('deposito_bajo_monto_gmf', 'deposito_bajo_monto', 'mensual', 6500, NULL, DATE '2026-01-01',
     'Artículos 872 y 879 (numerales 25, 27 y 28 y parágrafo 4) del Estatuto Tributario, Concepto DIAN 1466 de 2017',
     'grava', 4000);

CREATE INDEX transferencia_debito_del_mes
    ON nucleo.transferencia (usuario_id, creada_en) INCLUDE (monto, estado);

-- +goose Down
DROP INDEX nucleo.transferencia_debito_del_mes;
DELETE FROM nucleo.regla_limite WHERE id = 'deposito_bajo_monto_gmf';
ALTER TABLE nucleo.regla_limite
    DROP CONSTRAINT regla_limite_tarifa_coherente,
    DROP CONSTRAINT regla_limite_tipo_valido,
    DROP COLUMN tarifa_ppm,
    DROP COLUMN tipo;

-- +goose Up
INSERT INTO nucleo.cuenta (id, naturaleza, clase, titular_id) VALUES
    ('activo:custodia:banco', 'activo', 'custodia', NULL),
    ('pasivo:liquidacion_pendiente:riel', 'pasivo', 'liquidacion_pendiente', NULL),
    ('pasivo:gmf_por_pagar', 'pasivo', 'gmf_por_pagar', NULL)
ON CONFLICT (id) DO UPDATE SET naturaleza = EXCLUDED.naturaleza, clase = EXCLUDED.clase; -- idempotente: reejecutar converge a la definición del diseño

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE
    referenciadas bigint;
BEGIN
    SELECT count(*) INTO referenciadas
      FROM nucleo.posting
     WHERE cuenta_id IN (
        'activo:custodia:banco',
        'pasivo:liquidacion_pendiente:riel',
        'pasivo:gmf_por_pagar'
     );

    IF referenciadas > 0 THEN
        RAISE EXCEPTION 'no se puede revertir la siembra 0009: % lineas de posting referencian las cuentas internas', referenciadas;
    END IF;

    DELETE FROM nucleo.cuenta WHERE id IN (
        'activo:custodia:banco',
        'pasivo:liquidacion_pendiente:riel',
        'pasivo:gmf_por_pagar'
    );
END;
$$;
-- +goose StatementEnd

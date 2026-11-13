-- CF-A3-04: la auditoría del asistente admite la lectura de consumo por categoría.
-- +goose Up
ALTER TABLE asistente.llamada_funcion DROP CONSTRAINT llamada_funcion_funcion_check;
ALTER TABLE asistente.llamada_funcion ADD CONSTRAINT llamada_funcion_funcion_check
    CHECK (funcion IN ('consultar_saldo','consultar_limites','listar_movimientos',
        'resumir_movimientos','buscar_destinatario','consultar_transferencia',
        'preparar_transferencia','resumir_consumo_por_categoria','desconocida'));

-- +goose Down
DELETE FROM asistente.llamada_funcion WHERE funcion = 'resumir_consumo_por_categoria';
ALTER TABLE asistente.llamada_funcion DROP CONSTRAINT llamada_funcion_funcion_check;
ALTER TABLE asistente.llamada_funcion ADD CONSTRAINT llamada_funcion_funcion_check
    CHECK (funcion IN ('consultar_saldo','consultar_limites','listar_movimientos',
        'resumir_movimientos','buscar_destinatario','consultar_transferencia',
        'preparar_transferencia','desconocida'));

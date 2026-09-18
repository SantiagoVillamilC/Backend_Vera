-- +goose Up

-- El EXISTS de ObtenerTransferenciaConDespacho filtra por la transferencia del payload (NF-A4-02.2).
CREATE INDEX outbox_despacho_por_transferencia
    ON nucleo.outbox ((payload->>'transferencia_id'))
 WHERE tipo = 'despacho_transferencia' AND despachado_en IS NULL;

-- UltimaVentanaConciliada recorre el cursor por ventana_hasta descendente (NF-A4-01, monotonia).
CREATE INDEX conciliacion_corrida_por_ventana
    ON nucleo.conciliacion_corrida (ventana_hasta DESC, id DESC);

-- +goose Down
DROP INDEX nucleo.conciliacion_corrida_por_ventana;
DROP INDEX nucleo.outbox_despacho_por_transferencia;

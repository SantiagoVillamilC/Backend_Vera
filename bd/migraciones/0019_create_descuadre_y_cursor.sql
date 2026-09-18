-- +goose Up

-- Salida de A4 (capítulo 13): una fila por diferencia entre el diario y el extracto.
CREATE TABLE nucleo.descuadre (
    id                 bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
    tipo               text        NOT NULL,
    transferencia_id   uuid,
    referencia_externa text,
    monto_interno      bigint,
    monto_externo      bigint,
    ventana_desde      timestamptz NOT NULL,
    ventana_hasta      timestamptz NOT NULL,
    detectado_en       timestamptz NOT NULL DEFAULT now(),
    resuelto_en        timestamptz,
    resolucion         text,
    CONSTRAINT descuadre_identidad_check
        CHECK (num_nonnulls(transferencia_id, referencia_externa) = 1)
);

-- Respaldo de deduplicación del capítulo 13: una diferencia sin resolver por identidad.
CREATE UNIQUE INDEX descuadre_identidad_unico
    ON nucleo.descuadre (tipo, COALESCE(transferencia_id::text, referencia_externa))
 WHERE resuelto_en IS NULL;

-- Cursor de ventana (HU-NF-08, criterio 2.2): el final de la ventana de cada corrida.
CREATE TABLE nucleo.conciliacion_corrida (
    id            bigint PRIMARY KEY GENERATED ALWAYS AS IDENTITY,
    ventana_hasta timestamptz NOT NULL,
    corrida_en    timestamptz NOT NULL DEFAULT now()
);

-- El índice de posting(journal_entry_id) ya lo satisface posting_por_asiento de 0007.
CREATE INDEX journal_entry_ocurrido_en ON nucleo.journal_entry (ocurrido_en);
CREATE INDEX journal_entry_transferencia_id ON nucleo.journal_entry (transferencia_id);

-- Estado activo del Apéndice C: despachadas e indeterminadas, leídas por actualizada_en.
CREATE INDEX transferencia_activas_por_actualizada
    ON nucleo.transferencia (actualizada_en)
 WHERE estado IN ('despachada', 'indeterminada');

-- descuadre es el registro operativo: se inserta y se resuelve después.
GRANT SELECT, INSERT ON nucleo.descuadre TO rol_nucleo;
GRANT UPDATE (resuelto_en, resolucion) ON nucleo.descuadre TO rol_nucleo;
REVOKE DELETE, TRUNCATE ON nucleo.descuadre FROM rol_nucleo;

-- conciliacion_corrida es el cursor append-only: solo se lee el último y se inserta.
GRANT SELECT, INSERT ON nucleo.conciliacion_corrida TO rol_nucleo;
REVOKE UPDATE, DELETE, TRUNCATE ON nucleo.conciliacion_corrida FROM rol_nucleo;

-- +goose Down
DROP TABLE nucleo.conciliacion_corrida;
DROP TABLE nucleo.descuadre;
DROP INDEX nucleo.transferencia_activas_por_actualizada;
DROP INDEX nucleo.journal_entry_transferencia_id;
DROP INDEX nucleo.journal_entry_ocurrido_en;

-- name: InsertarDescuadre :one
INSERT INTO nucleo.descuadre (
    tipo, transferencia_id, referencia_externa,
    monto_interno, monto_externo,
    ventana_desde, ventana_hasta
) VALUES (
    @tipo, @transferencia_id, @referencia_externa,
    @monto_interno, @monto_externo,
    @ventana_desde, @ventana_hasta
)
RETURNING id, tipo, transferencia_id, referencia_externa, monto_interno, monto_externo,
          ventana_desde, ventana_hasta, detectado_en, resuelto_en, resolucion;

-- name: BuscarDescuadresAbiertos :many
SELECT id, tipo, transferencia_id, referencia_externa, monto_interno, monto_externo,
       ventana_desde, ventana_hasta, detectado_en, resuelto_en, resolucion
  FROM nucleo.descuadre
 WHERE resuelto_en IS NULL
   AND tipo = @tipo
   AND COALESCE(transferencia_id::text, referencia_externa) = @identidad::text
 ORDER BY id;

-- name: ResolverDescuadre :execrows
UPDATE nucleo.descuadre
   SET resuelto_en = now(),
       resolucion = @resolucion
 WHERE id = @id
   AND resuelto_en IS NULL;

-- name: InsertarCorridaConciliacion :one
INSERT INTO nucleo.conciliacion_corrida (ventana_hasta)
VALUES (@ventana_hasta)
RETURNING id, ventana_hasta, corrida_en;

-- name: UltimaVentanaConciliada :one
SELECT ventana_hasta
  FROM nucleo.conciliacion_corrida
 ORDER BY ventana_hasta DESC, id DESC
 LIMIT 1;

-- name: LeerAsientosDeVentana :many
SELECT id, tipo, transferencia_id, ocurrido_en, registrado_en, descripcion, deposito_id
  FROM nucleo.journal_entry
 WHERE ocurrido_en >= @desde
   AND ocurrido_en < @hasta
 ORDER BY ocurrido_en, id;

-- name: LeerAsientosDeTransferencia :many
SELECT id, tipo, transferencia_id, ocurrido_en, registrado_en, descripcion, deposito_id
  FROM nucleo.journal_entry
 WHERE transferencia_id = @transferencia_id
 ORDER BY ocurrido_en, id;

-- name: LeerTransferenciasActivasDeVentana :many
SELECT id, estado, actualizada_en
  FROM nucleo.transferencia
 WHERE estado IN ('despachada', 'indeterminada')
   AND actualizada_en >= @desde
   AND actualizada_en < @hasta
 ORDER BY actualizada_en, id;

-- name: LeerTransferenciasIndeterminadas :many
SELECT id, usuario_id, cuenta_origen, llave_destino, monto, moneda, idempotency_key, estado, creada_en, actualizada_en
  FROM nucleo.transferencia
 WHERE estado = 'indeterminada'
 ORDER BY actualizada_en, id;

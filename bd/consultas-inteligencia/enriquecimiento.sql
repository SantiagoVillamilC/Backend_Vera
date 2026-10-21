-- name: AnclasPendientes :many
SELECT id
  FROM nucleo.journal_entry
 WHERE id > sqlc.arg('despues_de')::bigint
 ORDER BY id
 LIMIT sqlc.arg('limite')::int;

-- name: LineasDeAsientos :many
SELECT je.id AS entry_id,
       je.tipo,
       je.transferencia_id,
       je.ocurrido_en,
       je.descripcion,
       p.cuenta_id,
       p.monto,
       p.moneda
  FROM nucleo.journal_entry je
  JOIN nucleo.posting p ON p.entry_id = je.id
 WHERE je.id = ANY(sqlc.arg('asientos')::bigint[])
 ORDER BY je.id, p.id;

-- name: LineasDeTransferencias :many
SELECT je.id AS entry_id,
       je.tipo,
       je.transferencia_id,
       je.ocurrido_en,
       je.descripcion,
       p.cuenta_id,
       p.monto,
       p.moneda
  FROM nucleo.journal_entry je
  JOIN nucleo.posting p ON p.entry_id = je.id
 WHERE je.transferencia_id = ANY(sqlc.arg('transferencias')::uuid[])
 ORDER BY je.id, p.id;

-- name: InsertarMovimientoEnriquecido :exec
INSERT INTO inteligencia.movimiento_enriquecido (
    usuario_id, transaccion_id, entry_ancla, ocurrido_en, moneda, direccion, monto_centavos,
    contraparte, contraparte_resuelta, descripcion, categoria, origen_categoria,
    comercio_mid, comercio_nombre, mcc, version_modelo,
    tipo_destino, contraparte_llave, contraparte_nombre_normalizado
) VALUES (
    sqlc.arg('usuario_id'), sqlc.arg('transaccion_id'), sqlc.arg('entry_ancla'),
    sqlc.arg('ocurrido_en'), sqlc.arg('moneda'), sqlc.arg('direccion'),
    sqlc.arg('monto_centavos'), sqlc.arg('contraparte'), sqlc.arg('contraparte_resuelta'),
    sqlc.arg('descripcion'), sqlc.arg('categoria'), sqlc.arg('origen_categoria'),
    sqlc.arg('comercio_mid'), sqlc.arg('comercio_nombre'), sqlc.arg('mcc'),
    sqlc.arg('version_modelo'), sqlc.arg('tipo_destino'), sqlc.arg('contraparte_llave'),
    sqlc.arg('contraparte_nombre_normalizado')
)
ON CONFLICT (usuario_id, transaccion_id) DO UPDATE SET
    entry_ancla = EXCLUDED.entry_ancla,
    ocurrido_en = EXCLUDED.ocurrido_en,
    moneda = EXCLUDED.moneda,
    direccion = EXCLUDED.direccion,
    monto_centavos = EXCLUDED.monto_centavos,
    contraparte = EXCLUDED.contraparte,
    contraparte_resuelta = EXCLUDED.contraparte_resuelta,
    descripcion = EXCLUDED.descripcion,
    categoria = EXCLUDED.categoria,
    origen_categoria = EXCLUDED.origen_categoria,
    comercio_mid = EXCLUDED.comercio_mid,
    comercio_nombre = EXCLUDED.comercio_nombre,
    mcc = EXCLUDED.mcc,
    version_modelo = EXCLUDED.version_modelo,
    tipo_destino = EXCLUDED.tipo_destino,
    contraparte_llave = EXCLUDED.contraparte_llave,
    contraparte_nombre_normalizado = EXCLUDED.contraparte_nombre_normalizado,
    calculado_en = now();

-- name: ListarMemoriaDeUsuario :many
SELECT usuario_id, tipo_destino, nivel, valor, categoria, nombre
  FROM inteligencia.memoria_correccion
 WHERE usuario_id = sqlc.arg('usuario_id');

-- name: BorrarCopia :exec
DELETE FROM inteligencia.movimiento_enriquecido;

-- name: ReiniciarCursor :exec
UPDATE inteligencia.cursor_enriquecimiento
   SET ultimo_entry_id = 0,
       actualizado_en = now()
 WHERE id = 1;

-- name: ExportarCopia :many
SELECT usuario_id, transaccion_id, entry_ancla, ocurrido_en, moneda, direccion, monto_centavos,
       contraparte, contraparte_resuelta, descripcion, categoria, origen_categoria,
       comercio_mid, comercio_nombre, mcc, version_modelo,
       tipo_destino, contraparte_llave, contraparte_nombre_normalizado
  FROM inteligencia.movimiento_enriquecido
 ORDER BY usuario_id, ocurrido_en, transaccion_id;

-- name: BorrarMovimientoEnriquecido :exec
DELETE FROM inteligencia.movimiento_enriquecido
 WHERE usuario_id = sqlc.arg('usuario_id')
   AND transaccion_id = sqlc.arg('transaccion_id');

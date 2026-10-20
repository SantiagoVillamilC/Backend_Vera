-- name: PaginaDeMovimientos :many
SELECT m.transaccion_id,
       m.ocurrido_en,
       m.moneda,
       m.direccion,
       m.monto_centavos,
       m.contraparte,
       m.contraparte_resuelta,
       m.descripcion,
       m.categoria,
       c.nombre AS categoria_nombre,
       m.origen_categoria
  FROM inteligencia.movimiento_enriquecido m
  JOIN inteligencia.categoria c ON c.codigo = m.categoria
 WHERE m.usuario_id = sqlc.arg('usuario_id')::text
   AND (m.ocurrido_en, m.transaccion_id) < (
        COALESCE(sqlc.narg('cursor_ocurrido_en')::timestamptz, 'infinity'::timestamptz),
        COALESCE(sqlc.narg('cursor_transaccion_id')::uuid, 'ffffffff-ffff-ffff-ffff-ffffffffffff'::uuid))
 ORDER BY m.ocurrido_en DESC, m.transaccion_id DESC
 LIMIT sqlc.arg('limite')::int;

-- name: PrimerMovimientoDelUsuario :one
SELECT min(ocurrido_en)::timestamptz AS primero
  FROM inteligencia.movimiento_enriquecido
 WHERE usuario_id = sqlc.arg('usuario_id')::text;

-- name: TotalesPorCategoriaYMes :many
SELECT m.categoria,
       c.nombre AS categoria_nombre,
       date_trunc('month', m.ocurrido_en AT TIME ZONE 'America/Bogota')::date AS mes,
       COALESCE(SUM(m.monto_centavos), 0)::bigint AS total_centavos
  FROM inteligencia.movimiento_enriquecido m
  JOIN inteligencia.categoria c ON c.codigo = m.categoria
 WHERE m.usuario_id = sqlc.arg('usuario_id')::text
   AND m.direccion = 'salida'
   AND m.ocurrido_en >= sqlc.arg('desde')::timestamptz
   AND m.ocurrido_en < sqlc.arg('hasta')::timestamptz
 GROUP BY m.categoria, c.nombre, mes
 ORDER BY m.categoria, mes;

-- name: ContarAvisosDelMes :one
SELECT count(*)::bigint AS avisos_emitidos
  FROM inteligencia.aviso
 WHERE usuario_id = sqlc.arg('usuario_id')::text
   AND periodo >= sqlc.arg('desde')::date
   AND periodo < sqlc.arg('hasta')::date;

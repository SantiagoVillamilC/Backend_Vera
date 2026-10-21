-- name: FilaDeMovimiento :one
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
       m.origen_categoria,
       m.tipo_destino,
       m.contraparte_llave,
       m.comercio_mid,
       m.contraparte_nombre_normalizado
  FROM inteligencia.movimiento_enriquecido m
  JOIN inteligencia.categoria c ON c.codigo = m.categoria
 WHERE m.usuario_id = sqlc.arg('usuario_id')::text
   AND m.transaccion_id = sqlc.arg('transaccion_id')::uuid;

-- name: GuardarCategoriaDeMemoria :exec
INSERT INTO inteligencia.memoria_correccion (
    usuario_id, tipo_destino, nivel, valor, categoria, actualizado_en
) VALUES (
    sqlc.arg('usuario_id')::text, sqlc.arg('tipo_destino')::text,
    sqlc.arg('nivel')::text, sqlc.arg('valor')::text,
    sqlc.arg('categoria')::text, now()
)
ON CONFLICT (usuario_id, tipo_destino, nivel, valor) DO UPDATE SET
    categoria = EXCLUDED.categoria,
    actualizado_en = now();

-- name: GuardarNombreDeMemoria :exec
INSERT INTO inteligencia.memoria_correccion (
    usuario_id, tipo_destino, nivel, valor, nombre, actualizado_en
) VALUES (
    sqlc.arg('usuario_id')::text, sqlc.arg('tipo_destino')::text,
    sqlc.arg('nivel')::text, sqlc.arg('valor')::text,
    sqlc.arg('nombre')::text, now()
)
ON CONFLICT (usuario_id, tipo_destino, nivel, valor) DO UPDATE SET
    nombre = EXCLUDED.nombre,
    actualizado_en = now();

-- name: RecalcularCategoriaPorDestino :many
WITH objetivo AS (
    SELECT m.usuario_id,
           m.transaccion_id,
           (SELECT mc.categoria
              FROM inteligencia.memoria_correccion mc
              JOIN inteligencia.categoria cc ON cc.codigo = mc.categoria
             WHERE mc.usuario_id = m.usuario_id
               AND mc.tipo_destino = m.tipo_destino
               AND mc.categoria IS NOT NULL
               AND cc.grupo <> 'ninguno'
               AND ((mc.nivel = 'llave' AND mc.valor = m.contraparte_llave)
                 OR (mc.nivel = 'mid' AND mc.valor = m.comercio_mid)
                 OR (mc.nivel = 'nombre' AND mc.valor = m.contraparte_nombre_normalizado))
             ORDER BY CASE mc.nivel WHEN 'llave' THEN 1 WHEN 'mid' THEN 2 ELSE 3 END
             LIMIT 1) AS categoria
      FROM inteligencia.movimiento_enriquecido m
     WHERE m.usuario_id = sqlc.arg('usuario_id')::text
       AND m.tipo_destino = sqlc.arg('tipo_destino')::text
       AND (m.contraparte_llave = sqlc.narg('llave')::text
         OR m.comercio_mid = sqlc.narg('mid')::text
         OR m.contraparte_nombre_normalizado = sqlc.narg('nombre_normalizado')::text)
)
UPDATE inteligencia.movimiento_enriquecido m
   SET categoria = o.categoria,
       origen_categoria = 'memoria',
       calculado_en = now()
  FROM objetivo o
 WHERE m.usuario_id = o.usuario_id
   AND m.transaccion_id = o.transaccion_id
   AND o.categoria IS NOT NULL
   AND (m.categoria IS DISTINCT FROM o.categoria
     OR m.origen_categoria IS DISTINCT FROM 'memoria')
RETURNING m.transaccion_id;

-- name: RenombrarContrapartePorDestino :many
WITH objetivo AS (
    SELECT m.usuario_id,
           m.transaccion_id,
           (SELECT mc.nombre
              FROM inteligencia.memoria_correccion mc
             WHERE mc.usuario_id = m.usuario_id
               AND mc.tipo_destino = m.tipo_destino
               AND mc.nombre IS NOT NULL
               AND ((mc.nivel = 'llave' AND mc.valor = m.contraparte_llave)
                 OR (mc.nivel = 'mid' AND mc.valor = m.comercio_mid)
                 OR (mc.nivel = 'nombre' AND mc.valor = m.contraparte_nombre_normalizado))
             ORDER BY CASE mc.nivel WHEN 'llave' THEN 1 WHEN 'mid' THEN 2 ELSE 3 END
             LIMIT 1) AS nombre
      FROM inteligencia.movimiento_enriquecido m
     WHERE m.usuario_id = sqlc.arg('usuario_id')::text
       AND m.tipo_destino = 'comercio'
       AND (m.contraparte_llave = sqlc.narg('llave')::text
         OR m.comercio_mid = sqlc.narg('mid')::text
         OR m.contraparte_nombre_normalizado = sqlc.narg('nombre_normalizado')::text)
)
UPDATE inteligencia.movimiento_enriquecido m
   SET contraparte = o.nombre,
       comercio_nombre = o.nombre,
       calculado_en = now()
  FROM objetivo o
 WHERE m.usuario_id = o.usuario_id
   AND m.transaccion_id = o.transaccion_id
   AND o.nombre IS NOT NULL
   AND (m.contraparte IS DISTINCT FROM o.nombre
     OR m.comercio_nombre IS DISTINCT FROM o.nombre)
RETURNING m.transaccion_id;

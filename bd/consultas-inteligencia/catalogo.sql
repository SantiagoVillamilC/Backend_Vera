-- name: ListarCategorias :many
SELECT codigo, nombre, grupo, orden
  FROM inteligencia.categoria
 ORDER BY orden;

-- name: ListarCodigosDeCategoria :many
SELECT codigo
  FROM inteligencia.categoria
 WHERE inteligencia.categoria_es_visible(codigo)
 ORDER BY codigo;

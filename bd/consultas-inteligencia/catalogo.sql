-- name: ListarCategorias :many
SELECT codigo, nombre, grupo, orden
  FROM inteligencia.categoria
 ORDER BY orden;

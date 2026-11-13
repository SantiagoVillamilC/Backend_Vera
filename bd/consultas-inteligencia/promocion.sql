-- name: RegistrarVoto :exec
INSERT INTO inteligencia.voto_destino (tipo_destino, nivel, valor, usuario_id, categoria)
VALUES (sqlc.arg('tipo_destino'), sqlc.arg('nivel'), sqlc.arg('valor'), sqlc.arg('usuario_id'), sqlc.arg('categoria'))
ON CONFLICT (tipo_destino, nivel, valor, usuario_id)
DO UPDATE SET categoria = EXCLUDED.categoria, creado_en = now();

-- name: ContarVotosPorCategoria :many
SELECT categoria, count(DISTINCT usuario_id)::int AS votos
  FROM inteligencia.voto_destino
 WHERE tipo_destino = sqlc.arg('tipo_destino')
   AND nivel = sqlc.arg('nivel')
   AND valor = sqlc.arg('valor')
 GROUP BY categoria
 ORDER BY votos DESC, categoria;

-- name: PromoverDestino :exec
INSERT INTO inteligencia.promocion_destino (tipo_destino, nivel, valor, categoria)
VALUES (sqlc.arg('tipo_destino'), sqlc.arg('nivel'), sqlc.arg('valor'), sqlc.arg('categoria'))
ON CONFLICT (tipo_destino, nivel, valor) DO NOTHING;

-- name: ListarPromociones :many
SELECT tipo_destino, nivel, valor, categoria, promovido_en
  FROM inteligencia.promocion_destino
 ORDER BY tipo_destino, nivel, valor;

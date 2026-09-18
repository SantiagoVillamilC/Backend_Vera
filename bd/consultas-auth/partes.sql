-- name: CrearParte :one
INSERT INTO auth.partes (id, tipo)
VALUES ($1, $2)
RETURNING *;

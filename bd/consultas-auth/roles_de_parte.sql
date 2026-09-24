-- name: CrearRolDeParte :one
INSERT INTO auth.roles_de_parte (parte_id, rol, representada_id)
VALUES ($1, $2, $3)
RETURNING *;

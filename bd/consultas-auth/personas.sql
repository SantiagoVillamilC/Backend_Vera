-- name: CrearPersona :one
INSERT INTO auth.personas (parte_id, tipo_documento, documento, nombre, perfil_nombre, nacida_en)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

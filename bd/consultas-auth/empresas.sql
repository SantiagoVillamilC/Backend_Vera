-- name: CrearEmpresa :one
INSERT INTO auth.empresas (parte_id, nit, razon_social)
VALUES ($1, $2, $3)
RETURNING *;

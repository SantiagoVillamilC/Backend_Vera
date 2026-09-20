-- name: CrearEmpresa :one
INSERT INTO auth.empresas (parte_id, nit, razon_social)
VALUES ($1, $2, $3)
RETURNING *;

-- name: EmpresaExistePorNit :one
SELECT EXISTS (SELECT 1 FROM auth.empresas WHERE nit = $1);

-- name: EmpresaDeParte :one
SELECT nit, razon_social
FROM auth.empresas
WHERE parte_id = $1;

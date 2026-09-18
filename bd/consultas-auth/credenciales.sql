-- name: CrearCredencial :one
INSERT INTO auth.credenciales (id, parte_id, correo, celular, valor_derivado, estado)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: CredencialPorID :one
SELECT * FROM auth.credenciales
WHERE id = $1;

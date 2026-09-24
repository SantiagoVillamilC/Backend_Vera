-- name: GetUserByEmail :one
SELECT id, email, created_at
FROM users
WHERE email = $1;

-- name: GetUserByID :one
SELECT id, email, created_at
FROM users
WHERE id = $1;

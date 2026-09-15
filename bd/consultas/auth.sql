-- name: GetUserByEmail :one
SELECT id, email, created_at
FROM users
WHERE email = $1;

-- name: CreateUser :one
INSERT INTO users (id, email)
VALUES ($1, $2)
RETURNING id, email, created_at;

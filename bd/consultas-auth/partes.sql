-- name: CrearParte :one
INSERT INTO auth.partes (id, tipo)
VALUES ($1, $2)
RETURNING *;

-- name: InsertarParteDePrueba :exec
INSERT INTO auth.partes (id, tipo)
VALUES ($1, 'persona')
ON CONFLICT (id) DO NOTHING;

-- name: ResolverDestinoPorID :one
SELECT p.id
FROM auth.partes p
LEFT JOIN auth.credenciales c ON c.parte_id = p.id
LEFT JOIN auth.empresas e ON e.parte_id = p.id
WHERE p.id = $1
  AND (e.parte_id IS NOT NULL OR (c.id IS NOT NULL AND c.estado <> 'cerrado'));

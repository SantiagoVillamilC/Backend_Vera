-- name: InsertarTokenRestablecimiento :exec
INSERT INTO auth.tokens_restablecimiento (id, credencial_id, token_hash, expira_en)
VALUES ($1, $2, $3, $4);

-- name: TokenRestablecimientoVigente :one
SELECT t.credencial_id, c.parte_id
FROM auth.tokens_restablecimiento t
JOIN auth.credenciales c ON c.id = t.credencial_id
WHERE t.token_hash = $1 AND t.usado_en IS NULL AND t.expira_en > now();

-- name: ConsumirTokenRestablecimiento :one
UPDATE auth.tokens_restablecimiento
SET usado_en = now()
WHERE token_hash = $1 AND usado_en IS NULL AND expira_en > now()
RETURNING credencial_id;

-- name: ActualizarContrasena :execrows
UPDATE auth.credenciales
SET valor_derivado = $1, actualizada_en = now()
WHERE id = $2 AND estado = 'activo';

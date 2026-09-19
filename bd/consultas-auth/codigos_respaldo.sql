-- name: CrearCodigoRespaldo :exec
INSERT INTO auth.codigos_respaldo (id, credencial_id, codigo_hmac)
VALUES ($1, $2, $3);

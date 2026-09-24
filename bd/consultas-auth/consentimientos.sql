-- name: CrearConsentimiento :one
INSERT INTO auth.consentimientos (
    id, parte_id, tipo, version_texto, texto, aceptado, accion, actor, origen, user_agent, metodo, ocurrido_en
) VALUES (
    @id, @parte_id, @tipo, @version_texto, @texto, @aceptado, @accion, @actor, @origen, @user_agent, @metodo, @ocurrido_en
)
RETURNING *;

-- name: TextoDeConsentimiento :one
SELECT tipo, version, texto
FROM auth.textos_consentimiento
WHERE tipo = @tipo AND version = @version;

-- name: UltimoConsentimientoPorTipo :one
SELECT aceptado
FROM auth.consentimientos
WHERE parte_id = @parte_id AND tipo = @tipo
ORDER BY ocurrido_en DESC
LIMIT 1;

-- name: ListarConsentimientosDeParte :many
SELECT *
FROM auth.consentimientos
WHERE parte_id = @parte_id
ORDER BY ocurrido_en DESC;

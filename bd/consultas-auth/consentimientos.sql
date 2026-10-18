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

-- name: TextoVigenteAsistente :one
SELECT tipo, version, texto FROM auth.textos_consentimiento
WHERE tipo = 'asistente_ia' AND vigente_desde <= clock_timestamp()
ORDER BY vigente_desde DESC, version DESC LIMIT 1;

-- Una única sentencia evita combinar un texto y un evento de snapshots distintos.
-- name: EstadoConsentimientoAsistente :one
SELECT t.tipo, t.version, t.texto,
    COALESCE((SELECT c.aceptado AND c.accion = 'otorgar' AND c.version_texto = t.version
        FROM auth.consentimientos c
        WHERE c.parte_id = @parte_id AND c.tipo = 'asistente_ia'
        ORDER BY c.orden DESC LIMIT 1), false)::boolean AS autorizado
FROM auth.textos_consentimiento t
WHERE t.tipo = 'asistente_ia' AND t.vigente_desde <= clock_timestamp()
ORDER BY t.vigente_desde DESC, t.version DESC LIMIT 1;

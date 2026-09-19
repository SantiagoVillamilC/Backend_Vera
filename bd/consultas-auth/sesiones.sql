-- name: InsertarSesion :one
INSERT INTO auth.sesiones (id, familia_id, parte_id, contexto_activo, dispositivo, refresh_hash, ultimo_uso_en, vence_en)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, familia_id, parte_id, contexto_activo, dispositivo, refresh_hash, creada_en, ultimo_uso_en, usado_en, revocada_en, vence_en;

-- name: RotarSesion :one
UPDATE auth.sesiones
SET usado_en = now()
WHERE refresh_hash = $1 AND usado_en IS NULL AND revocada_en IS NULL AND vence_en > now()
RETURNING id, familia_id, parte_id, contexto_activo, dispositivo, refresh_hash, creada_en, ultimo_uso_en, usado_en, revocada_en, vence_en;

-- name: SesionPorRefreshHash :one
SELECT id, familia_id, parte_id, contexto_activo, dispositivo, refresh_hash, creada_en, ultimo_uso_en, usado_en, revocada_en, vence_en
FROM auth.sesiones
WHERE refresh_hash = $1;

-- name: PrimeraSesionDeFamilia :one
SELECT min(creada_en)::timestamptz AS creada_en
FROM auth.sesiones
WHERE familia_id = $1;

-- name: RevocarSesionDeParte :execrows
UPDATE auth.sesiones
SET revocada_en = now()
WHERE id = $1 AND parte_id = $2 AND revocada_en IS NULL;

-- name: RevocarFamiliaDeParte :execrows
UPDATE auth.sesiones
SET revocada_en = now()
WHERE familia_id = $1 AND parte_id = $2 AND revocada_en IS NULL;

-- name: ActualizarContextoDeFamilia :execrows
UPDATE auth.sesiones
SET contexto_activo = $2
WHERE familia_id = $1 AND usado_en IS NULL AND revocada_en IS NULL;

-- name: RevocarFamilia :execrows
UPDATE auth.sesiones
SET revocada_en = now()
WHERE familia_id = $1 AND revocada_en IS NULL;

-- name: RevocarSesionesDeParte :execrows
UPDATE auth.sesiones
SET revocada_en = now()
WHERE parte_id = $1 AND revocada_en IS NULL;

-- name: ListarSesionesActivas :many
SELECT id, familia_id, parte_id, contexto_activo, dispositivo, refresh_hash, creada_en, ultimo_uso_en, usado_en, revocada_en, vence_en
FROM auth.sesiones
WHERE parte_id = $1 AND revocada_en IS NULL AND usado_en IS NULL AND vence_en > now()
ORDER BY ultimo_uso_en DESC;

-- name: RolesVigentesDeParte :many
SELECT rol, representada_id
FROM auth.roles_de_parte
WHERE parte_id = $1 AND revocado_en IS NULL
ORDER BY rol, representada_id;

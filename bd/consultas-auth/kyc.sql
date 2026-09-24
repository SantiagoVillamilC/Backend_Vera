-- name: TipoDeParte :one
SELECT tipo
FROM auth.partes
WHERE id = $1;

-- name: LeerEstadoKYC :one
SELECT producto, estado_kyc
FROM auth.kyc_estados
WHERE parte_id = $1;

-- name: AsegurarEstadoKYC :exec
INSERT INTO auth.kyc_estados (parte_id, producto, estado_kyc)
VALUES ($1, $2, $3)
ON CONFLICT (parte_id) DO NOTHING;

-- name: UpsertEstadoKYC :exec
INSERT INTO auth.kyc_estados (parte_id, producto, estado_kyc)
VALUES ($1, $2, $3)
ON CONFLICT (parte_id) DO UPDATE
SET producto = EXCLUDED.producto, estado_kyc = EXCLUDED.estado_kyc, actualizado_en = now();

-- name: InsertarVerificacionKYC :exec
INSERT INTO auth.kyc_verificaciones (parte_id, nivel_solicitado, medio, proveedor, resultado, referencia_proveedor, ocurrida_en)
VALUES ($1, $2, $3, $4, $5, $6, $7);

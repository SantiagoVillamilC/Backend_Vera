-- name: InsertarExtractoCustodio :exec
INSERT INTO nucleo.extracto_custodio (corte_en, saldo, origen)
VALUES (@corte_en, @saldo, @origen);

-- name: ExtractoPorCorte :one
SELECT corte_en, saldo, origen
  FROM nucleo.extracto_custodio
 WHERE corte_en = @corte_en;

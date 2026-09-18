-- name: InsertarExtractoCustodio :exec
INSERT INTO nucleo.extracto_custodio (corte_en, saldo, origen)
VALUES (@corte_en, @saldo, @origen);

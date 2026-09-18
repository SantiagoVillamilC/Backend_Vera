-- name: ReglasLimiteVigentes :many
SELECT ventana, tope_uvt_centesimas
  FROM nucleo.regla_limite
 WHERE nivel_kyc = @nivel_kyc
   AND vigente_desde <= @fecha
   AND (vigente_hasta IS NULL OR vigente_hasta > @fecha)
 ORDER BY vigente_desde DESC;

-- name: ValorUVTDelAnio :one
SELECT valor_centavos
  FROM nucleo.valor_uvt
 WHERE anio = @anio;

-- name: SumaDebitoDelMes :one
SELECT COALESCE(SUM(monto), 0)::bigint AS suma
  FROM nucleo.transferencia
 WHERE usuario_id = @usuario_id
   AND estado = ANY(@estados::text[])
   AND creada_en >= @desde
   AND creada_en < @hasta;

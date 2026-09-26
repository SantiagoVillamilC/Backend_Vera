-- name: PaginaDeAnclas :many
WITH grupos AS (
    SELECT COALESCE(je.transferencia_id::text, 'entry:' || je.id::text) AS grupo,
           MIN(je.id) AS ancla
      FROM nucleo.posting p
      JOIN nucleo.journal_entry je ON je.id = p.entry_id
     WHERE p.cuenta_id = ANY(@cuentas::text[])
       AND (sqlc.narg('cursor')::bigint IS NULL OR je.id < sqlc.narg('cursor')::bigint)
     GROUP BY COALESCE(je.transferencia_id::text, 'entry:' || je.id::text)
)
SELECT grupo::text, ancla::bigint
  FROM grupos
 ORDER BY ancla DESC
 LIMIT sqlc.arg('limite')::int;

-- name: LineasDeMovimientos :many
SELECT je.id AS entry_id,
       je.tipo,
       je.transferencia_id,
       je.ocurrido_en,
       je.registrado_en,
       p.cuenta_id,
       p.monto,
       p.moneda
  FROM nucleo.journal_entry je
  JOIN nucleo.posting p ON p.entry_id = je.id
 WHERE je.id = ANY(@anclas::bigint[])
    OR je.transferencia_id IN (
         SELECT je2.transferencia_id
           FROM nucleo.journal_entry je2
          WHERE je2.id = ANY(@anclas::bigint[])
            AND je2.transferencia_id IS NOT NULL)
 ORDER BY je.id ASC, p.id ASC;

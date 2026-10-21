-- name: LeerCursor :one
SELECT ultimo_entry_id, actualizado_en
  FROM inteligencia.cursor_enriquecimiento
 WHERE id = 1;

-- name: AvanzarCursor :exec
UPDATE inteligencia.cursor_enriquecimiento
   SET ultimo_entry_id = sqlc.arg('ultimo_entry_id')::bigint,
       actualizado_en = now()
 WHERE id = 1
   AND ultimo_entry_id < sqlc.arg('ultimo_entry_id')::bigint;

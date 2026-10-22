-- Los IDs vienen exclusivamente de una página del historial propio o del resolvedor.
-- La búsqueda usa el nombre visible (primer nombre e inicial), no apellidos privados.
-- name: BuscarDestinatariosPrevios :many
WITH personas AS (
    SELECT p.parte_id, regexp_split_to_array(trim(p.nombre), '\s+') AS nombres,
           c.celular
    FROM auth.personas p
    JOIN auth.credenciales c ON c.parte_id = p.parte_id
    WHERE p.parte_id = ANY(@ids::uuid[]) AND c.estado = 'activo'
    AND c.celular_verificado_en IS NOT NULL
    AND (c.celular_cambiado_en IS NULL OR c.celular_cambiado_en <= now() - (sqlc.arg('enfriamiento_segundos')::double precision * interval '1 second'))
), visibles AS (
    SELECT parte_id, celular,
    (nombres[1] || CASE WHEN cardinality(nombres) > 1 THEN ' ' || left(nombres[greatest(2, cardinality(nombres)-1)],1) || '.' ELSE '' END)::text AS nombre
    FROM personas
)
SELECT parte_id, celular, nombre FROM visibles
WHERE strpos(translate(regexp_replace(lower(nombre), '[̀-ͯ]', '', 'g'), 'áéíóúüñ', 'aeiouun'), @nombre::text) > 0
ORDER BY parte_id LIMIT 6;

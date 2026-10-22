-- Consulta interna por referencias que el núcleo obtuvo del historial propio.
-- No resuelve celulares actuales: evita atribuir transferencias antiguas a un nuevo titular.
-- name: NombresDeContrapartes :many
SELECT p.id, COALESCE(per.nombre, em.razon_social, '')::text AS nombre
FROM auth.partes p
LEFT JOIN auth.personas per ON per.parte_id = p.id
LEFT JOIN auth.empresas em ON em.parte_id = p.id
WHERE p.id = ANY(@ids::uuid[]);

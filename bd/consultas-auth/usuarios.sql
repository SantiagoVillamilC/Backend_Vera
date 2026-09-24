-- name: UsuarioDeParte :one
SELECT p.tipo,
       c.correo,
       per.nombre AS nombre_completo,
       e.razon_social
FROM auth.partes p
LEFT JOIN auth.credenciales c ON c.parte_id = p.id
LEFT JOIN auth.personas per ON per.parte_id = p.id
LEFT JOIN auth.empresas e ON e.parte_id = p.id
WHERE p.id = $1;

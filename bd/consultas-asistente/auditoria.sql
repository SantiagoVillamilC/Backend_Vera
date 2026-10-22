-- Sin RETURNING: el rol del asistente sólo anexa, ni siquiera puede leer auditoría.
-- El mensaje y la parte se obtienen del contexto autorizado del servidor.
-- name: AnexarLlamadaFuncion :execrows
INSERT INTO asistente.llamada_funcion(llamada_id,fase,usuario_id,conversacion_id,mensaje_id,
    funcion,argumentos_saneados,resultado,duracion_ms,request_id,version_instrucciones)
SELECT @llamada_id,@fase,c.usuario_id,c.id,m.id,@funcion,@argumentos_saneados::jsonb,
    @resultado,@duracion_ms,@request_id,@version_instrucciones
FROM asistente.conversacion c
JOIN asistente.mensaje m ON m.conversacion_id=c.id
WHERE c.id = @conversacion_id AND c.usuario_id = @usuario_id AND m.id = @mensaje_id AND m.rol='usuario'
AND (@fase::text='fin' OR c.estado='activo');

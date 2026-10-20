-- Consultas de los cinco avisos.
--
-- Gasto e ingreso se leen de la copia enriquecida con corte en el instante de la evaluación, porque un
-- asiento posterior a ese instante no puede contar. El saldo suma la subcuenta disponible y la
-- retenida: en el diario una subcuenta pasivo acumula negativo, así que el saldo del usuario es cero
-- menos esa suma. Los pagos excluyen las etiquetas genéricas, que no identifican un destino recurrente.

-- name: GastoPorCategoriaYMesParaAvisos :many
SELECT m.categoria,
       date_trunc('month', m.ocurrido_en AT TIME ZONE 'America/Bogota')::date AS mes,
       COALESCE(SUM(m.monto_centavos), 0)::bigint AS total_centavos
  FROM inteligencia.movimiento_enriquecido m
 WHERE m.usuario_id = sqlc.arg('usuario_id')::text
   AND m.direccion = 'salida'
   AND m.tipo_destino = 'comercio'
   AND m.contraparte <> 'GMF (impuesto)'
   AND m.ocurrido_en <= sqlc.arg('ahora')::timestamptz
 GROUP BY m.categoria, mes
 ORDER BY m.categoria, mes;

-- name: IngresoPorMesParaAvisos :many
SELECT date_trunc('month', j.ocurrido_en AT TIME ZONE 'America/Bogota')::date AS mes,
       COALESCE(SUM(-p.monto), 0)::bigint AS total_centavos
  FROM nucleo.journal_entry j
  JOIN nucleo.posting p ON p.entry_id = j.id
 WHERE p.cuenta_id IN (
        'pasivo:usuario:' || sqlc.arg('usuario_id')::text || ':disponible',
        'pasivo:usuario:' || sqlc.arg('usuario_id')::text || ':retenido')
   AND j.tipo = ANY(sqlc.arg('tipos_asiento')::text[])
   AND COALESCE(j.descripcion, '') <> ALL(sqlc.arg('descripciones_excluidas')::text[])
   AND j.ocurrido_en <= sqlc.arg('ahora')::timestamptz
 GROUP BY mes
 ORDER BY mes;

-- name: PagosDelUsuarioParaSeries :many
SELECT m.transaccion_id,
       m.ocurrido_en,
       m.monto_centavos,
       m.tipo_destino,
       m.contraparte_llave,
       m.contraparte_nombre_normalizado,
       m.categoria
  FROM inteligencia.movimiento_enriquecido m
 WHERE m.usuario_id = sqlc.arg('usuario_id')::text
   AND m.direccion = 'salida'
   AND m.tipo_destino IN ('comercio', 'persona')
   AND (m.contraparte_llave IS NOT NULL OR m.contraparte_nombre_normalizado IS NOT NULL)
   AND m.contraparte <> ALL(sqlc.arg('etiquetas_genericas')::text[])
   AND m.ocurrido_en <= sqlc.arg('ahora')::timestamptz
 ORDER BY m.ocurrido_en, m.transaccion_id;

-- name: SaldoDelUsuarioHasta :one
SELECT COALESCE(SUM(p.monto), 0)::bigint AS saldo_centavos
  FROM nucleo.posting p
  JOIN nucleo.journal_entry j ON j.id = p.entry_id
 WHERE p.cuenta_id IN (
        'pasivo:usuario:' || sqlc.arg('usuario_id')::text || ':disponible',
        'pasivo:usuario:' || sqlc.arg('usuario_id')::text || ':retenido')
   AND j.ocurrido_en <= sqlc.arg('ahora')::timestamptz;

-- name: AvisosDelUsuarioEnVentana :many
SELECT tipo, clave, periodo
  FROM inteligencia.aviso
 WHERE usuario_id = sqlc.arg('usuario_id')::text
   AND periodo >= sqlc.arg('desde')::date
   AND periodo < sqlc.arg('hasta')::date;

-- name: AvisosVigentesDelMes :many
SELECT id, tipo, clave, periodo, prioridad, severidad_centavos, texto, transaccion_id, creado_en
  FROM inteligencia.aviso
 WHERE usuario_id = sqlc.arg('usuario_id')::text
   AND descartado_en IS NULL
   AND creado_en >= sqlc.arg('desde')::timestamptz
   AND creado_en < sqlc.arg('hasta')::timestamptz
 ORDER BY prioridad, severidad_centavos DESC, clave, id;

-- name: ContarAvisosEmitidosEnElMes :one
SELECT count(*)::bigint AS emitidos
  FROM inteligencia.aviso
 WHERE usuario_id = sqlc.arg('usuario_id')::text
   AND creado_en >= sqlc.arg('desde')::timestamptz
   AND creado_en < sqlc.arg('hasta')::timestamptz;

-- name: EmitirAviso :one
INSERT INTO inteligencia.aviso (
    id, usuario_id, tipo, clave, periodo, severidad_centavos, prioridad,
    texto, texto_verificado, transaccion_id, creado_en
) VALUES (
    sqlc.arg('id')::uuid,
    sqlc.arg('usuario_id')::text,
    sqlc.arg('tipo')::text,
    sqlc.arg('clave')::text,
    sqlc.arg('periodo')::date,
    sqlc.arg('severidad_centavos')::bigint,
    sqlc.arg('prioridad')::smallint,
    sqlc.arg('texto')::text,
    sqlc.arg('texto_verificado')::boolean,
    sqlc.narg('transaccion_id')::uuid,
    sqlc.arg('creado_en')::timestamptz
)
ON CONFLICT ON CONSTRAINT aviso_dedupe_unico DO NOTHING
RETURNING id;

-- name: AvisoPorIdentidad :one
SELECT id, creado_en
  FROM inteligencia.aviso
 WHERE usuario_id = sqlc.arg('usuario_id')::text
   AND tipo = sqlc.arg('tipo')::text
   AND clave = sqlc.arg('clave')::text
   AND periodo = sqlc.arg('periodo')::date;

-- name: DescartarAviso :one
UPDATE inteligencia.aviso
   SET descartado_en = COALESCE(descartado_en, sqlc.arg('descartado_en')::timestamptz)
 WHERE id = sqlc.arg('id')::uuid
   AND usuario_id = sqlc.arg('usuario_id')::text
RETURNING id, descartado_en;

-- name: UsuariosConHistorial :many
SELECT DISTINCT usuario_id
  FROM inteligencia.movimiento_enriquecido
 WHERE usuario_id > sqlc.arg('despues_de')::text
 ORDER BY usuario_id
 LIMIT sqlc.arg('limite')::int;

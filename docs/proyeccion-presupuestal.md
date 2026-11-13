# Proyección presupuestal del aviso de gasto

El aviso `presupuesto_anticipado` compara el gasto del mes en curso con lo habitual de los meses
completos y avisa cuando lo supera. No es un modelo entrenado ni una probabilidad: la regla viva
compara dos cifras exactas y el texto de la tarjeta solo afirma las cifras que el hecho trae.

## La fórmula exacta

Con `prorrateo_acumulado` encendido y un acumulado distinto de cero, la proyección del mes es

```
proyeccion = acumulado_del_mes * dias_del_mes / dias_transcurridos
```

Con la configuración que corre hoy (`internal/inteligencia/avisos/config.go`, `ProrrateoAcumulado:
false`, que replica `analisis/config/avisos.json`) el factor es 1 y la proyección es el acumulado
tal cual. El cálculo vive en `internal/inteligencia/avisos/detectores.go` (`proyectado`, línea 442)
y usa `math/big.Rat`, nunca coma flotante. La variante con prorrateo está en
`analisis/config/avisos-prorrateo.json` y la fija `TestPresupuestoConProrrateo`.

La referencia es la **mediana** del gasto total de los meses completos que tengan gasto mayor que
cero, y hacen falta **mínimo 3** meses (`MesesMinimos: 3` y `LineaBase: mediana`, en la misma
configuración). Un mes es completo cuando empezó el día 1 y ya terminó antes del mes en curso
(`MesesCompletosAntes`, `internal/inteligencia/avisos/calendario.go`).

La regla viva es R1: dispara si

```
proyeccion > 1.0 * mediana        (MultiplicadorGasto: 1.0)
```

La comparación es exacta (`superior`, `detectores.go:417`). R2 con z modificado y MAD existe y está
probada, pero `ReglaViva` fija R1. La severidad que ordena la bandeja es `proyeccion - referencia` y
la prioridad del tipo es 4 de 5.

Cifras del escenario de la prueba de punta a punta, con tres meses completos de $900.000 y un
septiembre de $1.500.000: proyección $1.500.000, referencia $900.000 y severidad $600.000.

## De dónde salen las cifras

- Gasto: `inteligencia.movimiento_enriquecido`, solo salidas a `comercio`, sin GMF, agregadas por
  `date_trunc('month', ocurrido_en at time zone 'America/Bogota')` y cortadas en el instante de la
  evaluación. La consulta es `GastoPorCategoriaYMesParaAvisos` en `bd/consultas-inteligencia/avisos.sql`.
- El primer movimiento del usuario sale de la misma copia y decide cuántos meses completos hay
  (`PrimerMovimientoDelUsuario`).
- Los montos son centavos enteros en el dominio y en la base. La conversión a pesos ocurre una sola
  vez, al armar el texto, en `avisos.Pesos` (`$1.500.000` o `$1.234,56`).
- El hecho ya redondeado se guarda en `inteligencia.aviso.monto_centavos` y
  `inteligencia.aviso.referencia_centavos` (migración 0033), con `guardarAviso` en
  `internal/inteligencia/avisos_servicio.go:281`. La bandeja devuelve esos dos campos como cadenas
  (`monto_centavos` y `referencia_centavos` en el esquema `InteligenciaAviso` de `api/openapi.yaml`).
- El texto se arma en `internal/inteligencia/avisos/texto.go:108` y dice:
  "En <mes> llevas $X en gastos. Lo habitual en tus meses completos es $Y." `VerificarTarjeta`
  rechaza cualquier cifra que no sea la del hecho, así que el texto y los campos estructurados no
  pueden separarse.

## Cómo corre el job

`EmitirAvisos` (`internal/inteligencia/avisos_servicio.go:71`) recorre en páginas de 200 a todos los
usuarios con historial, y por usuario y en una transacción toma el candado de avisos, arma el
contexto con corte en `ahora`, evalúa los cinco detectores, agrupa por periodo, aplica la
deduplicación y el cupo, y guarda las tarjetas.

Hay dos caminos para dispararlo:

- `cmd/api` con `AVISOS_ACTIVO=true` registra `AvisosWorker` en River a la hora de Colombia de
  `AVISOS_HORA` (06:00 por defecto). En `infra/servidor/banderas.tsv` la bandera está apagada en los
  tres destinos.
- `cmd/avisos -ahora=<RFC3339>` corre el emisor una sola vez y reporta `emitidos`, `duracion_ms` y
  `ahora`. Lee `APP_DATABASE_URL` y `AVISOS_CUPO`, y sin `-ahora` usa el reloj del sistema.

El emisor acepta y rechaza lo mismo que la API: `AVISOS_CUPO` vacío toma 3, y cero, negativo o
ilegible detiene la corrida.

## Cómo se reproduce en local

La base desechable es la del puerto 55437 y no cuesta dinero.

```
cd /home/chris/Documents/universidad-2026B/proyecto-fin-de-grado/Backend_Vera
./infra/local/levantar-base.sh --semilla=200
set -a && source cargas/inteligencia/entorno.sh && set +a
out/bin/avisos -ahora=2026-09-15T15:00:00Z
```

Sin `--semilla` la base no tiene usuarios con historial y la corrida reporta `emitidos: 0`. Con la
semilla, los usuarios sintéticos de agosto y septiembre producen tarjetas y el listado se consulta
por HTTP con un token de `out/local/tokens.json`:

```
curl -s -H "Authorization: Bearer <token>" -H 'X-Real-IP: 10.20.0.1' \
  'http://127.0.0.1:8080/v1/inteligencia/avisos?mes=2026-09'
```

El escenario exacto de la prueba de punta a punta se reproduce con la prueba, que siembra tres meses
completos y un septiembre por encima de la mediana en un usuario nuevo y luego emite, lista y
descarta:

```
go test -tags integration -count=1 -p 1 -v \
  -run TestElPresupuestoAnticipadoDiceSusCifrasYLiberaCupoAlDescartar ./internal/inteligencia/
```

El descarte se hace con `POST /v1/inteligencia/avisos/{id}/descartar` y la bandeja con
`GET /v1/inteligencia/avisos`. Descartar no borra la tarjeta: sale del listado, libera cupo en su
periodo y sigue bloqueando la reemisión por la clave única de deduplicación.

## Canal de notificación

El canal es la bandeja de avisos dentro del producto: la tabla `inteligencia.aviso` y las rutas
`GET /v1/inteligencia/avisos`, `GET /v1/inteligencia/avisos/{id}` y
`POST /v1/inteligencia/avisos/{id}/descartar`. No hay envío externo.

La tabla `inteligencia.aviso_entrega` (migración 0024) existe con `canal`, `estado` y `enviado_en`,
pero hoy nada en `internal/` escribe en ella, así que no hay push, correo ni SMS implementados. El
descarte es un estado de la tarjeta, no una entrega. Cuando se implemente un canal externo, la
entrega irá en esa tabla para que la decisión no dependa del medio.

## Criterio de la prueba

La prueba de punta a punta falla si el aviso de presupuesto sale sin cifras, si falta la referencia,
si la proyección o la referencia no son las del escenario, o si el texto y los campos estructurados
discrepan. La comparación del texto extrae los montos impresos y los contrasta con
`monto_centavos` y `referencia_centavos` sin volver a calcular la proyección. La prueba unitaria
`TestLaTarjetaDiceLasCifrasDelHecho` (`internal/inteligencia/avisos/texto_test.go`) hace lo mismo
para los cinco tipos desde el hecho.

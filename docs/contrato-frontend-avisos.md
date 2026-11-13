# Contrato de avisos para el frontend

Inventario de lo que la API de avisos entrega hoy, hecho para las tarjetas CF-A4-01 a 03 (wireframes
y pantallas de avisos). Todo se verificó contra el código en `7250970` (HEAD al empezar) y contra una
API levantada con la semilla de CF-A2-03. Cada afirmación lleva `ruta:línea` o se rotula **no
verificado**. Los dos defectos que documentó la captura (bandeja por defecto incompleta y descarte con
500) ya están corregidos en el backend: la sección siguiente dice qué cambió, y las capturas de la
sección 4 se conservan como evidencia de **antes** de la corrección, rotuladas así. La sección 3 ya
no es propuesta: `accion_codigo` está implementado.

## Corregido: la bandeja por defecto y el descarte de avisos futuros

### Defecto 1: la bandeja por defecto ocultaba tres de los cinco tipos (corregido)

Antes la bandeja sin parámetros (y con `limite`, `cursor` o `estado`) filtraba con igualdad exacta
contra el primer día del mes (`periodo = $2`), pero solo `presupuesto_anticipado` y
`categoria_sobre_media` guardan el día 1. Ahora las dos consultas paginadas
(`AvisosVigentesPorPagina` y `AvisosDescartadosPorPagina`, `bd/consultas-inteligencia/avisos.sql`)
usan el rango del mes: `periodo >= primer día del mes AND periodo < primer día del mes siguiente`,
el mismo criterio de `AvisosVigentesDelMes` (`?mes=`), del cupo y del resumen. El código de
`internal/inteligencia/internal/repositorio` se regeneró con `sqlc generate`. El orden (prioridad
ascendente, severidad descendente, clave, id), la paginación por cursor y el contrato de `?mes=` no
cambian.

Qué entra ahora en la bandeja del mes en curso, según el periodo que guarda cada tipo:

| Tipo | Periodo que guarda | Cita | Entra en la bandeja por defecto |
| --- | --- | --- | --- |
| `presupuesto_anticipado` | día 1 del mes en curso | `detectores.go:393`, `texto.go:145` | Sí |
| `categoria_sobre_media` | día 1 del mes en curso | `detectores.go:393`, `texto.go:145` | Sí |
| `pago_no_alcanza` | fecha del próximo pago esperado | `detectores.go:252`, `texto.go:148` | Sí, si esa fecha cae en el mes en curso (antes solo si era día 1) |
| `cobro_nuevo` | fecha del primer pago de la llave | `detectores.go:339`, `texto.go:148` | Sí, si esa fecha cae en el mes en curso (antes solo si era día 1) |
| `caida_ingreso` | día 1 del mes **anterior** (`MesEvaluado`) | `detectores.go:394-396` | **No** en el mes en curso: pertenece al mes evaluado y se lista con `?mes=` de ese mes |

Lo que sigue igual por diseño, y que la UI debe conocer:

- El periodo es el mes al que **pertenece** el hecho, no el mes en que se entrega la tarjeta
  (`api/openapi.yaml:972-975`, `avisos/orden.go:61-90`). Por eso `caida_ingreso` (que evalúa el mes
  completo anterior) y un `cobro_nuevo` cuyo primer pago fue el mes pasado aparecen en la bandeja de
  **ese** mes, no en la del mes en curso. Mostrarlos en la bandeja actual habría roto la regla de que
  bandeja, cupo y resumen cuentan lo mismo. Si producto quiere que una caída de ingreso aparezca el mes
  en que se detecta, es una decisión de semántica del periodo, no un cambio de consulta.
- Un `pago_no_alcanza` cuyo próximo pago cae en el mes siguiente aparece en la bandeja de ese mes
  siguiente, no en la actual.
- `estado=descartados` usa el mismo rango, así que ya lista los descartados con fecha de pago.
  Los descartados de meses anteriores siguen sin poder listarse.

Evidencia **de antes** de la corrección (sección 4, bloques A1 a A3): con la semilla de la demo la
bandeja por defecto devolvía 1 tarjeta, `?mes=2026-11` devolvía 2 y `GET /v1/inteligencia/resumen`
informaba `avisos_emitidos: 2`. Con el rango, la bandeja por defecto de esa misma captura devuelve (derivado, no
recapturado en vivo) las 2 tarjetas de noviembre (`presupuesto_anticipado` y `pago_no_alcanza`), igual que `?mes=2026-11` y el
resumen. La prueba de integración no lo detectaba porque sembraba las cinco filas con
`periodo = '2026-07-01'`: ahora las pruebas siembran cada tipo con su periodo real (ver abajo).

Pruebas: `TestPaginaAvisosPeriodosPropiosDeCadaTipo` y
`TestPaginaAvisosDescartadosConPeriodoDistintoDelDiaUno`
(`internal/inteligencia/avisos_pagina_integration_test.go`) y
`TestAvisosBandejaPorDefectoConPeriodosDeCadaTipoHTTP`
(`internal/platform/httpserver/avisos_handoff_integration_test.go`). Siembran `pago_no_alcanza` el 20 de
julio, `cobro_nuevo` el 28 de julio, `caida_ingreso` en junio y filas en los bordes (30 de junio y 1 de
agosto, que no deben entrar), comprueban el orden, el recorrido por cursor con `limite=2` y la paridad
con `?mes=`. La prueba con periodo día 1 se mantiene.

### Defecto 3: descartar un aviso "futuro" respondía 500 (corregido)

`POST /v1/inteligencia/avisos/{id}/descartar` estampa `descartado_en` con el reloj del servidor y la
migración 0026 exige `descartado_en >= creado_en` (CHECK `aviso_descarte_no_anterior_al_alta`). Si el
aviso tiene `creado_en` posterior al reloj (la semilla de demo fija el día 19), la base rechazaba la
fila y el handler respondía 500 `InternalError`. Ahora el servicio detecta esa violación concreta
(SQLSTATE 23514 con esa restricción) y devuelve `ErrDescarteAnteriorAlAlta`
(`internal/inteligencia/avisos_servicio.go`), y el handler responde:

```json
{"success": false, "data": null, "error": {"code": "DescarteAnteriorAlAlta", "message": "The notice cannot be dismissed before its creation time."}, "meta": null}
```

con estado **409**, el sobre estándar y sin tocar el aviso: sigue vigente y con `descartado_en`
nulo. No se cambió el CHECK ni los timestamps guardados, y el `409` se declaró en las respuestas de la
operación (`api/openapi.yaml`). Para la demo basta esperar a que el reloj real pase el `creado_en`
del aviso o sembrar con una `DEMO_FECHA` ya pasada. Pruebas:
`TestDescartarAvisoConAltaFuturaResponde409` (unitaria,
`internal/inteligencia/avisos_pagina_test.go`) y `TestAvisosDescartarConAltaFuturaResponde409HTTP`
(integración, `avisos_handoff_integration_test.go`).

## 1. Endpoints

Los tres exigen sesión: sin identidad responden 401 (`handler.go:293-300`). El sobre es
`{success, data, error, meta}` y `meta` es siempre `null` en estos tres (`handler.go:401`, `:444`,
`:468`, `:527`).

### GET /v1/inteligencia/avisos (`api/openapi.yaml:967-1014`, handler `handler.go:369-445`)

| Parámetro | Regla | Error | Capturado |
| --- | --- | --- | --- |
| `limite` | entero de 1 a 50, por defecto 20 (`handler.go:404-411`), llega como texto | 400 `limite`/`range` | `limite=51` |
| `cursor` | opaco, base64url de `v1\|prioridad\|severidad\|id\|clave` (`avisos_pagina.go:47-48`), no se construye a mano | 400 `cursor`/`format` | `cursor=abc` |
| `estado` | `vigentes` (defecto) o `descartados` | 400 `estado`/`oneof` | `estado=todos` |
| `mes` | `YYYY-MM`, por defecto el mes en curso | 400 `mes`/`format` | `mes=2026-13` |

- `mes` no se combina con `limite`, `cursor` ni `estado`: 400 `mes`/`excluded_with` (`handler.go:375-378`,
  capturado con `mes=2026-11&limite=5`).
- Hay **dos modos** con semántica distinta:
  - Modo `mes` solo (`handler.go:387-402`): sin paginación, sin cursor, solo vigentes, filtra por
    rango de periodo (`avisos.sql:72-79`, `avisos_servicio.go:329-353`).
  - Modo bandeja (cualquier otra combinación, `handler.go:404-444`): pagina con una fila extra para
    saber si hay más, sin OFFSET (`avisos_pagina.go:99-157`), siempre del mes en curso con el rango del
    mes (ver arriba), y es el único donde se puede pedir `estado=descartados`.
- Orden en ambos modos: prioridad ascendente, severidad descendente, clave, id (`avisos.sql:79`,
  `:156`, `:176`).
- Paginación: si hay más páginas, el cursor viaja en el encabezado `X-Vera-Siguiente-Cursor`
  (`handler.go:441-443`), expuesto a CORS en `internal/platform/httpserver/router.go:112`. El
  encabezado **no está declarado** en las `responses` del OpenAPI, solo se menciona en la descripción
  (`api/openapi.yaml:978`). Sin más páginas el encabezado no se envía. **No capturado en vivo**: la
  bandeja por defecto de la demo tiene 1 tarjeta. Derivado de `handler.go:437-445` y de
  `avisos_pagina_integration_test.go`.
- Periodo que devuelve la bandeja: el mes en curso según el reloj del servidor, en zona fija
  UTC-5 (`avisos/tipos.go:16`, `Zona`, y `avisos_servicio.go:390-395`). El reloj es `time.Now` salvo
  inyección de pruebas (`handler.go:62-63`). El filtro por periodo entró en `559e069`.
- Descartados: solo con `estado=descartados` y solo del mes en curso (rango del mes, ya sin el
  defecto de la igualdad con el día 1). **No hay forma de listar descartados de meses anteriores.**
- Meses anteriores: solo con `?mes=YYYY-MM`, solo vigentes, sin paginar. Un mes sin avisos responde
  `data: []` con 200 (capturado `mes=2026-09`). Un mes futuro no está probado (**no verificado**).

### GET /v1/inteligencia/avisos/{id} (`api/openapi.yaml:1016-1029`, handler `handler.go:507-528`)

- `id` no UUID: 400 `id`/`uuid`. Inexistente o de otro usuario: 404 `NotFound` con mensaje
  `Notice not found.` (el mismo para los dos casos, `avisos_servicio.go:18`). Ambos capturados.
- Devuelve el aviso **sin importar si está descartado**: la consulta no filtra `descartado_en`
  (`avisos.sql:133-137`) ni lo selecciona, y el esquema no tiene ese campo
  (`api/openapi.yaml:3554-3607`). Capturado: el detalle del `cobro_nuevo` ya descartado responde 200
  idéntico al de uno vigente. **El detalle no permite saber si el aviso está descartado.**

### POST /v1/inteligencia/avisos/{id}/descartar (`api/openapi.yaml:1031-1067`, handler `handler.go:447-472`)

- Sin cuerpo. Responde `{id, descartado_en}` (`api/openapi.yaml:3627-3636`).
- Idempotente: repetir devuelve el primer instante (`avisos.sql:119-123`, `COALESCE`). Capturado.
- No borra: libera cupo del periodo y el aviso sigue deduplicando, así que no se vuelve a emitir
  (`api/openapi.yaml:1035-1038`, `avisos.sql:81-87`). Capturado: `resumen.avisos_emitidos` bajó de 2 a 0.
- `id` ajeno o inexistente: 404. `id` malformado: 400 (`handler.go:453-457`).
- Si el aviso se creó "en el futuro" respecto del reloj responde **409** `DescarteAnteriorAlAlta`
  (antes 500, bloque C1 de la sección 4). Ver el apartado de correcciones al inicio.

## 2. Cinco tipos: qué emite el backend

Todos los valores salen de código, no de la base. `accion`, `porque` y `titulo` se **derivan del tipo
al leer** y no se guardan (`avisos_servicio.go:408-433`, y `accion` en `:417`). La acción es un texto
visible fijo por tipo (`avisos/texto.go:32-38`), y `VerificarTarjeta` exige que coincida con ese mapa
(`texto.go:160`). Por eso hay exactamente **cinco** valores de `accion`, uno por tipo. El código estable
que la UI debe usar para decidir el destino es `accion_codigo` (sección 3).

| Campo | `pago_no_alcanza` | `caida_ingreso` | `cobro_nuevo` | `presupuesto_anticipado` | `categoria_sobre_media` |
| --- | --- | --- | --- | --- | --- |
| `accion` (texto literal) | `Ver los pagos que vencen` | `Ver mis ingresos` | `Ver el cobro y su próximo vencimiento` | `Ver los gastos de este mes` | `Ver los movimientos de esta categoría` |
| `prioridad` | 1 | 2 | 3 | 4 | 5 |
| `clave` | `pago-proximo` | `ingreso-mensual` | `cobro-nuevo` | `gasto-total` | código de la categoría (por ejemplo `transporte`) |
| `transaccion_id` | `null` | `null` | `null` | `null` | `null` |
| `monto_centavos` | **saldo** del usuario | ingreso del mes evaluado | mediana de los cobros de la llave nueva | gasto proyectado del mes | gasto proyectado de la categoría |
| `referencia_centavos` | suma de los próximos pagos recurrentes | mediana de los meses anteriores | **ausente** (la clave no viaja) | mediana de los meses completos | mediana de la categoría |
| `periodo` | fecha del próximo pago (`YYYY-MM-DD`) | día 1 del mes anterior | fecha del primer pago de la llave | día 1 del mes en curso | día 1 del mes en curso |
| `severidad_centavos` | `abs(referencia - monto)` | `abs(monto - referencia)` | igual a `monto` | `abs(monto - referencia)` | `abs(monto - referencia)` |
| `porque` | `Los pagos recurrentes de la ventana suman más que tu saldo.` | `El ingreso del último mes completo quedó por debajo de lo habitual.` | `Apareció una llave recurrente que no estaba en tu historial.` | `El gasto acumulado del mes supera lo habitual de tus meses completos.` | `El acumulado de esta categoría supera lo habitual de tus meses completos.` |
| `titulo` | `Tus próximos pagos suman más que tu saldo` | `Tu ingreso bajó` | `Apareció un cobro nuevo` | `Tu gasto va por encima de lo habitual` | `Gastas más en <Etiqueta>` |

Citas de la tabla:

- Acciones y porqués: `avisos/texto.go:32-46`. Títulos: `texto.go:271-287`.
- Prioridad: `avisos/orden.go:11-25`. Severidad: `orden.go:104-125` (el valor guardado sale de
  `avisos.SeveridadDe`, `avisos_servicio.go:298`).
- Claves, montos y referencias: `avisos/detectores.go:363-410` (`HechoDe`).
- Periodo: `avisos/texto.go:143-149` y `detectores.go:393-396`.
- `transaccion_id` siempre nulo: `guardarAviso` nunca pasa `TransaccionID` al `INSERT`
  (`avisos_servicio.go:289`) aunque la columna y el mapeo existen (`:428-431`). Capturado nulo en
  los cuatro tipos que produce la demo. La clave JSON **siempre viaja** (`api/openapi.yaml:3562`,
  `"transaccion_id": null`).
- `referencia_centavos` ausente en `cobro_nuevo`: `Referencia` no se llena en ese hecho
  (`detectores.go:384-388`) y el campo es `omitempty` (`openapi.gen.go`, `InteligenciaAviso`).
  Capturado: la clave no aparece en el JSON. Es opcional en el esquema, no `null`.
- `pago_no_alcanza` **sí** trae referencia (la suma de pagos, `detectores.go:374-376`). Esto contradice el
  comentario de la migración (`bd/migraciones/0033_add_montos_al_aviso.sql:5-6`) y la descripción del
  esquema (`api/openapi.yaml:3556-3559`), que dicen que ese tipo no tiene línea base. Capturado con
  `referencia_centavos: "13990000"`.
- El significado de `monto_centavos` cambia por tipo. En `pago_no_alcanza` es el **saldo**, no una
  proyección (capturado: monto `970000`, texto "Tu saldo es $9.700"). La UI no debe rotularlo igual
  para los cinco tipos.
- La **severidad no es una etiqueta** (baja, media, alta): es un entero en centavos que solo ordena
  dentro de una prioridad. Si la UI quiere un nivel visual, sale de `prioridad`, no de la severidad.
- `categoria_sobre_media`: un solo aviso por evaluación, el de la categoría con mayor cociente
  (`detectores.go:102-160`). La etiqueta del título viene de `avisos/texto.go:15-30` y la `clave` es
  el código del catálogo (`GET /v1/inteligencia/categorias`, `api/openapi.yaml:1069`).
- `cobro_nuevo`, `pago_no_alcanza`: la identidad del comercio o destino (`Detalle`) **no se guarda ni
  se devuelve** (`detectores.go:374-388` la pone en el `Hecho`, el `INSERT` no la persiste). La UI no
  puede mostrar "ENEL" ni enlazar a una serie concreta desde el aviso.

## 3. Código estable de la acción: `accion_codigo`

Se implementó la opción B de la propuesta original: un campo **aditivo** `accion_codigo` con enum en
snake_case, uno por tipo de aviso. `accion` **no cambió**: sigue siendo `type: string` libre, sigue
siendo obligatorio y sigue trayendo la etiqueta visible de siempre (`avisos/texto.go:32-38`). La
diferencia es de uso: `accion` es copy para mostrar y puede cambiar de redacción, `accion_codigo` es
la llave para decidir el destino y no cambia aunque cambie la etiqueta.

- Definido en `api/openapi.yaml` como el esquema `InteligenciaAvisoAccionCodigo` (enum de cinco
  valores) y referenciado desde `InteligenciaAviso.accion_codigo`. El tipo Go generado es
  `api.InteligenciaAvisoAccionCodigo` (`internal/platform/api/openapi.gen.go`).
- **No es obligatorio** en el esquema (`required` no lo incluye): un cliente generado contra el
  contrato anterior sigue válido y un cliente nuevo debe tratarlo como opcional. El servidor lo envía
  siempre que el tipo del aviso es uno de los cinco.
- Se **deriva del tipo al leer**, igual que `accion`: `avisos.AccionCodigoDe(tipo)`
  (`avisos/texto.go`), asignado en `avisoDeFila` (`avisos_servicio.go`) y copiado a la respuesta en
  `respuestaDeAviso` (`handler.go`). No hay columna ni migración: no se guarda.
- Un cliente debe tolerar un valor que no conozca (por ejemplo, mostrar el aviso sin botón de acción),
  porque un sexto tipo de aviso traería un sexto código.
- Respuestas afectadas: las mismas que ya devuelven `InteligenciaAviso` (bandeja y detalle de avisos).
  Ninguna ruta cambió de comportamiento.

### Enum final y destino en la app

| `tipo` | `accion_codigo` | `accion` (etiqueta, sin cambios) | Destino en la app | Endpoint que lo alimenta hoy | Límite |
| --- | --- | --- | --- | --- | --- |
| `pago_no_alcanza` | `revisar_pagos` | Ver los pagos que vencen | Pantalla de pagos recurrentes próximos | `GET /v1/inteligencia/series` (`docs/recurrencia-consultable.md`) | El aviso no trae la identidad, así que no se abre la serie concreta |
| `caida_ingreso` | `revisar_ingreso` | Ver mis ingresos | Movimientos filtrados a ingresos | `GET /v1/inteligencia/movimientos` | No filtra por dirección en el servidor: el filtro es local con `direccion` |
| `cobro_nuevo` | `revisar_cobro` | Ver el cobro y su próximo vencimiento | Pantalla de pagos recurrentes, resaltando el cobro nuevo | `GET /v1/inteligencia/series` | Igual que `revisar_pagos`: sin identidad ni `transaccion_id` |
| `presupuesto_anticipado` | `revisar_presupuesto` | Ver los gastos de este mes | Resumen de gastos del mes en curso | `GET /v1/inteligencia/resumen?mes=` | Ninguno |
| `categoria_sobre_media` | `revisar_categoria` | Ver los movimientos de esta categoría | Movimientos de la categoría del aviso | `GET /v1/inteligencia/movimientos` | No hay filtro por categoría en el servidor: el filtro es local con `clave` y `categoria` |

Notas de diseño:

- Los códigos describen el **destino**, no el verbo de la etiqueta. Por eso `pago_no_alcanza` es
  `revisar_pagos` y no `recargar`: la etiqueta que muestra la app es "Ver los pagos que vencen" y no
  existe en la API un flujo de recarga al que enlazar desde el aviso.
- Los cinco códigos son uno por tipo y la relación es 1:1. Si la UI ya decide por `tipo`, puede seguir
  haciéndolo: `accion_codigo` existe para quien prefiera una llave de acción con nombre propio que no
  dependa del texto.
- Dos tipos pueden terminar en la misma pantalla (`revisar_pagos` y `revisar_cobro` van a series), pero
  conservan códigos distintos para que la UI pueda cambiar el énfasis sin tocar el backend.
- Las pruebas que protegen el contrato: `avisos/accion_codigo_test.go` (tabla escrita a mano, snake_case,
  unicidad, etiqueta intacta), `aviso_accion_codigo_test.go` (el YAML declara el mismo enum que Go,
  el campo es opcional y `accion` sigue obligatoria y libre, la respuesta de los cinco tipos trae
  ambos campos) y la aserción de `TestAvisosHandoffHTTPReal` (HTTP real contra Postgres).

## 4. Ejemplos reales del escenario de demo (CF-A2-03)

### Procedencia

Los bloques A, B y C están **capturados**, no derivados, y son de **antes** de la corrección de los
defectos 1 y 3 (se conservan como evidencia: con el código actual la bandeja por defecto del bloque A1
ya devuelve también `pago_no_alcanza` y el descarte del bloque C1 responde 409). Condiciones:

- API compilada desde `7250970` con árbol limpio (`go build`, sin Docker build), Postgres 16.15 y Redis
  7.4.1 efímeros en contenedores `vera-contrato-avisos-pg` y `vera-contrato-avisos-redis`, borrados al
  terminar. Respuestas tomadas con `curl` sobre `http://127.0.0.1:8080`, con un usuario de demo creado
  por `cmd/login-demo` y sesión obtenida por `POST /v1/auth/login`. Los tokens y las credenciales no
  se incluyen en este documento.
- Semilla: `cmd/semilla-escenario` con `DEMO_FECHA=2026-11-19` y `AVISOS_CUPO=5`
  (`docs/escenario-demo.md`). Resultado de la semilla: 52 asientos, 7 series, cuatro avisos
  (`caida_ingreso`, `cobro_nuevo`, `pago_no_alcanza`, `presupuesto_anticipado`).
- El reloj real era 2026-11-13 y el simulado de la semilla 2026-11-19, por eso `creado_en` aparece en
  el futuro (`2026-11-19T12:00:00-05:00`).
- La API corrió con `AVISOS_ACTIVO=false` y el cupo por defecto, **3** (`avisos/config.go:25`,
  `cargas/inteligencia/entorno.sh` no fija `AVISOS_CUPO`). Por eso `cupo_avisos` sale 3, que **no** es
  el cupo de la demo (5, `infra/servidor/banderas.tsv:19`).
- Salida reindentada para leerla. El servidor la entrega compacta.
- **Ajuste manual, bloques A y C1 antes, B después**: tras el fallo C1 se ejecutó en la base efímera
  `UPDATE inteligencia.aviso SET creado_en='2026-11-12T12:00:00-05:00'` sobre los cuatro avisos del
  usuario de demo para poder descartar. Todo lo marcado B y C2 se capturó con ese ajuste.

El tipo `categoria_sobre_media` **no lo produce la semilla**. Su ejemplo está al final y es
**derivado, no capturado**.

### A. Lecturas antes del ajuste (A1 a A5)

A1. `GET /v1/inteligencia/avisos` (bandeja por defecto): una sola tarjeta.

```json
{
  "success": true,
  "data": [
    {
      "accion": "Ver los gastos de este mes",
      "clave": "gasto-total",
      "creado_en": "2026-11-19T12:00:00-05:00",
      "id": "2c6242c9-f963-40e2-9f92-0a2050ca9f87",
      "monto_centavos": "19590000",
      "periodo": "2026-11-01",
      "porque": "El gasto acumulado del mes supera lo habitual de tus meses completos.",
      "prioridad": 4,
      "referencia_centavos": "18990000",
      "severidad_centavos": "600000",
      "texto": "En noviembre de 2026 llevas $195.900 en gastos. Lo habitual en tus meses completos es $189.900.",
      "tipo": "presupuesto_anticipado",
      "titulo": "Tu gasto va por encima de lo habitual",
      "transaccion_id": null
    }
  ],
  "error": null,
  "meta": null
}
```

A2. `GET /v1/inteligencia/avisos?mes=2026-11`: dos tarjetas. Aparece `pago_no_alcanza`, que A1 oculta.

```json
{
  "success": true,
  "data": [
    {
      "accion": "Ver los pagos que vencen",
      "clave": "pago-proximo",
      "creado_en": "2026-11-19T12:00:00-05:00",
      "id": "255c555d-1f84-422d-b55b-b30d6550f928",
      "monto_centavos": "970000",
      "periodo": "2026-11-20",
      "porque": "Los pagos recurrentes de la ventana suman más que tu saldo.",
      "prioridad": 1,
      "referencia_centavos": "13990000",
      "severidad_centavos": "13020000",
      "texto": "Tu saldo es $9.700 y los pagos recurrentes que vencen desde 2026-11-20 suman $139.900.",
      "tipo": "pago_no_alcanza",
      "titulo": "Tus próximos pagos suman más que tu saldo",
      "transaccion_id": null
    },
    {
      "accion": "Ver los gastos de este mes",
      "clave": "gasto-total",
      "creado_en": "2026-11-19T12:00:00-05:00",
      "id": "2c6242c9-f963-40e2-9f92-0a2050ca9f87",
      "monto_centavos": "19590000",
      "periodo": "2026-11-01",
      "porque": "El gasto acumulado del mes supera lo habitual de tus meses completos.",
      "prioridad": 4,
      "referencia_centavos": "18990000",
      "severidad_centavos": "600000",
      "texto": "En noviembre de 2026 llevas $195.900 en gastos. Lo habitual en tus meses completos es $189.900.",
      "tipo": "presupuesto_anticipado",
      "titulo": "Tu gasto va por encima de lo habitual",
      "transaccion_id": null
    }
  ],
  "error": null,
  "meta": null
}
```

A3. `GET /v1/inteligencia/resumen` (solo los campos de avisos): `"avisos_emitidos": 2`, `"cupo_avisos": 3`,
`"mes": "2026-11"`. Dos avisos contados y la bandeja por defecto mostró uno.

A4. `GET /v1/inteligencia/avisos?mes=2026-10`: mes anterior, sin `referencia_centavos` en `cobro_nuevo`.

```json
{
  "success": true,
  "data": [
    {
      "accion": "Ver mis ingresos",
      "clave": "ingreso-mensual",
      "creado_en": "2026-11-19T12:00:00-05:00",
      "id": "409484e1-54be-4673-badf-a770df522fb1",
      "monto_centavos": "13800000",
      "periodo": "2026-10-01",
      "porque": "El ingreso del último mes completo quedó por debajo de lo habitual.",
      "prioridad": 2,
      "referencia_centavos": "20000000",
      "severidad_centavos": "6200000",
      "texto": "El ingreso de octubre de 2026 fue $138.000. Lo habitual en tus meses anteriores es $200.000.",
      "tipo": "caida_ingreso",
      "titulo": "Tu ingreso bajó",
      "transaccion_id": null
    },
    {
      "accion": "Ver el cobro y su próximo vencimiento",
      "clave": "cobro-nuevo",
      "creado_en": "2026-11-19T12:00:00-05:00",
      "id": "527d4bd4-aa65-4305-879e-762e17ead8b0",
      "monto_centavos": "2490000",
      "periodo": "2026-10-28",
      "porque": "Apareció una llave recurrente que no estaba en tu historial.",
      "prioridad": 3,
      "severidad_centavos": "2490000",
      "texto": "Apareció un cobro de $24.900 el 2026-10-28 y ya se repite o está declarado como recurrente.",
      "tipo": "cobro_nuevo",
      "titulo": "Apareció un cobro nuevo",
      "transaccion_id": null
    }
  ],
  "error": null,
  "meta": null
}
```

A5. Otras lecturas y errores capturados:

- `GET /v1/inteligencia/avisos?mes=2026-09` responde `{"success": true, "data": [], "error": null, "meta": null}`.
- `GET /v1/inteligencia/avisos/255c555d-1f84-422d-b55b-b30d6550f928` devuelve la misma tarjeta de A2
  dentro de `data` como objeto (no arreglo).
- `GET /v1/inteligencia/avisos/00000000-0000-4000-8000-000000000000` responde 404:
  `{"success": false, "data": null, "error": {"code": "NotFound", "message": "Notice not found."}, "meta": null}`.
- Validaciones, todas 400 con `error.code: "ValidationError"`, `message: "Request validation failed."` y
  `details: [{"field": ..., "rule": ...}]`: `id`/`uuid`, `mes`/`format`, `mes`/`excluded_with`,
  `limite`/`range`, `estado`/`oneof`, `cursor`/`format`.

### C1. Descarte fallido por reloj simulado (antes del ajuste)

`POST /v1/inteligencia/avisos/2c6242c9-f963-40e2-9f92-0a2050ca9f87/descartar` respondió **500**:

```json
{"success": false, "data": null, "error": {"code": "InternalError", "message": "Something went wrong."}, "meta": null}
```

Registro de la API: `query DescartarAviso: ERROR: new row for relation "aviso" violates check
constraint "aviso_descarte_no_anterior_al_alta" (SQLSTATE 23514)`. Ver hallazgos.

### B. Descarte y lecturas posteriores (con el ajuste de `creado_en`)

B1. `POST .../descartar` sobre `presupuesto_anticipado`. La segunda llamada devuelve el mismo
`descartado_en`:

```json
{
  "success": true,
  "data": {
    "descartado_en": "2026-11-13T12:08:53.719287-05:00",
    "id": "2c6242c9-f963-40e2-9f92-0a2050ca9f87"
  },
  "error": null,
  "meta": null
}
```

B2. Después de descartar los tres avisos de noviembre y octubre que aplican (`presupuesto_anticipado`,
`pago_no_alcanza`, `cobro_nuevo`):

| Consulta | Resultado capturado |
| --- | --- |
| `GET /v1/inteligencia/avisos` | `[]` |
| `GET /v1/inteligencia/avisos?estado=descartados` | solo `presupuesto_anticipado` (periodo `2026-11-01`) |
| `GET /v1/inteligencia/avisos?mes=2026-11` | `[]` |
| `GET /v1/inteligencia/avisos?mes=2026-10` | solo `caida_ingreso` |
| `GET /v1/inteligencia/avisos/{id del cobro_nuevo}` | 200 con la tarjeta completa, sin marca de descartado |
| `GET /v1/inteligencia/resumen` (noviembre) | `avisos_emitidos: 0` |
| `GET /v1/inteligencia/resumen?mes=2026-10` | `avisos_emitidos: 1` |

Con el código de esa captura, el `pago_no_alcanza` y el `cobro_nuevo` descartados **no aparecían en
ninguna lista**: ni en `estado=descartados` (periodo distinto de día 1) ni en `?mes=` (excluye
descartados). Hoy `estado=descartados` los lista si su periodo cae en el mes en curso.

### Ejemplo derivado (no capturado): `categoria_sobre_media`

Derivado de `internal/inteligencia/avisos/testdata/vectores-avisos.json` (caso
`categoria_sobre_media-categoria_sobre_media`) y de `avisos/escenarios_test.go:170-173`. Son los campos
que produce la lógica pura: `id` y `creado_en` los asigna el servicio al guardar, así que se muestran
como marcador. `transaccion_id` es `null` por `guardarAviso` (no capturado para este tipo).

```json
{
  "accion": "Ver los movimientos de esta categoría",
  "clave": "transporte",
  "creado_en": "<lo asigna el servicio>",
  "id": "<uuid asignado al guardar>",
  "monto_centavos": "95000000",
  "periodo": "2026-07-01",
  "porque": "El acumulado de esta categoría supera lo habitual de tus meses completos.",
  "prioridad": 5,
  "referencia_centavos": "40000000",
  "severidad_centavos": "55000000",
  "texto": "En julio de 2026 llevas $950.000 en Transporte. Lo habitual en esa categoría es $400.000.",
  "tipo": "categoria_sobre_media",
  "titulo": "Gastas más en Transporte",
  "transaccion_id": null
}
```

(`monto_centavos` y `referencia_centavos` salen del vector, que los trae como enteros. La API los
entrega como cadena. `severidad_centavos` es `abs(monto - referencia)`, calculado aquí con
`orden.go:104-125`, no capturado.)

## 5. Estados de la UI: qué se alcanza hoy

| Estado | ¿Alcanzable con la API? | Cómo, y qué falta |
| --- | --- | --- |
| Vacío | Sí | `data: []` con 200 (capturado). Corregido: la bandeja por defecto usa el rango del mes y coincide con `?mes=` y con `resumen.avisos_emitidos` del mismo mes (probado en integración). Una bandeja vacía con avisos reales solo puede ser de otro mes (por ejemplo `caida_ingreso`, que vive en el mes anterior) |
| Cupo | Parcial | `resumen` da `cupo_avisos` y `avisos_emitidos` del mes (capturado: 3 y 2). Sirve para "n de N". **No** hay cuántas tarjetas recortó el cupo (el recuento existe solo para métricas, `avisos/orden.go:125-133`). El cupo cuenta por periodo, así que `caida_ingreso` cuenta contra el mes anterior (`orden.go:71-90`). Con la semilla de demo no se llega al tope (dos avisos por periodo, cupo 5), llenarlo exige varios hechos en un mismo mes |
| Descartado | Parcial | Descartar funciona y es idempotente (capturado). Listar descartados con `estado=descartados`, solo del mes en curso, ya incluye los de periodo con fecha de pago (`pago_no_alcanza`, `cobro_nuevo`). El detalle no trae marca. Los descartados de otros meses (incluido `caida_ingreso`, que vive en el mes anterior) no se pueden listar. Descartar un aviso "futuro" respecto del reloj responde 409 `DescarteAnteriorAlAlta`, ya no 500 |
| Mes anterior | Parcial | Solo `?mes=YYYY-MM`, solo vigentes, sin paginación y sin combinar con `limite`, `cursor` ni `estado` (capturado). Es el único camino a `caida_ingreso`. `resumen?mes=` da el cupo de ese mes. No hay descartados de meses anteriores |
| Vencido | **No** | No existe campo de vigencia, vencimiento ni estado en el esquema (`api/openapi.yaml:3554-3607`). Lo único cercano es el `periodo` de `pago_no_alcanza` (fecha del próximo pago), que la UI podría comparar con hoy: sería inferencia del cliente, **no verificado** que el emisor retire o actualice el aviso después de esa fecha |
| Más páginas | Parcial | Cursor en `X-Vera-Siguiente-Cursor` solo en modo bandeja (derivado, no capturado en vivo) |
| Sesión vencida o límite | Sí | 401 (`handler.go:293-300`) y 429 declarado en el OpenAPI para los tres endpoints (`api/openapi.yaml:1009`, `:1027`, `:1062`). 429 no capturado |

## 6. Hallazgos que la UI debe conocer

1. **Bandeja por defecto incompleta: corregido** (arriba). Las dos consultas paginadas usan el rango
   del mes. `caida_ingreso` sigue fuera de la bandeja del mes en curso por diseño: se lista con
   `?mes=` del mes anterior.
2. **Descarte con 500 en demo antes del día 19: corregido** (arriba). `DescartarAviso` estampa la hora
   real (`avisos_servicio.go`) y la migración exige `descartado_en >= creado_en`
   (`bd/migraciones/0026_add_descarte_a_aviso.sql:12-14`). La semilla de demo siempre fija el día 19
   (`cmd/semilla-escenario/main.go:168-172`), así que antes de esa fecha real cada descarte fallaba con
   un `InternalError`. Ahora responde 409 `DescarteAnteriorAlAlta`. En producción no pasa, porque el
   emisor usa la hora real. Para una demo que descarte avisos hay que sembrar con una `DEMO_FECHA` ya
   pasada o esperar al día 19: la UI debe tratar el 409 como "todavía no se puede descartar", no como
   fallo del servidor.
3. **Sin marca de descartado en la tarjeta** ni en el detalle. La UI solo sabe el estado por la lista
   de donde la sacó o por la respuesta del `POST`.
4. **Sin llave de navegación**: `transaccion_id` siempre es `null` y la identidad del destino no se
   guarda. Los textos de `accion` no traen parámetros.
5. **`monto_centavos` no significa lo mismo en los cinco tipos** y `referencia_centavos` falta en
   `cobro_nuevo`.
6. **Documento viejo en el repo**: `docs/handoff-avisos-pag-health-404-index.md:267-268` muestra
   `accion: "Revisa el saldo antes de la fecha del pago."` y un `transaccion_id` con valor. Es un
   diseño previo que no coincide con lo que el código emite hoy. No usarlo como referencia.
7. **`categoria_sobre_media` no está en la demo**: la semilla produce cuatro de los cinco tipos
   (`cmd/semilla-escenario/main.go:105-110`). Para un wireframe de ese tipo hay que usar el ejemplo
   derivado.

## No verificado

- Paginación con varias páginas y el encabezado de cursor, contra la API en vivo.
- Un 429 real y el comportamiento con un mes futuro en `?mes=`.
- Que `categoria_sobre_media` aparezca en la bandeja por defecto con datos reales en vivo (la prueba
  de integración lo cubre con filas sembradas, no con la semilla de la demo).
- Si el emisor diario duplica `pago_no_alcanza` cuando cambia la fecha del próximo pago entre
  corridas (la deduplicación es por tipo, clave y periodo, `bd/migraciones/0024_create_aviso_y_cursor.sql`).

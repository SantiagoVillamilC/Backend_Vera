# Handoff: detalle de avisos, paginación, health de modelos, 404 e índice por periodo

## 0. Encabezado

| Campo | Valor |
| --- | --- |
| Rama | `feat/avisos-pag-health-404-index` |
| Base | `origin/dev` en `cfbf7b6` |
| Dueño | Santiago, backend-app |
| Fecha | 2026-10-11 |
| Módulo | `github.com/SantiagoVillamilC/Backend_Centinela` (`go.mod:1`) |

Alcance: cinco tareas, nada de IA, nada de ledger. El trabajo es aditivo sobre lo que ya existe en
`dev`. Las cinco palabras de la rama son las cinco tareas: `avisos`, `pag`, `health`, `404` e `index`.
Si algo de lo que sigue no se puede verificar en el worktree, no lo implementes y pregúntalo.

Todo lo que este documento afirma sobre el código actual se verificó en `cfbf7b6` con `git show` y
`rg`. Las líneas son de esa revisión. Si la base se mueve antes de empezar, reverifica.

---

## 1. Qué existe hoy

### 1.1 Endpoints

| Método y ruta | Handler | Línea | Contrato |
| --- | --- | --- | --- |
| `GET /v1/inteligencia/avisos` | `ListarAvisosInteligencia` | `internal/inteligencia/handler.go:303` | `api/openapi.yaml:942` |
| `POST /v1/inteligencia/avisos/{id}/descartar` | `DescartarAvisoInteligencia` | `internal/inteligencia/handler.go:331` | `api/openapi.yaml:977` |
| `GET /healthz` | `HealthResource.Live` | `internal/platform/httpserver/health.go:26` | fuera de OpenAPI |
| `GET /readyz` | `HealthResource.Ready` | `internal/platform/httpserver/health.go:32` | fuera de OpenAPI |

No existe detalle de aviso. No existe `GET /readyz/modelos`. No hay paginación: `AvisosDelUsuario`
devuelve todas las vigentes del mes sin límite ni cursor (`internal/inteligencia/avisos_servicio.go:300`).

Listado y descarte viven en el grupo autenticado montado con `api.HandlerWithOptions`
(`internal/platform/httpserver/router.go:143`), detrás de `RequireAuth`. La ausencia de token ya sale
como 401 sin tocar el handler: `usuarioDeLaPeticion` (`handler.go:228`) responde `Unauthorized` cuando
el contexto no trae identidad.

`/healthz` y `/readyz` se registran a mano en el grupo público (`router.go:134-135`) con
`publicTimeout` de 15 s. El rate limit global los excluye por ruta exacta: la lista está en
`router.go:114` y el middleware compara `r.URL.Path` contra un mapa
(`internal/platform/httpserver/middleware.go:213-230`). La comparación es exacta, no por prefijo.

### 1.2 Consultas sqlc

Todas en `bd/consultas-inteligencia/avisos.sql`, generadas a
`internal/inteligencia/internal/repositorio` según la lista de `sqlc.yaml`.

| Consulta | Línea | Tipo | Qué hace |
| --- | --- | --- | --- |
| `AvisosDelUsuarioEnVentana` | 61 | `:many` | Lee `tipo, clave, periodo` para deduplicar |
| `AvisosVigentesDelMes` | 68 | `:many` | Bandeja, orden en la línea 75 |
| `ContarAvisosEmitidosEnElMes` | 77 | `:one` | Cuenta emitidos del mes para el cupo |
| `EmitirAviso` | 84 | `:one` | Inserta con `ON CONFLICT ... DO NOTHING` |
| `AvisoPorIdentidad` | 104 | `:one` | Lee el aviso ya emitido tras el conflicto |
| `DescartarAviso` | 112 | `:one` | Marca `descartado_en` con `COALESCE` (114) |
| `UsuariosConHistorial` | 119 | `:many` | Pagina usuarios para el emisor |

No hay consulta de aviso por identificador. `AvisosVigentesDelMes` filtra `descartado_en IS NULL`
(línea 72) y la ventana por `creado_en` (73-74), no por `periodo`. Ese detalle importa para la tarea 5.
`DescartarAviso` es idempotente por construcción: `COALESCE(descartado_en, sqlc.arg('descartado_en'))`
conserva el primer instante y devuelve `RETURNING id, descartado_en`.

### 1.3 Servicios y handlers

| Pieza | Línea | Nota |
| --- | --- | --- |
| `ErrAvisoNoEncontrado` | `avisos_servicio.go:16-18` | Un error para ajeno e inexistente, anti-fuga |
| `AvisoEmitido` | `avisos_servicio.go:31-45` | Estructura de dominio de la tarjeta |
| `DescarteDeAviso` | `avisos_servicio.go:47-51` | `AvisoID` y `DescartadoEn` |
| `estadoDeAvisos` | `avisos_servicio.go:232` | Deduplicación y cupo del emisor |
| `AvisosDelUsuario` | `avisos_servicio.go:299-340` | Mapeo de fila inline en 318-338 |
| `DescartarAviso` | `avisos_servicio.go:342-361` | `pgx.ErrNoRows` a `ErrAvisoNoEncontrado` |
| `ServicioDeLectura` | `handler.go:21-28` | Interfaz que el handler necesita |
| `respuestaDeAviso` | `handler.go:357` | Dominio a `api.InteligenciaAviso` |
| `InteligenciaHandler` | `servidor_v1.go:86-92` | Interfaz que el router consume |
| `sinImplementar` | `servidor_v1.go:186` | 501 en envelope si la dependencia es nula |

El mapeo de fila a `AvisoEmitido` está inline en `avisos_servicio.go:318-338`. No hay función
reutilizable, así que extraerla es parte de la tarea 1.

El descarte ya distingue ajeno de inexistente por construcción: el `UPDATE` lleva
`WHERE id = ... AND usuario_id = ...` (`avisos.sql:115-116`), así que un aviso de otro usuario no
produce fila y cae en el mismo `ErrAvisoNoEncontrado`. La tarea 4 no reescribe esa lógica.

### 1.4 Health

`HealthResource` guarda solo el `*pgxpool.Pool` (`health.go:15-17`) y se construye con
`NewHealthResource(pool)` (`health.go:20`). `Live` no toca dependencias (`health.go:26-28`). `Ready`
abre un `context.WithTimeout` de 2 s (`health.go:33`), hace `pool.Ping` (`health.go:36`) y responde 503
con `NotReady` si falla. El detalle del error solo va al log (`health.go:37`), nunca al cuerpo.

Redis no se comprueba a propósito y hay una prueba que lo fija: `TestReadyzNoDependeDeRedis`
(`internal/platform/httpserver/health_integration_test.go:16`) monta el router con un Redis apuntando
a `127.0.0.1:1` y exige 200 en `/readyz`.

Ningún `HealthResource` conoce URLs de modelos. `VERA_VOZ_URL` se lee en `cmd/api/main.go:67` con
default `http://127.0.0.1:8766` y solo se usa si `VERA_VOZ_TOKEN` viene definido. `VERA_STT_URL` y
`VERA_OLLAMA_URL` no existen hoy en el árbol: la tarea 3 introduce esos nombres como variables del
probe y no cablea clientes nuevos para ellas.

### 1.5 Contrato y migraciones

`openapi.yaml:942` declara el listado con `operationId: listarAvisosInteligencia` y un único parámetro
`mes`. `openapi.yaml:977` declara el descarte con `operationId: descartarAvisoInteligencia` y `id` como
texto. Schemas: `InteligenciaAviso` (3340), `InteligenciaAvisosResponse` (3386),
`InteligenciaAvisoDescarte` (3404), `InteligenciaAvisoDescarteResponse` (3415). El error compartido es
`components/responses/Error` (2062) contra `ErrorResponse`.

`/healthz` y `/readyz` están fuera de `api/openapi.yaml` por decisión registrada en `AGENTS.md:165`.
`/readyz/modelos` hereda esa decisión.

El código generado sale de `go generate ./...` (directiva en `internal/platform/api/doc.go:3`,
generador fijado por la directiva `tool` de `go.mod:99`). El gate local exige `git diff --exit-code`
sobre `openapi.gen.go` (`AGENTS.md:170-171`).

Migraciones hasta `0027_indice_movimientos_por_cuenta_fecha.sql`. `inteligencia.aviso` se crea en
`0024` con `id uuid`, `usuario_id text`, `tipo`, `clave`, `periodo date`, `severidad_centavos bigint`,
`prioridad smallint`, `texto`, `texto_verificado bool`, `transaccion_id uuid null`, `creado_en
timestamptz`, la restricción `aviso_dedupe_unico` y el índice `aviso_por_usuario (usuario_id, periodo
DESC, prioridad, severidad_centavos DESC)`. `0026` agrega `descartado_en timestamptz`, el `CHECK
aviso_descarte_no_anterior_al_alta` y el índice parcial `aviso_vigente_por_usuario (usuario_id,
creado_en DESC) WHERE descartado_en IS NULL`.

La lista de esquema del bloque de inteligencia en `sqlc.yaml` no incluye `0027` porque solo crea un
índice. Por la misma razón `0028` tampoco necesita entrar.

### 1.6 Reloj y cupo hoy

`AvisosDelUsuario` y `DescartarAviso` ya reciben `ahora time.Time` (`avisos_servicio.go:300` y `:344`)
y el handler los alimenta con `h.reloj()` (`handler.go:313` y `:342`). El cupo mensual cuenta por
`creado_en` con `ContarAvisosEmitidosEnElMes` (`avisos.sql:77`) y se aplica con `Tope`
(`internal/inteligencia/avisos/orden.go:47`). Eso es lo que hay en `dev`.

A2-01 (cupo por periodo, descarte que libera cupo) vive en `feat/cierre-ia` y no está en esta base. No
asumas su comportamiento. El reloj ya inyectado es lo único que comparten.

---

## 2. Las cinco tareas

### 2.1 `avisos`: detalle por identificador

**Situación.** Un cliente que recibe una tarjeta no puede volver a pedirla sola. El listado devuelve
todas las del mes y no hay ruta para una tarjeta concreta.

**Lo que ya se tiene.** El mapeo de fila (`avisos_servicio.go:318-338`), el mapeo a contrato
(`handler.go:357`), `ErrAvisoNoEncontrado` (`:16-18`) y el patrón de UUID con `ValidationError`
(`handler.go:336-341`).

**Lo que se espera.** `GET /v1/inteligencia/avisos/{id}` con `operationId: detalleAvisoInteligencia`,
200 para aviso propio, 400 para UUID inválido, 401 sin token, 404 con el mismo cuerpo para ajeno e
inexistente, y 429, 500 y 504 del middleware.

**Cómo hacerlo.**

1. Consulta nueva en `avisos.sql`, después de `AvisosVigentesDelMes`:

```sql
-- name: AvisoPorID :one
SELECT id, tipo, clave, periodo, prioridad, severidad_centavos, texto, transaccion_id, creado_en
  FROM inteligencia.aviso
 WHERE id = sqlc.arg('id')::uuid
   AND usuario_id = sqlc.arg('usuario_id')::text;
```

El filtro por `usuario_id` va en el `WHERE`, no en Go. Un aviso ajeno no produce fila y comparte el
camino del inexistente, que es lo que impide distinguirlos.

2. Extrae el mapeo. `AvisosVigentesDelMesRow` y `AvisoPorIDRow` son structs distintos con los mismos
campos y Go no permite una función que reciba cualquiera de los dos. La forma que no duplica lógica:

```go
type filaDeAviso struct {
	ID                pgtype.UUID
	Tipo              string
	Clave             string
	Periodo           pgtype.Date
	Prioridad         int16
	SeveridadCentavos int64
	Texto             string
	TransaccionID     pgtype.UUID
	CreadoEn          pgtype.Timestamptz
}

func avisoDeFila(fila filaDeAviso) AvisoEmitido { /* cuerpo de avisos_servicio.go:318-338 */ }
```

Con dos adaptadores triviales, `filaDeAvisoDesdeVigente` y `filaDeAvisoDesdePorID`, y
`AvisosDelUsuario` pasa a usarlo. Duplicar las 21 líneas en `Aviso` es la alternativa, y acepta que las
dos copias se separen en el siguiente cambio de contrato. No la recomiendo.

3. Servicio en `avisos_servicio.go`:

```go
func (s *Servicio) Aviso(ctx context.Context, usuarioID string, avisoID uuid.UUID) (AvisoEmitido, error)
```

Traduce `pgx.ErrNoRows` a `ErrAvisoNoEncontrado` igual que `avisos_servicio.go:354-356`, envuelve el
resto con `fmt.Errorf("inteligencia: leer el aviso %s de %q: %w", ...)` y usa `enTransaccion` como el
resto del archivo.

4. Agrega `Aviso(...)` a `ServicioDeLectura` (`handler.go:20-26`) y al falso de `handler_test.go:72-84`.

5. Handler junto a `DescartarAvisoInteligencia`:

```go
func (h *Handler) DetalleAvisoInteligencia(w http.ResponseWriter, r *http.Request, id string)
```

Orden: `usuarioDeLaPeticion` (401), `uuid.Parse` (400 con `Field: "id"`, `Rule: "uuid"`), servicio,
`ErrAvisoNoEncontrado` (404), `api.WriteAPIError` para el resto, y
`api.WriteData(w, http.StatusOK, respuestaDeAviso(aviso), nil)`.

6. Router: firma nueva en `InteligenciaHandler` (`servidor_v1.go:86-92`) y wrapper con guarda de nulo y
`sinImplementar`, copiando `servidor_v1.go:167-173`. El resto lo hace `api.HandlerWithOptions`. No
registres la ruta a mano.

7. OpenAPI, antes de `/v1/inteligencia/avisos/{id}/descartar`:

```yaml
  /v1/inteligencia/avisos/{id}:
    get:
      operationId: detalleAvisoInteligencia
      summary: Devuelve una tarjeta de aviso propia por su identificador.
      tags: [inteligencia]
      parameters: [{ name: id, in: path, required: true, schema: { type: string } }]
      responses:
        "200": { description: Tarjeta de aviso, content: { application/json: { schema: { $ref: "#/components/schemas/InteligenciaAvisoDetalleResponse" } } } }
        "400": { $ref: "#/components/responses/Error" }
        "401": { $ref: "#/components/responses/Error" }
        "404": { $ref: "#/components/responses/Error" }
        "429": { $ref: "#/components/responses/Error" }
        "500": { $ref: "#/components/responses/Error" }
        "504": { $ref: "#/components/responses/Error" }
```

`InteligenciaAvisoDetalleResponse` es el envelope de cuatro claves con `data: $ref InteligenciaAviso`,
calcado de `InteligenciaAvisoDescarteResponse` (3415). `id` se declara `string` sin `format: uuid` a
propósito, para que un valor malformado llegue al handler y salga con `details`, igual que en el
descarte (`openapi.yaml:986-990`).

8. Regenera con `go generate ./...`.

**Ejemplo.**

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:8080/v1/inteligencia/avisos/6f1c2b3a-4d5e-4f60-8a71-2b3c4d5e6f70
```

```json
{
  "success": true,
  "data": {
    "id": "6f1c2b3a-4d5e-4f60-8a71-2b3c4d5e6f70",
    "tipo": "pago_no_alcanza",
    "clave": "pago-2026-07-arriendo",
    "periodo": "2026-07-15",
    "severidad_centavos": "4500000",
    "prioridad": 1,
    "titulo": "El pago de arriendo no alcanza",
    "texto": "El pago de arriendo de 45000.00 no alcanza con el saldo disponible.",
    "porque": "Se compara el monto del pago con el saldo del usuario al momento de la evaluacion.",
    "accion": "Revisa el saldo antes de la fecha del pago.",
    "transaccion_id": "0b9f7c1e-2a34-4c5d-9e8f-1a2b3c4d5e6f",
    "creado_en": "2026-07-15T15:00:00Z"
  },
  "error": null,
  "meta": null
}
```

```json
{ "success": false, "data": null,
  "error": { "code": "NotFound", "message": "Notice not found." }, "meta": null }
```

**Criterios de aceptación.**

1. `AvisoPorID` existe y filtra por `id` y `usuario_id`.
2. `Servicio.Aviso` devuelve `AvisoEmitido` para un aviso propio.
3. Devuelve `ErrAvisoNoEncontrado` para un aviso ajeno y para un UUID inexistente.
4. El mapeo de fila no está duplicado: `AvisosDelUsuario` y `Aviso` comparten función.
5. UUID malformado devuelve 400 con `code: ValidationError` y `details[0].field = "id"`.
6. Sin token devuelve 401 por la vía de `usuarioDeLaPeticion`.
7. Ajeno e inexistente devuelven 404 con cuerpo byte a byte idéntico.
8. `openapi.gen.go` contiene `DetalleAvisoInteligencia` y el contrato declara 200, 400, 401, 404, 429,
   500 y 504.
9. `go generate ./...` deja el árbol limpio.

**Impacto.** La app abre una tarjeta desde una notificación o un enlace sin pedir la bandeja completa.
En la demo permite mostrar el aviso concreto después de descartarlo, porque el detalle lo lee igual.

### 2.2 `pag`: límite, cursor y estado en el listado

**Situación.** El listado devuelve todo el mes de una vez, el contrato no tiene tope y no hay forma de
leer las descartadas, que es lo que la bandeja necesita para el historial.

**Lo que ya se tiene.** El orden estable `prioridad, severidad_centavos DESC, clave, id` ya está en
`avisos.sql:75`. El módulo tiene un patrón completo de cursor opaco: `CursorDeMovimiento`
(`internal/inteligencia/lectura.go:45-49`), `CodificarCursor` (`:52`), `DecodificarCursor` (`:58`),
`ErrCursorInvalido` (`:37`) y el handler que lo usa (`handler.go:70-102`). También
`limiteDeLaPagina` (`handler.go:237`), `api.MetaCursor` (`openapi.gen.go:2245`) y
`api.WriteDataConCursor` (`internal/platform/api/envelope.go:43`).

**Lo que se espera.** `limite` (1 a 50, defecto 20), `cursor` opaco y `estado`
(`vigentes` o `descartados`, defecto `vigentes`). El cuerpo sigue siendo el arreglo de tarjetas, sin
`meta` nuevo. El cursor de la página siguiente viaja en la cabecera `X-Vera-Siguiente-Cursor`. Cursor
o estado inválidos son 400.

**Cómo hacerlo.**

1. Cursor propio en un archivo nuevo `internal/inteligencia/avisos_pagina.go`:

```go
type CursorDeAviso struct {
	Prioridad         int
	SeveridadCentavos int64
	ID                uuid.UUID
	Clave             string
}

func CodificarCursorDeAviso(ancla CursorDeAviso) string
func DecodificarCursorDeAviso(texto string) (CursorDeAviso, error)
```

Formato `v1|prioridad|severidad_centavos|id|clave` con `base64.RawURLEncoding`, leído con
`strings.SplitN(crudo, "|", 5)`. El orden no es casual: `clave` es texto libre y puede traer `|`, así
que va al final y absorbe el resto del segmento. `id` va antes porque es un UUID sin separadores. Un
`Split` plano con cinco partes rompería con una clave que contenga el carácter. Cualquier fallo
devuelve `ErrCursorInvalido` envuelto, igual que `lectura.go:58-76`.

2. Dos consultas en `avisos.sql`, una por estado, con el mismo `ORDER BY`. Sin `OFFSET`.

```sql
-- name: AvisosVigentesPorPagina :many
SELECT id, tipo, clave, periodo, prioridad, severidad_centavos, texto, transaccion_id, creado_en
  FROM inteligencia.aviso
 WHERE usuario_id = sqlc.arg('usuario_id')::text
   AND descartado_en IS NULL
   AND (sqlc.narg('prioridad')::smallint IS NULL
        OR prioridad > sqlc.narg('prioridad')::smallint
        OR (prioridad = sqlc.narg('prioridad')::smallint
            AND severidad_centavos < sqlc.narg('severidad')::bigint)
        OR (prioridad = sqlc.narg('prioridad')::smallint
            AND severidad_centavos = sqlc.narg('severidad')::bigint
            AND clave > sqlc.narg('clave')::text)
        OR (prioridad = sqlc.narg('prioridad')::smallint
            AND severidad_centavos = sqlc.narg('severidad')::bigint
            AND clave = sqlc.narg('clave')::text
            AND id > sqlc.narg('id')::uuid))
 ORDER BY prioridad, severidad_centavos DESC, clave, id
 LIMIT sqlc.arg('limite')::int;
```

`AvisosDescartadosPorPagina` es la misma con `descartado_en IS NOT NULL`. El predicado del cursor se
repite en las dos a propósito: un `OR` que cambia de estado no deja al planificador usar un índice
parcial distinto por rama.

Desde Go se pasa `limite + 1`. La fila extra nunca se serializa: decide si hay página siguiente y
ahorra una consulta de conteo. El cursor se calcula con la última fila devuelta, no con la extra, o la
página siguiente se salta un aviso.

3. Servicio:

```go
type EstadoDeAvisos string

const (
	EstadoVigentes    EstadoDeAvisos = "vigentes"
	EstadoDescartados EstadoDeAvisos = "descartados"
)

type PaginaDeAvisos struct {
	Avisos    []AvisoEmitido
	Siguiente string
	HayMas    bool
}

func (s *Servicio) AvisosDelUsuarioPaginados(ctx context.Context, usuarioID string,
	estado EstadoDeAvisos, ancla *CursorDeAviso, limite int) (PaginaDeAvisos, error)
```

Valida el estado contra las dos constantes y devuelve error de dominio para el 400. No reutilices
`AvisosDelUsuario`: ese conserva la ventana por mes y lo usa solo el handler de avisos.

4. En el handler, mantén intactas las líneas 309-322 mientras puedas. Orden: identidad (401), `limite`
(400), `estado` (400), `cursor` (400), servicio, y al final:

```go
w.Header().Set("X-Vera-Siguiente-Cursor", pagina.Siguiente)
api.WriteData(w, http.StatusOK, respuesta, nil)
```

La cabecera se omite cuando no hay página siguiente. No uses `api.WriteDataConCursor`: metería `meta`
en el cuerpo y cambia la forma del contrato.

5. CORS. Agrega `"X-Vera-Siguiente-Cursor"` a `ExposedHeaders` (`router.go:111`) o un cliente en
navegador no puede leerla. Sin esto la paginación funciona en curl y falla en la web, que es el peor
bug de diagnosticar.

6. OpenAPI: tres parámetros en `openapi.yaml:947-958`, con el patrón de `limit` y `cursor` de
movimientos (`openapi.yaml:821-840`).

```yaml
        - { name: limite, in: query, required: false, schema: { type: integer, x-go-type: string },
            description: "Tamano de pagina, de 1 a 50. Por defecto 20. Fuera de rango es ValidationError." }
        - { name: cursor, in: query, required: false, schema: { type: string },
            description: "Cursor opaco de la pagina anterior, entregado en X-Vera-Siguiente-Cursor." }
        - { name: estado, in: query, required: false,
            schema: { type: string, enum: [vigentes, descartados], x-go-type: string },
            description: "vigentes por defecto. descartados lee las descartadas." }
```

La cabecera se documenta en la descripción del 200, no como `headers:` tipado. Declararla tipada hace
que oapi-codegen genere un struct de respuesta y cambie la firma de `ServerInterface` y de todos los
wrappers de `servidor_v1.go`. El valor para el cliente es el mismo y el costo es mucho menor.

7. Pruebas nuevas en `avisos_integration_test.go` y en `handler_test.go` con el falso.

**Ejemplo.**

```bash
curl -s -D - -H "Authorization: Bearer $TOKEN" \
  "http://127.0.0.1:8080/v1/inteligencia/avisos?limite=2&estado=vigentes"
```

```
HTTP/1.1 200 OK
X-Vera-Siguiente-Cursor: djF8MXw0NTAwMDAwfDZmMWMyYjNhLTRkNWUtNGY2MC04YTcxLTJiM2M0ZDVlNmY3MHxwYWdvLTIwMjYtMDctYXJy
```

```json
{
  "success": true,
  "data": [
    { "id": "6f1c2b3a-4d5e-4f60-8a71-2b3c4d5e6f70", "tipo": "pago_no_alcanza",
      "clave": "pago-2026-07-arriendo", "periodo": "2026-07-15",
      "severidad_centavos": "4500000", "prioridad": 1,
      "titulo": "El pago de arriendo no alcanza",
      "texto": "El pago de arriendo de 45000.00 no alcanza con el saldo disponible.",
      "porque": "Se compara el monto del pago con el saldo del usuario al momento de la evaluacion.",
      "accion": "Revisa el saldo antes de la fecha del pago.",
      "transaccion_id": "0b9f7c1e-2a34-4c5d-9e8f-1a2b3c4d5e6f",
      "creado_en": "2026-07-15T15:00:00Z" }
  ],
  "error": null,
  "meta": null
}
```

```json
{ "success": false, "data": null,
  "error": { "code": "ValidationError", "message": "Request validation failed.",
             "details": [{ "field": "cursor", "rule": "format" }] },
  "meta": null }
```

**Criterios de aceptación.**

1. `limite` ausente usa 20. `limite=1` y `limite=50` funcionan. `0`, `51` y `abc` responden 400.
2. `estado` ausente equivale a `vigentes`. `estado=descartados` devuelve solo descartadas. `otro`
   responde 400.
3. Un cursor del servidor devuelve la página siguiente sin repetir ni saltar avisos con la misma
   `prioridad` y `severidad_centavos`.
4. La última página no emite `X-Vera-Siguiente-Cursor`.
5. El cuerpo sigue siendo el envelope con `data` como arreglo y `meta` en `null`. No aparece
   `next_cursor`.
6. No hay `OFFSET` en ninguna de las dos consultas.
7. La cabecera está en `ExposedHeaders` del CORS.
8. `openapi.gen.go` regenerado contiene los tres parámetros en `ListarAvisosInteligenciaParams`.

**Impacto.** La bandeja crece sin tope y el historial de descartadas sale por la misma ruta. La demo
gana una vista de historial sin endpoint nuevo.

### 2.3 `health`: `/readyz/modelos` separado

**Situación.** `/readyz` solo comprueba Postgres. Si la voz, el STT u Ollama están caídos, el
orquestador no se entera y el asistente responde degradado sin que nadie lo note. Meterlos en `/readyz`
haría que un modelo caído drene toda la API, ledger incluido.

**Lo que ya se tiene.** El patrón de 2 s con detalle solo en log (`health.go:32-42`), el registro de las
dos sondas en el grupo público (`router.go:134-135`), la exclusión del rate limit por ruta exacta
(`router.go:114`) y `TestReadyzNoDependeDeRedis` (`health_integration_test.go:16`) como prueba de que
`/readyz` no debe crecer.

**Lo que se espera.** `GET /readyz/modelos` comprueba solo las dependencias configuradas entre
`VERA_VOZ_URL`, `VERA_STT_URL` y `VERA_OLLAMA_URL`, cada una con 2 s, en paralelo. 200 con el detalle
o 503 con las que fallan. Una URL ausente no es fallo: se reporta como `no configurada` y no cuenta
para el 503. `/readyz` no cambia.

**Cómo hacerlo.**

1. Archivo nuevo `internal/platform/httpserver/health_modelos.go`:

```go
type DependenciasDeModelos struct {
	Voz    string
	STT    string
	Ollama string
}

type EstadoDeModelo struct {
	Estado string `json:"estado"`
	Motivo string `json:"motivo,omitempty"`
}

func (rs HealthResource) Modelos(w http.ResponseWriter, r *http.Request)
```

`Modelos` lanza un `goroutine` por URL configurada con `context.WithTimeout(r.Context(), 2*time.Second)`
propio, espera con `sync.WaitGroup` y hace `GET` a la raíz de cada URL con `http.NewRequestWithContext`.
Cuenta como sano cualquier status menor a 500: un 404 en la raíz de Ollama no significa que el modelo
esté caído y tratarlo como fallo produce 503 falsos. Los fallos de red, los timeouts y los 5xx son
fallo. Un `http.Client` compartido con `Timeout` propio, no el global.

2. El recurso gana el campo y conserva la firma actual para no romper llamadas existentes:

```go
type HealthResource struct {
	pool    *pgxpool.Pool
	modelos DependenciasDeModelos
}

func NewHealthResource(pool *pgxpool.Pool, opciones ...OpcionDeSalud) HealthResource
func ConModelos(deps DependenciasDeModelos) OpcionDeSalud
```

3. Cableado. `NewRouter` y `NewRouterConVera` ya usan su variádico para `pseURL ...string`, así que no
hay lugar para otro. La vía con menos superficie es agregar el campo a `DependenciasVera`
(`router.go:55-68`), que ya es el paquete de dependencias del asistente y ya se construye en
`cmd/api/main.go:324`. Añade `Modelos DependenciasDeModelos` y pásalo a `NewHealthResource`
(`router.go:118`) con `ConModelos(vera.Modelos)`.

4. En `cmd/api/main.go:324`, construye el struct con `os.Getenv` directo y sin default:

```go
httpserver.DependenciasDeModelos{
	Voz:    os.Getenv("VERA_VOZ_URL"),
	STT:    os.Getenv("VERA_STT_URL"),
	Ollama: os.Getenv("VERA_OLLAMA_URL"),
}
```

Sin default a propósito. `VERA_VOZ_URL` tiene default en `main.go:67` porque ahí alimenta un cliente
real. En el probe, un default convierte una variable no configurada en una dependencia inventada y en
un 503 permanente en cualquier entorno sin voz local.

5. Router, en el grupo público:

```go
r.Get("/healthz", health.Live)
r.Get("/readyz", health.Ready)
r.Get("/readyz/modelos", health.Modelos)
```

Y agrega `"/readyz/modelos"` a la lista de `router.go:114`. La comparación es por ruta exacta
(`middleware.go:224`), así que `"/readyz"` no cubre la ruta nueva.

6. No toques `Ready`. `/readyz` sigue comprobando solo Postgres y no Redis, y
`TestReadyzNoDependeDeRedis` sigue pasando sin cambios.

7. No entra en `api/openapi.yaml`, por la misma decisión que deja fuera `/healthz` y `/readyz`
(`AGENTS.md:165`).

**Ejemplo.**

```bash
curl -s -i http://127.0.0.1:8080/readyz/modelos
```

```
HTTP/1.1 503 Service Unavailable
```

```json
{
  "success": false,
  "data": {
    "voz":    { "estado": "ok" },
    "stt":    { "estado": "no configurada" },
    "ollama": { "estado": "falla", "motivo": "context deadline exceeded" }
  },
  "error": { "code": "NotReady", "message": "One or more model dependencies are unavailable." },
  "meta": null
}
```

Con las tres sanas, mismo cuerpo con `success: true`, `error: null` y 200. Con ninguna configurada, 200
y tres entradas `no configurada`. El 503 lleva el detalle a propósito: la sonda es operativa y la
consume el orquestador. Eso no contradice a `Ready`, que mantiene el detalle en el log porque la suya
es una dependencia de datos con mensajes que podrían filtrar el DSN.

**Criterios de aceptación.**

1. `/readyz/modelos` existe, está en el grupo público y fuera del rate limit.
2. Con las tres variables ausentes responde 200 y tres entradas `no configurada`.
3. Con una URL que responde 500 responde 503 y nombra esa dependencia.
4. Con una URL que no resuelve responde 503 dentro de los 2 s.
5. Cada dependencia tiene su presupuesto: una colgada no retrasa el veredicto de las otras más allá de
   2 s en total.
6. `/readyz` no cambia y `TestReadyzNoDependeDeRedis` pasa.
7. La prueba usa `httptest.NewServer` y corre sin etiqueta `integration`.
8. `go vet ./...` limpio y el core no importa el paquete de modelos.

**Impacto.** El orquestador saca de rotación solo la parte del asistente sin tocar el ledger. En la
demo el modo degradado se muestra explícito en vez de fallar sin explicación.

### 2.4 `404`: misma respuesta para ajeno e inexistente

**Situación.** El descarte ya devuelve 404 con `NotFound` y `Notice not found.` para ajeno e
inexistente (`handler.go:343-346`) y el `UPDATE` filtra por `usuario_id` (`avisos.sql:115-116`). El descarte ajeno e inexistente ya tiene pruebas de integración
(`avisos_integration_test.go:341-346`). Falta el detalle nuevo, la comparación byte a byte del cuerpo
404 y la prueba de la ruta de detalle, que es lo que impide que una refactorización futura
reintroduzca la fuga con un 403.

**Lo que ya se tiene.** `ErrAvisoNoEncontrado` único (`:16-18`), el 404 del descarte con el cuerpo
correcto, el 400 con `details` de UUID (`handler.go:336-341`) y el `COALESCE` idempotente
(`avisos.sql:114`).

**Lo que se espera.**

| Caso | Detalle | Descarte |
| --- | --- | --- |
| UUID inválido | 400 `ValidationError`, `details[0].field = "id"` | 400 idéntico |
| Aviso propio | 200 con la tarjeta | 200 con `id` y `descartado_en` |
| Aviso ajeno | 404 `NotFound`, `Notice not found.` | 404 idéntico |
| UUID inexistente | 404 `NotFound`, `Notice not found.` | 404 idéntico |

El descarte repetido devuelve 200 con el mismo `descartado_en` de la primera vez.

**Cómo hacerlo.**

1. No agregues un error nuevo. Un `ErrAvisoAjeno` produciría un 403 y eso confirma la existencia del
   aviso, que es lo que la semántica actual evita.
2. No cambies el mensaje. El detalle tiene que usar el mismo literal `"Notice not found."`, no el
   `"Resource not found."` del 404 de chi (`router.go:169`). La prueba que compara cuerpos depende de
   eso.
3. Centraliza el 404 en un helper del paquete para que no se pueda desviar:

```go
func responderAvisoNoEncontrado(w http.ResponseWriter) {
	api.WriteError(w, http.StatusNotFound, "NotFound", "Notice not found.", nil)
}
```

4. Pruebas. Unitarias en `handler_test.go` con el falso: 400 por UUID inválido y 404 por
   `ErrAvisoNoEncontrado` en los dos handlers, comparando el cuerpo completo y no solo el status. De
   integración en `avisos_integration_test.go`: un aviso de `usuario_a` pedido y descartado por
   `usuario_b` devuelve 404, y el aviso de `usuario_a` sigue vigente en su bandeja. Ese segundo
   `assert` prueba que el 404 no fue un descarte silencioso. Y descarte repetido: dos llamadas
   seguidas devuelven el mismo `descartado_en`, el de la primera.
5. Si el detalle y el descarte devuelven cuerpos distintos, es un bug. La misma prueba corre contra las
   dos rutas con el mismo `usuarioID`.

**Ejemplo.** Mismo cuerpo para ajeno y para inexistente, en las dos rutas:

```json
{ "success": false, "data": null,
  "error": { "code": "NotFound", "message": "Notice not found." }, "meta": null }
```

Descarte repetido:

```bash
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:8080/v1/inteligencia/avisos/$ID/descartar
```

```json
{ "success": true,
  "data": { "id": "6f1c2b3a-4d5e-4f60-8a71-2b3c4d5e6f70", "descartado_en": "2026-07-15T15:00:00Z" },
  "error": null, "meta": null }
```

Las dos respuestas llevan el mismo `descartado_en`.

**Criterios de aceptación.**

1. Los cuatro casos están cubiertos por prueba en el detalle y en el descarte.
2. Ajeno e inexistente devuelven el cuerpo 404 idéntico, comparado como string.
3. El UUID inválido devuelve 400 con `details[0].field = "id"` y `Rule: "uuid"`.
4. El descarte repetido devuelve el primer `descartado_en` y no lo reescribe.
5. El 404 de un aviso ajeno no modifica la fila: la bandeja del dueño lo sigue listando.
6. No existe ningún 403 en el camino de avisos.

**Impacto.** Cierra una clase de bug de privacidad y deja la idempotencia del descarte como contrato
probado, que es lo que necesita el botón cuando el usuario toca dos veces.

### 2.5 `index`: migración 0028 con dos índices parciales

**Situación.** Hay dos índices: `aviso_por_usuario (usuario_id, periodo DESC, prioridad,
severidad_centavos DESC)` de `0024` y `aviso_vigente_por_usuario (usuario_id, creado_en DESC) WHERE
descartado_en IS NULL` de `0026`. Ninguno cubre el `ORDER BY` completo de la página nueva, que termina
en `clave, id`, y no hay ninguno para las descartadas.

**Lo que ya se tiene.** Eso, más el hecho de que `sqlc.yaml` no lista los archivos que solo crean
índices (`0027` tampoco está), así que la migración nueva no requiere tocar el generador.

**Lo que se espera.** `bd/migraciones/0028_add_indice_avisos_por_periodo.sql` con los dos índices y un
`Down` que los borra.

**Cómo hacerlo.**

```sql
-- Índices de la bandeja y del historial de avisos.
--
-- La bandeja pagina por (prioridad, severidad_centavos DESC, clave, id) dentro de un usuario y un
-- periodo. El índice de 0024 llega hasta severidad_centavos y no cubre el desempate por clave e id.
-- El de 0026 ordena por creado_en, que no es el orden de la página. El historial no tenía índice.

-- +goose Up
CREATE INDEX aviso_vigente_por_usuario_periodo
    ON inteligencia.aviso (usuario_id, periodo, prioridad, severidad_centavos DESC, clave, id)
    WHERE descartado_en IS NULL;

CREATE INDEX aviso_descartado_por_usuario_periodo
    ON inteligencia.aviso (usuario_id, periodo, descartado_en DESC)
    WHERE descartado_en IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS inteligencia.aviso_descartado_por_usuario_periodo;
DROP INDEX IF EXISTS inteligencia.aviso_vigente_por_usuario_periodo;
```

El `Down` borra en orden inverso y con `IF EXISTS`, como `0026`. Los índices son parciales para que el
de vigentes no cargue las descartadas y el de descartadas no cargue las vigentes. Esa es la razón de
tener dos y no uno con `descartado_en` en la clave.

**Qué consulta usa cada índice.**

- `aviso_vigente_por_usuario_periodo` sirve a la rama vigentes de la página
  (`AvisosVigentesPorPagina`) y a la bandeja del mes. La igualdad por `usuario_id`, el filtro parcial
  y el orden por `prioridad, severidad_centavos DESC, clave, id` coinciden con el índice cuando el
  `periodo` está fijado, y el plan es un `Index Scan` sin `Sort`.
- `aviso_descartado_por_usuario_periodo` sirve al historial de descartadas cuando se pide por periodo
  y a cualquier lectura que ordene por `descartado_en DESC`.

Hay una limitación que conviene dejar escrita. `periodo` va entre `usuario_id` y `prioridad`, y la
página de la tarea 2 no recibe `periodo`. Sin ese filtro, Postgres no puede recorrer el índice en el
orden de la consulta y resuelve con un `Bitmap Index Scan` por `usuario_id` más un `Sort` del conjunto
del usuario. El conjunto está acotado por el cupo mensual, así que es aceptable, pero no es lo que el
índice promete. Las salidas limpias son agregar `periodo` como filtro opcional a la página o mover
`periodo` al final del índice. Las dos cambian algo fuera de las cinco tareas, así que quedan anotadas
y no se implementan acá. Si A2-01 agrega `periodo` al listado, la limitación se resuelve sola.

**Criterios de aceptación.**

1. La migración se llama `0028_add_indice_avisos_por_periodo.sql` y usa `-- +goose Up` y `-- +goose
   Down`.
2. Crea `aviso_vigente_por_usuario_periodo` con las seis columnas y `WHERE descartado_en IS NULL`.
3. Crea `aviso_descartado_por_usuario_periodo` con `(usuario_id, periodo, descartado_en DESC)` y
   `WHERE descartado_en IS NOT NULL`.
4. El `Down` borra los dos con `IF EXISTS` y no toca los índices de `0024` ni de `0026`.
5. `goose up` y `goose down` corren limpios sobre una base migrada desde cero.
6. `EXPLAIN` de `AvisosVigentesPorPagina` con `usuario_id` y `periodo` fijos usa el índice nuevo y no
   aparece `Sort`.
7. `sqlc.yaml` no cambia y `sqlc generate` produce el mismo árbol.

**Impacto.** La bandeja y el historial dejan de ordenar en memoria cuando se consultan por mes, que es
el caso de la app.

---

## 3. Orden recomendado y dependencias

| Orden | Tarea | Depende de | Por qué |
| --- | --- | --- | --- |
| 1 | `avisos` | nada | Es la pieza que falta y la que el 404 necesita probar |
| 2 | `404` | `avisos` | El caso del detalle no existe hasta que existe la ruta |
| 3 | `pag` | `avisos`, solo por archivo | Comparte `avisos_servicio.go`, hacerlo antes reduce conflictos |
| 4 | `health` | nada | Paquete distinto, se puede hacer en paralelo desde el inicio |
| 5 | `index` | `pag` | Los índices se justifican con las consultas de la página |

`health` es el candidato obvio para paralelizar porque solo toca
`internal/platform/httpserver/` y `cmd/api/main.go`. `avisos`, `pag` y `404` comparten
`avisos_servicio.go`, `handler.go`, `avisos.sql` y `openapi.yaml`, así que van en serie.

`index` va al final porque su criterio 6 se verifica con el `EXPLAIN` de la consulta que crea `pag`. Si
lo adelantas, usa la consulta de este documento.

---

## 4. Lo que NO se hace

- No se toca el ledger, el dinero ni `internal/nucleo`.
- No se toca ningún modelo, proveedor de IA, prompt ni el paquete `internal/copilot`.
- No se toca el frontend.
- No se implementa A2-01 (cupo por periodo, descarte que libera cupo). Vive en `feat/cierre-ia`.
- No se implementa A0-01.
- No se cambia la forma del cuerpo de `GET /v1/inteligencia/avisos`. `data` sigue siendo arreglo y
  `meta` sigue en `null`. El cursor va en cabecera.
- No se mete Redis en `/readyz`. `TestReadyzNoDependeDeRedis` lo impide.
- No se mete Postgres en `/readyz/modelos`.
- No se usa `OFFSET`.
- No se agrega un 403 para avisos ajenos.
- No se agrega `/readyz/modelos` a `api/openapi.yaml`.
- No se edita `openapi.gen.go` a mano ni se cambia `sqlc.yaml`.
- No se toca `internal/inteligencia/avisos/` ni la lógica de cupo o de texto.
- No se hace push. La rama la publica el orquestador.

---

## 5. Verificación y definición de terminado

### 5.1 Comandos

```bash
go build ./...
go vet ./...
go test ./internal/inteligencia/ ./internal/platform/httpserver/ ./cmd/api/
go generate ./...
git diff --exit-code internal/platform/api/openapi.gen.go
```

El último es el gate local que exige `AGENTS.md:170-171`. Si `go generate ./...` modifica
`openapi.gen.go`, el commit está incompleto.

### 5.2 Pruebas de integración

Las que tocan base llevan la etiqueta `integration` y leen `APP_DATABASE_URL`
(`internal/platform/httpserver/integracion_helpers_test.go:16`). El helper de transacción ya aplica el
rol del esquema.

```bash
APP_DATABASE_URL="postgres://rol_api:api@127.0.0.1:5432/centinela?sslmode=disable" \
  go test -tags=integration -count=1 ./internal/inteligencia/ ./internal/platform/httpserver/
```

Migra la base de prueba antes, para que `0028` esté aplicada.

### 5.3 Definición de terminado

1. `go build ./...` y `go vet ./...` limpios.
2. Los tres paquetes de 5.1 pasan sin la etiqueta `integration`.
3. Las pruebas de 5.2 pasan con base migrada.
4. `go generate ./...` no deja diff en `openapi.gen.go`.
5. Cada criterio de aceptación de las cinco tareas tiene al menos una prueba.
6. `/readyz` responde 200 con Redis caído.
7. `/readyz/modelos` responde 200 sin variables configuradas.
8. Los `curl` de 5.4 devuelven lo que este documento dice.
9. `git status` en el worktree solo muestra archivos de estas cinco tareas.

### 5.4 `curl` de ejemplo

```bash
TOKEN="usuario-de-prueba"

curl -s -i -H "Authorization: Bearer $TOKEN" \
  "http://127.0.0.1:8080/v1/inteligencia/avisos?limite=20"

curl -s -i -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:8080/v1/inteligencia/avisos/6f1c2b3a-4d5e-4f60-8a71-2b3c4d5e6f70

curl -s -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:8080/v1/inteligencia/avisos/no-es-un-uuid

curl -s -i http://127.0.0.1:8080/readyz
curl -s -i http://127.0.0.1:8080/readyz/modelos
```

---

## 6. Anexos

### 6.1 Rutas exactas

| Ruta | Verbo | Autenticación | En OpenAPI |
| --- | --- | --- | --- |
| `/v1/inteligencia/avisos` | `GET` | sí | sí, `openapi.yaml:942` |
| `/v1/inteligencia/avisos/{id}` | `GET` | sí | sí, nueva |
| `/v1/inteligencia/avisos/{id}/descartar` | `POST` | sí | sí, `openapi.yaml:977` |
| `/healthz` | `GET` | no | no, decisión B-05 |
| `/readyz` | `GET` | no | no, decisión B-05 |
| `/readyz/modelos` | `GET` | no | no, misma decisión |

### 6.2 Nombres del contrato

| Pieza | Nombre |
| --- | --- |
| `operationId` del detalle | `detalleAvisoInteligencia` |
| `operationId` del listado | `listarAvisosInteligencia` |
| `operationId` del descarte | `descartarAvisoInteligencia` |
| Schema del aviso | `InteligenciaAviso` |
| Schema nuevo del detalle | `InteligenciaAvisoDetalleResponse` |
| Schema del listado | `InteligenciaAvisosResponse` |
| Schema del descarte | `InteligenciaAvisoDescarte` |
| Respuesta de error | `components/responses/Error` |
| Cabecera nueva | `X-Vera-Siguiente-Cursor` |
| Parámetros nuevos | `limite`, `cursor`, `estado` |

### 6.3 Nota de merge con A2-01

A2-01 vive en `feat/cierre-ia` y no está en `cfbf7b6`. Toca cupo por periodo, descarte que libera cupo
y reloj inyectado. Los cuatro archivos de choque previsible son
`internal/inteligencia/avisos_servicio.go`, `internal/inteligencia/handler.go`,
`bd/consultas-inteligencia/avisos.sql` y `api/openapi.yaml`.

Regla: conserva los dos comportamientos, no elijas uno.

- En `avisos_servicio.go`, `Aviso` y `AvisosDelUsuarioPaginados` son métodos nuevos. Un conflicto ahí
  significa que A2-01 renombró algo que compartes: queda el nombre de esta rama y la firma con reloj de
  A2-01.
- En `handler.go`, el conflicto probable es `ListarAvisosInteligencia`. Esta rama agrega `limite`,
  `cursor` y `estado`. A2-01 agrega lo suyo del cupo. El handler hace las dos cosas y el contrato
  declara los dos conjuntos de parámetros.
- En `avisos.sql`, las consultas nuevas no chocan. El choque es en `ContarAvisosEmitidosEnElMes` y en
  `AvisosDelUsuarioEnVentana` si A2-01 cambia la ventana a `periodo`. No reescribas la consulta de
  A2-01 para que se parezca a la tuya: son ventanas distintas y la de la página no usa ventana.
- En `openapi.yaml`, suma los parámetros de las dos ramas. Si A2-01 agrega `periodo` al listado, ese
  parámetro es el que hace que `aviso_vigente_por_usuario_periodo` se use como promete la sección 2.5.
  Actualiza el párrafo de la limitación, no lo borres.
- Regenera `openapi.gen.go` después del merge y corre el gate de 5.1.

No escribas nada que asuma el cupo por periodo. Si el merge obliga a decidir sobre cupo, esa decisión
es de quien lleva A2-01.

### 6.4 Prompt para arrancar con un agente

```
Lee solo docs/handoff-avisos-pag-health-404-index.md. Trabaja en la rama
feat/avisos-pag-health-404-index, creada desde origin/dev (cfbf7b6). No salgas de las cinco tareas
del documento: avisos, pag, health, 404 e index. No toques ledger, dinero, modelos ni IA. No hagas
push. Verifica cada ruta y linea del documento en el worktree antes de editar y, si algo no coincide,
corrige el documento en vez del codigo. Cierra con los comandos de la seccion 5 y no commitees nada
que no este en la definicion de terminado.
```

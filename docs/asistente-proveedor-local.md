# CF-A3-01 · Adaptador Ollama del puerto ProveedorIA

`internal/copilot/ollama` implementa `copilot.ProveedorIA` contra el servidor local de
Ollama. Es un adaptador de transporte y traducción: no ejecuta herramientas, no decide
reintentos de negocio y no persiste nada.

## Alcance

- `proveedor.go` habla HTTP con `POST {URL}/api/chat`, `stream: false`, plazo por intento
  y hasta tres intentos.
- `traduccion.go` convierte `SolicitudIA` al cuerpo de `/api/chat` y la respuesta al turno
  del puerto. Los tipos HTTP viven sólo en este paquete.
- El orquestador sigue siendo el único que ejecuta funciones y el único que impone el
  plazo global de la conversación.

## Endpoint

```
POST http://127.0.0.1:11434/api/chat
Content-Type: application/json
```

Cuerpo que produce el adaptador:

```json
{
  "model": "gemma4-e4b-local",
  "messages": [
    {"role": "system", "content": "instrucciones"},
    {"role": "user", "content": "texto del usuario"},
    {"role": "assistant", "content": "", "tool_calls": [{"id": "call_1", "function": {"name": "consultar_saldo", "arguments": {}}}]},
    {"role": "tool", "tool_name": "consultar_saldo", "content": "{\"saldo\":\"1000\"}"}
  ],
  "tools": [
    {"type": "function", "function": {"name": "consultar_saldo", "description": "...", "parameters": {"type": "object"}}}
  ],
  "stream": false,
  "options": {"temperature": 0.2, "num_ctx": 8192, "num_predict": 2048},
  "keep_alive": "30m"
}
```

Decisiones del contrato:

- `stream` siempre en `false`. Una respuesta por streaming obligaría a acumular trozos y
  escondería los cortes.
- `num_ctx` 8192, `num_predict` 2048 y `keep_alive` 30m son constantes del adaptador: el
  contexto cabe en una GPU de 4 GB y el modelo queda caliente entre turnos.
- El campo `think` no se envía nunca. Con `think: false` explícito, Ollama 0.40.10 deja de
  emitir `tool_calls` en Gemma 4, así que la clave se omite por completo.
- Los `parameters` de cada herramienta viajan byte a byte tal como los declaró el catálogo.
  Ni se reordenan ni se reescriben: el esquema que valida el modelo es el mismo que valida
  el backend.
- Los resultados de herramientas van como un mensaje `tool` por resultado, con `tool_name`
  para correlacionar. Ollama no usa identificadores en ese mensaje.
- La firma opaca de `ParteIA` no se envía: `/api/chat` no define ese campo.

## Configuración

| Variable | Valor inicial / validación |
| --- | --- |
| `VERA_PROVEEDOR` | valor inicial `simulado`, admite `simulado`, `gemini` o `local` |
| `VERA_OLLAMA_URL` | valor inicial `http://127.0.0.1:11434`, un origen http(s) sin ruta, credenciales, consulta ni fragmento |
| `VERA_MODELO` | `gemma4-e4b-local` con `local`, obligatorio y no puede quedar vacío |
| `VERA_TIMEOUT_LLAMADA` | `120s` con `local`, mayor que cero y máximo `5m`, por intento |
| `VERA_TEMPERATURA` | `0.2`, finita entre 0 y 1 |

`LoadConfig` acumula errores. `ConfigVera.LogValue` registra el origen de Ollama y la
presencia de la clave de Gemini, nunca la clave. `nuevoProveedorIA` no sondea el servidor
al arrancar: el proceso puede levantar antes que Ollama y el primer turno informa el fallo.

Con `gemini` o `simulado` los valores por defecto no cambian: el plazo sigue en `10s` con
máximo `30s`.

## Errores públicos

`errors.Is` funciona a través de los wrappers del orquestador. Nunca se expone el cuerpo
ni el mensaje del servidor.

| Situación | Categoría | Reintenta |
| --- | --- | --- |
| 400, 404, otro 4xx | `ErrSolicitudIA` | no |
| 401, 403 | `ErrCredencialesIA` | no |
| 429 | `ErrLimiteIA` | no |
| 408 | `ErrTimeoutIA` | sí |
| 5xx | `ErrNoDisponibleIA` | sí |
| conexión rechazada, DNS o corte de red | `ErrNoDisponibleIA` | sí |
| plazo del intento o lectura interrumpida | `ErrTimeoutIA` | sí |
| redirección 3xx | `ErrNoDisponibleIA` | no |
| `done: false`, `done_reason` distinto de `stop`, respuesta vacía, JSON inválido, tipo de contenido distinto de `application/json` o cuerpo mayor de 1 MiB | `ErrRespuestaIA` | no |

Al agotar los tres intentos el error también coincide con `ErrNoDisponibleIA`. Las
redirecciones no se siguen: reenviarían la conversación a otro destino. Una respuesta
ilegible no se reintenta porque repetirla no la corrige.

## Precisión de importes

Los argumentos se decodifican con `UseNumber` y se reenvían como JSON crudo, así que un
entero nunca pasa por `float64` al viajar al modelo. Antes de aceptarlos se rechazan los
enteros fuera de 2^53 y cualquier valor no finito: un importe redondeado llegaría al
catálogo como otro número. Los montos siguen siendo cadenas de centavos.

## Nombres de modelo

| Nombre en Ollama | Archivo en `modelos/lock.json` | Tamaño | Uso |
| --- | --- | --- | --- |
| `gemma4-e4b-local` | `gemma-4-e4b-it-q4_k_m` | 4977171584 bytes (4,6 GiB) | defecto del adaptador |
| `gemma4-e2b-local` | `gemma-4-e2b-it-q4_k_m` | 3106738272 bytes (2,9 GiB) | equipo de desarrollo con GTX 1650 de 4 GB |

El E4B en Q4_K_M no cabe en una GTX 1650 de 4 GB: el archivo solo ya ocupa 4,6 GiB, antes
del contexto y de la caché KV. El E2B sí entra. El nombre del modelo en Ollama lo fija
quien lo crea a partir del GGUF, y es el valor que recibe `VERA_MODELO`.

## Evidencia de tool calling nativo

Hecho comprobado con Ollama 0.40.10 y Gemma 4 E4B en Q4_K_M:

1. `/api/chat` acepta `tools` con el formato de función de OpenAI.
2. El modelo emite `message.tool_calls` con `function.name` y `function.arguments` sin
   necesidad de `TEMPLATE` propio en el Modelfile: la plantilla que trae el GGUF ya declara
   las herramientas.
3. Si la petición incluye `think: false`, el modelo deja de emitir `tool_calls`. Ese es el
   motivo por el que el adaptador omite el campo en lugar de enviarlo en `false`.

Para reproducirlo en una máquina con Ollama arriba:

```sh
ollama create gemma4-e2b-local -f Modelfile   # FROM gemma4/gemma-4-E2B-it-Q4_K_M.gguf
curl -s http://127.0.0.1:11434/api/chat -d '{
  "model": "gemma4-e2b-local",
  "messages": [{"role": "user", "content": "Consulta mi saldo"}],
  "tools": [{"type": "function", "function": {"name": "consultar_saldo", "description": "Consulta el saldo", "parameters": {"type": "object", "properties": {}}}}],
  "stream": false
}' | jq '.message.tool_calls'
```

La respuesta debe traer un objeto con `function.name` igual a `consultar_saldo`. Repetir la
misma petición con `"think": false` debe dejar `tool_calls` vacío. Esa comprobación no corre
en CI ni en las pruebas del paquete: requiere un servidor con el modelo cargado.

## Registro

Cada intento registra con `slog.InfoContext` el proveedor, el modelo, la duración, los
tokens conocidos y el resultado. No registra instrucciones, mensajes, resultados de
funciones ni audio. Ante timeout pueden existir cargos de cómputo local que nadie reporta,
así que no se inventa consumo.

## Evaluación contra el conjunto congelado

`cmd/evaluar-vera` acepta `-proveedor local`. En ese modo construye `ollama.Nuevo` con el modelo de
`VERA_MODELO`, no exige `-nivel-gratuito-confirmado` y usa intervalo cero salvo que se fije. El
detalle de la medición, los criterios y la reproducción están en `aceptacion-agente-local.md`.

## Verificación automatizada

```sh
go test -race ./internal/copilot/ollama ./cmd/api
go build ./...
go vet ./...
```

Las pruebas de `internal/copilot/ollama` usan `httptest` y no necesitan Ollama:

- cuerpo exacto de la petición, incluidos `stream`, `keep_alive`, `num_ctx`, `num_predict`
  y la ausencia de `think`
- paso sin alteraciones de las siete herramientas del catálogo, comparando bytes
- turnos de usuario, asistente y herramienta, con una y con dos llamadas en el mismo turno
- traducción de texto, tokens y llamadas de la respuesta
- errores por categoría, sin filtrar el cuerpo del servidor
- recuperación al tercer intento, esperas de 200 a 300 ms y de 400 a 600 ms
- timeout por intento, cancelación antes y durante la llamada
- truncamiento: `done` en `false`, `done_reason` distinto de `stop` y respuesta sin partes
- rechazo de enteros fuera de 2^53 y de esquemas inválidos
- `SoloTranscripcion` devuelve `ErrSolicitudIA`
- registro sin contenido y configuración local sin sondeo al arrancar

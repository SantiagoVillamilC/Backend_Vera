# SCRUM-119 · IA-A1-01 · Puerto ProveedorIA

Implementa HU-BE-IA-06 de `Historias_de_Usuario_Asistente_Vera.docx`.

## Contrato y alcance

`internal/copilot/proveedor.go` define `ProveedorIA.Generar`: recibe instrucciones,
turnos ordenados y declaraciones con JSON Schema. Cada parte es texto, audio en
memoria, llamada o resultado de función. Devuelve un turno del asistente y tokens.
Los tipos del SDK sólo se usan dentro de `internal/copilot/gemini`.

`cmd/api` construye el proveedor al arrancar y lo inyecta con `NewRouterConVera`.
Las rutas `/v1/vera` conservan su respuesta 501: consentimiento, catálogo,
orquestación y endpoints corresponden a otras historias. Este cambio no envía
mensajes de usuarios ni ejecuta funciones o transferencias.

El SDK oficial está fijado a `google.golang.org/genai v1.75.0`:
<https://pkg.go.dev/google.golang.org/genai@v1.75.0>.
Su grafo de dependencias requiere actualizar `golang.org/x/crypto` de v0.54.0 a
v0.55.0; `go.mod` y `go.sum` registran las versiones resueltas.

## Configuración

| Variable | Valor inicial / validación |
| --- | --- |
| `VERA_PROVEEDOR` | `simulado`; admite `simulado` o `gemini` |
| `GEMINI_API_KEY` | Obligatoria sólo con Gemini; nunca se registra |
| `VERA_MODELO` | Obligatorio con Gemini; elegir un Flash disponible en la cuenta |
| `VERA_TIMEOUT_LLAMADA` | `10s`, mayor que cero y máximo `30s`, por intento |
| `VERA_TEMPERATURA` | `0.2`, finita entre 0 y 1 |

`LoadConfig` acumula errores de configuración. `ConfigVera.LogValue` sólo registra
la presencia de la clave. No hay modelo fijado en código ni credenciales en el repo.

## Gemini

- Traduce el esquema JSON nativo conservando descripciones, tipos y obligatorios.
  Usa `AUTO` para que el modelo elija texto o llamadas, sin ejecutarlas.
- Conserva orden, llamadas paralelas, nombres, argumentos, ID y firma opaca de cada
  parte. El orquestador debe reenviar el turno recibido **completo**, seguido de
  los resultados correlacionados. Si Gemini no emite ID, éste queda vacío; no se
  inventa un identificador del proveedor. Las firmas no son texto para el usuario.
- Los importes son strings de centavos. El SDK decodifica argumentos numéricos en
  float64; se rechazan valores fuera del rango seguro de JSON/JavaScript para no
  entregar al catálogo enteros redondeados. El catálogo validará el esquema y la
  semántica de cada argumento en SCRUM-123.
- Máximo 2048 tokens de salida, un candidato y filtros de seguridad configurados
  en `BLOCK_ONLY_HIGH` para reducir falsos positivos en conversación normal sin
  desactivar los filtros. No devuelve razonamiento interno. Los tokens de
  salida incluyen los de pensamiento cuando el proveedor los informa.
- Tres intentos como máximo ante timeout, HTTP 408 o 5xx. Un 429 no se reintenta.
  Esperas de 200–300 ms
  y 400–600 ms (exponencial con jitter). Reintentos internos del SDK y continuación
  automática desactivados. Truncamiento, continuación y respuesta vacía se rechazan
  como respuesta incompleta, sin ejecutar llamadas parciales.
- El plazo o cancelación del contexto padre interrumpe intentos y esperas. El
  futuro orquestador debe imponer sus 45 segundos globales; el timeout de este
  adaptador es por intento, no por conversación completa.
- Errores públicos estables con `errors.Is`: credenciales, solicitud, bloqueo,
  límite, timeout, indisponibilidad y respuesta inválida. Al agotar reintentos,
  el error también coincide con `ErrNoDisponibleIA`. No se expone el error original
  del SDK, su cuerpo, URL ni mensaje.
- Cada intento registra con `slog.InfoContext` modelo, duración, tokens conocidos
  y resultado. El handler existente añade `request_id`. No registra instrucciones,
  mensajes, audio, resultados de funciones ni claves. Ante timeout pueden existir
  cargos que el proveedor no alcanzó a reportar; no se inventa ese consumo.

## Simulador

`provsim.Nuevo(pasos...)` consume respuestas y errores definidos en el guion. Copia
los datos para evitar mutaciones externas y sincroniza el consumo concurrente.
Un contexto ya cancelado no consume pasos. Al terminar devuelve
`ErrGuionAgotadoIA`. Crear una instancia por escenario o conversación guionada:
compartir un guion entre conversaciones repartiría sus pasos entre ellas.

Sin pasos, devuelve siempre un aviso de modo simulado sin datos financieros.
No usa red ni credenciales. Esto es lo que se selecciona al iniciar en desarrollo
sin configuración adicional.

## Verificación automatizada

```sh
go test -race ./...
go vet ./...
go generate ./...
go test -race -cover ./internal/copilot/gemini ./internal/copilot/provsim
```

Las pruebas usan `httptest`: traducción de esquemas, audio y resultados, llamadas
paralelas y firmas, precisión de importes string, límites de generación, seguridad,
clasificación de errores, recuperación al tercer intento, jitter, timeout por
intento, cancelación antes/durante la llamada o espera, ausencia de secretos en
logs y configuración. El simulador verifica guiones y consumo concurrente.

## Prueba manual contra Gemini (opt-in)

Pendiente de ejecutar con una clave y un modelo habilitado en la cuenta. La prueba
automatizada normal la omite. Sólo envía un saludo sintético, no datos personales
ni financieros. Puede consumir cuota de la cuenta. No pegar la clave en este
documento, en un commit ni en un reporte de resultados.

Desde la raíz del backend, PowerShell 7 y Docker:

```powershell
$env:GEMINI_API_KEY = Read-Host 'Clave local de Gemini' -MaskInput
$env:VERA_MODELO = Read-Host 'Modelo Flash habilitado en la cuenta'
$env:VERA_PRUEBA_REAL = '1'
try {
    docker run --rm -e GEMINI_API_KEY -e VERA_MODELO -e VERA_PRUEBA_REAL -v "${PWD}:/src" -w /src golang:1.27.0 go test -count=1 -run '^TestGeminiManual$' -v ./internal/copilot/gemini
} finally {
    Remove-Item Env:GEMINI_API_KEY, Env:VERA_PRUEBA_REAL
}
```

Con Go local, el mismo `go test` sirve sin Docker. El éxito imprime únicamente
consumo de tokens; anotar fecha, modelo y resultado sin copiar claves ni respuestas
privadas. Esta prueba confirma conectividad; la evaluación de calidad y línea base
contra el proveedor real corresponden a SCRUM-139.

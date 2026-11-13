# Contrato para la app: voz del asistente y escalón de la categoría

Aclara dos tarjetas de la app con lo que el backend hace hoy, verificado en el código. Cada afirmación
lleva `ruta:línea` o se rotula **no verificado**. Fecha de la lectura: 2026-11-13, rama
`feat/local-hardware`, con los cambios de `/sintetizar` aún sin commitear. Las líneas citadas corresponden
al árbol de trabajo de ese día.

- CF-A4-04, reproducir el audio del asistente.
- CF-A4-05, mostrar qué escalón decidió la categoría.

## Respuestas cortas

1. **El mensaje del asistente no trae audio.** `RespuestaAsistente` solo tiene `conversacion_id`,
   `mensaje_id`, `texto`, `transcripcion`, `creado_en` y `accion_pendiente` (`api/openapi.yaml:4127-4150`).
   `MensajeVera`, el historial, tampoco (`api/openapi.yaml:4028-4050`). La app debe pedir el audio aparte con
   `POST /v1/vera/sintetizar` enviando el `texto` del mensaje. La frase de la tarjeta "al recibir un mensaje o
   por el endpoint de audio" se resuelve así: solo existe el endpoint.
2. **El WAV es PCM16 mono a 24 kHz**, no 16 kHz. Se verificó en el servidor, en el validador Go y en un
   archivo generado (ver "Formato del WAV").
3. **Los campos no se llaman `origen` y `escalon`.** El contrato los llama `origen_categoria` y `escalon`, y
   salen en cada `InteligenciaMovimiento` (`api/openapi.yaml:3281-3289`). El `origen` de
   `api/openapi.yaml:3050` es del schema `Consent` (consentimientos) y no tiene relación con la categoría.
4. **Ya existe un enum** para el origen y no falta ningún valor (ver "Origen y escalón").

## Voz

### Cuándo se pide el audio

El flujo de un mensaje con voz propia es de dos llamadas:

1. `POST /v1/vera/mensajes` (texto) o `POST /v1/vera/mensajes/voz` (audio del usuario) devuelve el mensaje
   del asistente solo con texto.
2. La app llama `POST /v1/vera/sintetizar` con `{"texto": "<texto del mensaje>"}` cuando el usuario
   pulsa escuchar (o automáticamente, si el diseño lo decide).

El comentario del handler lo dice así: "entrega audio aparte del turno: un fallo nunca borra el mensaje"
(`internal/platform/httpserver/vera_sintesis.go:20`). `POST /v1/vera/mensajes/voz` recibe audio y devuelve texto,
nunca audio (`api/openapi.yaml:1533-1563`, `internal/platform/httpserver/vera_voz.go:54-68`).

### Autenticación y requisitos

| Requisito | Detalle | Fuente |
|---|---|---|
| Sesión | `bearerAuth` global, igual que el resto de `/v1` | `api/openapi.yaml:16-17` |
| Cabecera `X-Vera-Token` | **No se envía ni se exige.** Si llega, se ignora. `VERA_VOZ_TOKEN` es solo el secreto entre el backend y Kokoro y la app nunca lo necesita | `api/openapi.yaml:1380-1386` (sin parámetro), `vera_sintesis.go:21-58` (el handler no la lee) |
| Consentimiento del asistente | Vigente, si no responde 403 | `vera_sintesis.go:23`, `vera_consentimiento.go:22-23` |
| Cuota de voz | Canal `voz`, 10 por hora por defecto (`VERA_LIMITE_VOZ_HORA`), compartida con `/mensajes/voz`. **Solo se descuenta si el backend tiene sintetizador** | `vera_sintesis.go:34-41`, `vera_voz.go:47`, `cmd/api/config_vera.go:136` |
| Cuerpo | `{"texto": string}`, sin otros campos, **1 a 400 caracteres** (no bytes) | `api/openapi.yaml:2153-2163`, `vera_sintesis.go:18,30` |

Con sintetizador, la cuota se descuenta antes de sintetizar y también cuando el audio sale de la caché: el
control de uso corre en `vera_sintesis.go:39` y la caché se consulta después, en `:44` (`docs/stt-tts-operacion.md:18`
lo confirma). Un turno de voz con su audio gasta dos de las 10 de la hora. **Sin sintetizador** (backend sin
`VERA_VOZ_TOKEN`) la respuesta degradada sale antes del control de uso y no descuenta nada (`vera_sintesis.go:34-38`),
porque no hay audio que entregar. Un texto rechazado con 422 tampoco descuenta cuota. La ruta también cuenta para el
límite general de 100 solicitudes por minuto por IP (`internal/platform/httpserver/router.go:114`, valores en
`router.go:23-24`).

### Orden de comprobaciones

1. Sesión: el middleware `RequireAuth` (`internal/platform/httpserver/middleware.go:137-163`).
2. Consentimiento (`vera_sintesis.go:23`).
3. Cuerpo y texto: JSON cerrado, 1 MiB, 1 a 400 caracteres (`vera_sintesis.go:26-33`, `binding.go:32-46`).
4. Si no hay sintetizador: degradado, sin cuota (`vera_sintesis.go:34-38`).
5. Cuota de voz (`vera_sintesis.go:39-41`).
6. Síntesis con plazo de 25 s (`vera_sintesis.go:42-50`) y, si falla, degradado (`vera_sintesis.go:51-58`).

### Respuestas posibles

| Caso | Estado | Cuerpo | Cuándo |
|---|---|---|---|
| Audio | 200 `audio/wav` | WAV binario | Kokoro respondió y el WAV es válido (`vera_sintesis.go:44-50`) |
| Degradado | 200 `application/json; charset=utf-8` | `{"success":true,"data":{"audio_disponible":false,"voz_local":true},"error":null,"meta":null}` y cabecera `X-Vera-Audio: no-disponible` | Cualquier fallo del sintetizador, incluido el plazo vencido, y también cuando el backend no tiene `VERA_VOZ_TOKEN` y por eso no creó el sintetizador. En ese caso no se descuenta cuota (`vera_sintesis.go:34-38,51-58`, `internal/platform/api/envelope.go:96`) |
| Sesión | 401 `Unauthorized` | Envelope de error | Sesión ausente o inválida, la decide el middleware antes de llegar al handler (`middleware.go:141-144,160`). **Ya no existe un 401 por token de voz** |
| Consentimiento | 403 `ConsentRequired` | Envelope de error | Falta consentimiento vigente (`vera_consentimiento.go:22-23`) |
| Texto inválido | 422 `ValidationError` | Envelope de error | Vacío, solo espacios, más de 400 caracteres, JSON inválido o campos extra (`vera_sintesis.go:30-33`, `internal/platform/api/binding.go:40-43,206-217`) |
| Cuerpo enorme | 413 `PayloadTooLarge` | Envelope de error | Más de 1 MiB (`binding.go:36-38`). Declarado en el contrato de esta ruta (`api/openapi.yaml:1422-1423`) |
| Cuota | 429 `TooManyRequests` con `Retry-After` en segundos | Envelope de error | Cuota por hora o presupuesto diario, solo con sintetizador (`internal/platform/httpserver/vera_uso.go:22-32`) |
| No verificable | 503 `AssistantUnavailable` | Envelope de error | Redis o el control de uso no responden, solo con sintetizador (`vera_uso.go:14-16,33-34`), o no se pudo verificar el consentimiento (`vera_consentimiento.go:24-25`) |
| Auth caída | 503 `AuthUnavailable` | Envelope de error | La ruta está en la lista de rutas de dinero y la verificación de identidad no está disponible (`router.go:34`, `middleware.go:150-153`). **No está en el contrato** de esta operación (`api/openapi.yaml:1428-1429` solo declara el 503 del asistente) |

Cómo distinguir audio de degradado: por `Content-Type` (`audio/wav` frente a `application/json`) o por la
cabecera `X-Vera-Audio`. No hay que intentar decodificar el cuerpo antes de mirar el tipo.
Las respuestas del handler llevan `Cache-Control: no-store` (`vera_sintesis.go:22`): si la app quiere repetir un audio
sin volver a pedirlo, debe guardarlo ella.

El 401 de la ruta tiene una sola causa: la sesión. Lo emite `RequireAuth` con el código `Unauthorized`
(`middleware.go:142,160`), montado en el grupo autenticado (`router.go:139-142`). El contrato lo declara así
(`api/openapi.yaml:1418-1419`). La prueba del caso sin sesión (`vera_sintesis_test.go:54`, caso "sin sesion", y `:51`, caso
"sin sintetizador y sin sesion") lo confirma con 401.

### Qué pasa sin las variables

`cmd/api/main.go:64-72`: el sintetizador solo se crea si `VERA_VOZ_TOKEN` no está vacío. Ese valor es el secreto con el que el backend habla con Kokoro y no es una credencial de la app. `VERA_VOZ_URL` es
opcional y vale `http://127.0.0.1:8766` por defecto. `vozlocal.Nueva` solo acepta `http://127.0.0.1` y un token de
al menos 32 caracteres, si no el API no arranca (`internal/copilot/vozlocal/voz.go:26-31`, `main.go:68-70`).

| Situación del servidor | Lo que ve la app |
|---|---|
| `VERA_VOZ_TOKEN` sin definir | La síntesis queda apagada: no hay sintetizador y la respuesta es **200 degradado** con `X-Vera-Audio: no-disponible`, nunca 401 (`vera_sintesis.go:34-38`). La sesión, el consentimiento y el cuerpo se comprueban igual, pero **no se descuenta cuota** |
| Token definido y Kokoro caído o lento | 200 degradado |
| Token definido y distinto del de Kokoro (`KOKORO_TOKEN`) | 200 degradado, porque Kokoro responde 401 al API. El healthcheck del contenedor no lo detecta (`docs/pila-modelos-pod.md:66-70`) |

El token de Kokoro entra al servidor como `VERA_LAB_TOKEN` (`infra/voz-local/servidor.py:139`,
`docs/stt-tts-operacion.md:5-7`).

### Formato del WAV

PCM16 little endian, **mono, 24 000 Hz**, 48 000 bytes por segundo, cabecera de 44 bytes. Verificado en tres sitios:

- Servidor Kokoro: rechaza cualquier frecuencia distinta de 24 000 y escribe con `wave` 1 canal, 2 bytes, la
  frecuencia del motor (`infra/voz-local/servidor.py:115-124`). Prueba:
  `infra/voz-local/test_servidor.py:44`.
- Validador del API: `WAVValido` exige formato PCM, 1 canal, 24 000 Hz, 48 000 bytes por segundo, alineación 2,
  16 bits (`internal/copilot/vozlocal/voz.go:157-190`). Un WAV que no cumpla se convierte en el caso degradado.
- Archivo real: `infra/voz-local/ejemplos/vera-hola.wav`, leído con `wave`: 24 000 Hz, 1 canal, 16 bits,
  105 984 cuadros, 4,416 s.

La afirmación de 16 kHz es falsa para la salida. El único 16 kHz del repositorio es la **entrada**:
la demo del laboratorio convierte el micrófono a WAV mono de 16 kHz (`docs/asistente-laboratorio-voz.md:250`) y el
servidor de transcripción remuestrea a 16 kHz (`infra/stt-local/servidor.py:100`). Esa entrada nada tiene que ver
con el WAV que la app recibe de `/sintetizar`.
`docs/e2e-voz-local-2026-11-13.md:30` menciona un clip de 24 kHz, pero es un clip de **entrada** de Whisper
(`infra/stt-local/clips/`), no una salida de síntesis. Ese documento no mide Kokoro.

### Tamaños, plazos y límites

| Dato | Valor | Fuente |
|---|---|---|
| Texto de entrada | 1 a 400 caracteres (antes 8192 bytes). Más largo da 422. Cuenta y límites en `docs/stt-tts-operacion.md` | `vera_sintesis.go:18,30`, `api/openapi.yaml:2161` |
| Tamaño del WAV | Hasta 4 MiB más 4096 bytes de cabeceras, unos 87 s de audio. Un texto de 400 caracteres da unos 25 s | `vozlocal/voz.go:158`, `vozlocal/cache.go:29` |
| Peso por segundo | 48 000 bytes, unos 47 KiB (una frase de 4,4 s pesa 212 012 bytes) | cálculo, archivo de ejemplo |
| Plazo del backend | 25 s para sintetizar. Si vence, responde degradado, no error | `vera_sintesis.go:42`, `voz.go:32` |
| Reintento interno | Hasta 4 reintentos de 500 ms si Kokoro responde 409 (ocupado) | `voz.go:78-88` |
| Caché del API | 16 WAV por instancia, 5 minutos, por SHA-256 del texto, en memoria | `vozlocal/cache.go:16,60-66` |
| Concurrencia | Una síntesis a la vez por instancia del API | `cache.go:34-39` |
| Voz | Fija, `ef_dora`, español. La petición no permite elegirla | `vera_sintesis.go:44`, `api/openapi.yaml:2153-2163` |
| Pronunciación | El servidor Kokoro convierte `120000.00 COP` en "120000 pesos colombianos" y quita los marcadores `**` y las marcas de código | `infra/voz-local/servidor.py:18-24` |

**Tiempos medidos**

| Medida | Resultado | Condiciones | Fuente |
|---|---|---|---|
| Frase de unos 3,2 s (51 caracteres, 154 124 bytes, duración calculada a 48 000 bytes por segundo) | 1,78 s la primera vez, 56,7 µs desde la caché | Kokoro real en contenedor, CPU, modelo int8 de producción | `docs/evidencia-stt-tts-seed.md:45,49-50` |
| Frase de 4,4 s (212 012 bytes) | 7,4 s en la primera llamada | Pesos fp32 locales (no el int8), 2 hilos, CPU, sin contenedor, medido el 2026-11-13 al generar el ejemplo | medición propia, no versionada |

La medición propia usa el modelo fp32 y una CPU compartida, así que **no representa la latencia de producción**.
La única medición con el modelo int8 de producción es la de 1,78 s. **No verificado:** no hay medición de mensajes
largos con el modelo int8. El tope de 400 caracteres sale de una **estimación** con esa medición (unos 14 s de
síntesis para 400 caracteres, ver `docs/stt-tts-operacion.md`), no de una medición en este hardware. Por el plazo
fijo de 25 s del backend (`vera_sintesis.go:42`), un audio largo podría llegar como degradado. La app debe tratar el
degradado como camino normal y no como error.

### Qué debe hacer la app

1. Al pulsar escuchar, llamar a `/sintetizar` con el texto del mensaje y la sesión. No hay que enviar `X-Vera-Token`.
   Si el texto pasa de 400 caracteres, ni intentarlo: usar directamente la voz del teléfono, porque el backend
   respondería 422.
2. Si la respuesta es `audio/wav`, reproducirla (el reproductor debe aceptar WAV PCM16 de 24 kHz).
3. Si es 200 JSON degradado, usar la voz del teléfono y avisarlo con discreción, como pide la tarjeta.
4. Ante 401, 403, 422, 429 o 503 (incluido `AuthUnavailable`), conservar el mensaje y usar la voz del teléfono. Con 429 respetar
   `Retry-After` antes de volver a pedir. Con 403 llevar al usuario al flujo de consentimiento.
5. No pedir el audio de un mensaje dos veces seguidas sin necesidad, porque cada intento gasta cuota.

### Hallazgos para decidir

- **Sin secreto en el cliente.** Resuelto: `/sintetizar` ya no pide `X-Vera-Token`. `VERA_VOZ_TOKEN` (igual a
  `KOKORO_TOKEN`, `docs/pila-modelos-pod.md:66-70`) queda solo en el servidor, y la app no lo necesita ni lo recibe.
  Lo que protege el endpoint es la sesión, el consentimiento de IA y la cuota de voz.
- **Contrato completo.** `api/openapi.yaml:1418-1429` declara 401 (sesión), 403, 413, 422, 429 y 503. Le falta
  el 503 `AuthUnavailable` de la lista de rutas de dinero (`router.go:34`), que no se agregó.
- **CORS.** `router.go:110` todavía permite la cabecera `X-Vera-Token` en `AllowedHeaders`. Es inocuo (ningún
  handler la lee) y no se tocó.
- **Tarjeta ambigua.** Ni el contrato ni el código entregan audio dentro del mensaje. Conviene reescribir la
  tarjeta CF-A4-04 como "pedir el audio a `/sintetizar` con el texto del mensaje".

### Limitaciones conocidas

- **La caché de síntesis es compartida entre usuarios.** Su clave es el SHA-256 del texto, sin el usuario
  (`internal/copilot/vozlocal/cache.go:43`). Quien conozca un texto exacto puede inferir por el tiempo de respuesta
  (56,7 µs frente a 1,78 s en `docs/evidencia-stt-tts-seed.md:49-50`) si otro usuario lo sintetizó en los últimos
  5 minutos (`cache.go:66`). La cuota se descuenta igual, pero el tiempo delata el acierto. No se cambió la caché.
- **Kokoro no cancela el cálculo cuando el cliente abandona.** Si el backend corta a los 25 s, Kokoro termina el
  audio con su cerrojo tomado y las síntesis siguientes reciben 409 (`infra/voz-local/servidor.py:90-91`) hasta que
  acabe. El comentario de `voz.go:76-77` lo reconoce. El tope de 400 caracteres acota esa ventana, no la elimina.
- **Cualquier sesión con consentimiento llega a Kokoro**, porque ya no hay token de voz en la app. Lo limitan la
  cuota de 10 por hora por usuario y el tope de texto.

## Ejemplo y simulador para desarrollar sin backend

### WAV de ejemplo

`infra/voz-local/ejemplos/vera-hola.wav` (212 012 bytes, 4,416 s, SHA-256
`ce63cb866cd7a8fd647dab8b906e3bdec505dc8839b0e7ece1219b667f786250`). Texto:
"Hola, soy Vera. Tu saldo disponible es de 120000.00 COP."

Cómo se generó: con el `crear_servidor` real de `infra/voz-local/servidor.py` en `127.0.0.1` con puerto
efímero, un token aleatorio y la voz `ef_dora`, sobre CPU (`CPUExecutionProvider`), llamando a `POST /sintetizar`.
**No se bajó ningún peso**: se usaron archivos que ya estaban en disco.

- `voices-v1.0.bin`: su SHA-256 coincide con el del manifiesto (`infra/voz-local/descargar.py:14-16`).
- Modelo: el disco tiene `kokoro-v1.0.onnx` en fp32 (326 MB), **no** el `kokoro-v1.0.int8.onnx` que fija el
  manifiesto (`infra/voz-local/descargar.py:10`) y que no está en este equipo. El formato del WAV es idéntico,
  pero la calidad de la voz puede diferir un poco de la que sale en producción.

Para obtener uno con el modelo de producción: construir y levantar `infra/modelos/vera-kokoro` con
`KOKORO_TOKEN` (`docs/pila-modelos-pod.md:12,59`) y llamar
`curl -H "X-Vera-Token: $KOKORO_TOKEN" -d '{"texto":"Hola"}' http://127.0.0.1:8766/sintetizar -o ejemplo.wav`.
Verificar antes que `docker` y `curl` existan en el equipo de quien lo ejecute.

### Simulador

`infra/voz-local/simulado.py`, solo biblioteca estándar de Python 3, sin modelos ni dependencias. Simula
`POST /v1/vera/sintetizar` con el orden de comprobaciones del handler real (sesión, consentimiento, cuerpo, cuota) y
con el mismo tope de 400 caracteres.

```bash
python3 infra/voz-local/simulado.py --puerto 8780            # solo loopback
python3 infra/voz-local/simulado.py --host 0.0.0.0 --puerto 8780   # emulador Android: http://10.0.2.2:8780
```

Hace falta una cabecera `Authorization: Bearer <cualquier valor>`. No pide `X-Vera-Token`: si llega, la ignora, igual que el backend real. El caso se elige con `X-Simulado-Caso`:

| Valor | Respuesta |
|---|---|
| `audio` (por defecto) | 200 `audio/wav` con `ejemplos/vera-hola.wav` |
| `degradado` | 200 JSON degradado y `X-Vera-Audio: no-disponible` |
| `sin-consentimiento` | 403 `ConsentRequired` |
| `cuota` | 429 `TooManyRequests` con `Retry-After: 60` |
| `no-verificable` | 503 `AssistantUnavailable` |
| `sin-configurar` | Igual que `degradado`: el backend sin `VERA_VOZ_TOKEN` no tiene sintetizador y responde 200 degradado. El simulador no lleva la cuenta de la cuota, así que no refleja que ese caso no la descuenta |

Una sesión ausente da 401 `Unauthorized`. Un texto inválido (vacío o de más de 400 caracteres) da 422 y un
cuerpo de más de 1 MiB da 413. El simulador siempre devuelve el mismo WAV sin importar el texto. Tiene pruebas en
`infra/voz-local/test_simulado.py`:

```bash
cd infra/voz-local && python3 -m unittest test_simulado
```

La suite completa de `infra/voz-local` (5 pruebas de `test_servidor.py` y 7 de `test_simulado.py`) se ejecuta con
`out/voz-venv/bin/python -m unittest discover -s infra/voz-local -p 'test_*.py'`.

El simulador no cubre `/v1/vera/mensajes` ni `/mensajes/voz` (**no verificado** si hace falta para la tarjeta, porque
la app ya envía voz según la tarjeta).

## Origen y escalón de la categoría

### Campos del contrato

Ambos viajan en cada `InteligenciaMovimiento`, tanto en `GET /v1/inteligencia/movimientos` como en la respuesta de
`POST /v1/inteligencia/movimientos/{id}/correcciones` (`api/openapi.yaml:819-861, 923-965, 3247-3289, 3386, 3547`). Los dos son
**obligatorios y no nulos** (`api/openapi.yaml:3249-3251`) y el handler siempre los escribe
(`internal/inteligencia/handler.go:327-328`). "Si el campo no llega" solo puede ocurrir con un backend antiguo, por eso
la app debe caer a una etiqueta neutra y no romperse, como pide la tarjeta.

- `origen_categoria`: enum `OrigenEnriquecido`, 7 valores (`api/openapi.yaml:3291-3297`).
- `escalon`: entero de 1 a 7 (`api/openapi.yaml:3283-3289`, restricción en BD en
  `bd/migraciones/0032_add_escalon_a_la_copia.sql:35-37`).

### Valores emitidos

El conjunto lo fija `inteligencia.OrigenesEscribibles` (`internal/inteligencia/cascada.go:98-106`), el CHECK de
la migración 0032 (`0032_add_escalon_a_la_copia.sql:39-43`) y el contrato. `TestContratoDeOrigenesYCatalogo`
(`internal/inteligencia/contrato_inteligencia_test.go:50-55`) compara el enum del contrato contra la lista del
código, así que un valor nuevo sin contrato rompe la prueba. Se ejecutó el 2026-11-13 y pasa
(`go test -run TestContratoDeOrigenesYCatalogo ./internal/inteligencia`).

| `origen_categoria` | `escalon` | Dónde se asigna |
|---|---|---|
| `memoria` | 1 | `cascada.go:139`. También lo escribe una corrección del usuario: `bd/consultas-inteligencia/correcciones.sql:71-73` |
| `contraparte_interna` | 2 | `cascada.go:146`. Solo GMF y comisión de la billetera, categoría `comisiones_costos_financieros` (`contraparte.go:160-177`) |
| `mcc` | 3 | `cascada.go:162`. Solo QR a comercio (`cascada.go:150-158`) |
| `catalogo` | 4 | `cascada.go:154,168,172`. Solo si la tabla `inteligencia.catalogo_marca` tiene filas (`enriquecimiento.go:609-611`) |
| `lexico` | 5 | `cascada.go:178`. Solo con `LEXICO=v2` (`docs/configuracion-entornos.md:24`) |
| `tfidf` | 6 | `cascada.go:188`. Solo con `CLASIFICADOR=tfidf` (`docs/configuracion-entornos.md:23`) |
| `sin_categoria` | 7 | `cascada.go:157,183`. Ningún peldaño decidió |
| `sin_categoria` | **6** | Abstención del clasificador: `cascada.go:192` devuelve escalón 6 sin origen y `enriquecimiento.go:525-528` lo guarda como `sin_categoria` |

La combinación `sin_categoria` con escalón 6 significa "el modelo miró y se abstuvo". Con escalón 7 significa "no
llegó a mirar o ninguna señal aplicó" (pago por llave, destino persona, nombre vacío o escalón 6 apagado). Por eso la
app debe leer **los dos campos juntos**, no solo el origen. Lo dice el propio contrato (`api/openapi.yaml:3286-3289`) y la
migración (`0032_add_escalon_a_la_copia.sql:3-6`).

El orden `escalon` equivale al orden del origen en los seis primeros casos, así que la app puede mostrar el chip
solo con `origen_categoria`, y usar `escalon` para separar los dos `sin_categoria`.

### Etiquetas para el chip

Propuesta de texto, en español neutro. La columna "Chip" es el nombre corto y "Al tocar" la explicación de la
tarjeta. Los textos son una propuesta de redacción: lo verificado es la señal que los respalda, no el texto.

| `origen_categoria` / `escalon` | Chip | Al tocar |
|---|---|---|
| `memoria` / 1 | Tu corrección | Vera usó la categoría que tú elegiste antes para este comercio o esta persona. |
| `contraparte_interna` / 2 | Cargo de la billetera | Es una comisión o el impuesto GMF que cobra la propia plataforma, por eso va en comisiones y costos financieros. |
| `mcc` / 3 | Tipo de comercio (MCC) | El comercio informó su tipo de negocio en el pago por QR y ese código define la categoría. |
| `catalogo` / 4 | Catálogo de marcas | El comercio está en una lista de marcas conocidas que ya tiene su categoría. |
| `lexico` / 5 | Palabras del nombre | El nombre del comercio contiene una palabra que apunta a una sola categoría. |
| `tfidf` / 6 | Modelo de texto (TF-IDF) | Un modelo local comparó el nombre del comercio con ejemplos y propuso esta categoría. |
| `sin_categoria` / 6 | Sin categoría, Vera no adivina | Vera analizó el nombre y prefirió no arriesgar una categoría. Puedes asignarla tú. |
| `sin_categoria` / 7 | Sin categoría | Ninguna señal alcanzó para decidirla. Puedes asignarla tú. |
| Valor desconocido o ausente | (sin chip) | No mostrar nada, o mostrar solo la categoría. No debe romper la pantalla. |

Para la sustentación: la abstención (`sin_categoria` / 6) es información del diseño de la cascada, no un error.
Conviene presentarla con un estilo neutro y no de alerta, como advierte la tarjeta.
Tras una corrección del usuario la respuesta trae `origen_categoria: memoria` y `escalon: 1`
(`correcciones.sql:72-73`), así que el chip cambia a "Tu corrección" sin recargar el listado.

### Lo que la app hace hoy

Se leyó la copia local de la app en `/home/chris/Documents/vera-reconciliacion/Vera` (rama `main`, HEAD `b5ccc3b`).
**No verificado** que sea la rama que Laura está editando.

| Hallazgo | Fuente |
|---|---|
| Lee `origen_categoria` y lo traduce con `etiquetaDeOrigen` | `lib/features/inteligencia/data/modelos.dart:20-43,79-81` |
| **No lee `escalon`**: `fromJson` no tiene ese campo, así que no distingue `sin_categoria` del escalón 6 del del 7 | `modelos.dart:64-83` |
| Etiquetas actuales: `memoria` "Tu corrección", `contraparte_interna` "Contacto", `mcc` "Comercio", `catalogo` "Catálogo", `lexico` "Descripción", `tfidf` "Clasificador", `sin_categoria` vacía | `modelos.dart:33-43` |
| "Contacto" es incorrecto para `contraparte_interna`: el backend solo lo emite para GMF y comisión de la billetera, no para contactos | `contraparte.go:160-177` frente a `modelos.dart:35` |
| Sobran `llm` y `manual`: el backend dejó de emitirlos con la migración 0032 | `modelos.dart:14-15,27-28,40-41` frente a `0032_add_escalon_a_la_copia.sql:8-10` |
| Un valor desconocido cae a `desconocido` sin romper, y no muestra etiqueta | `modelos.dart:79-81` |
| La etiqueta se pinta como texto suelto junto a la fecha, y `sin_categoria` no pinta nada | `lib/features/inteligencia/presentation/movimientos_enriquecidos_screen.dart:372-376` |

Para CF-A4-05 esto significa: reutilizar `etiquetaDeOrigen` y extenderla con la tabla de arriba, leer `escalon`
en `fromJson` y separar la abstención del escalón 6. Con las etiquetas actuales el movimiento decidido por catálogo
se ve como "Catálogo" y no como "Catálogo de marcas", que es lo que pide el criterio de aceptación.

### ¿Falta un enum o un valor?

- **Enum de origen:** ya existe (`OrigenEnriquecido`) y está pinchado por una prueba. No hace falta crearlo.
- **Valores faltantes:** ninguno. Los siete valores cubren los siete escalones y el CHECK de la BD no admite otros.
  La migración 0032 retiró `llm` y `manual` (`0032_add_escalon_a_la_copia.sql:8-10,13-19`), así que si la app todavía
  tiene etiquetas para esos dos, sobran.
- **Mejora opcional:** `escalon` es un entero libre de 1 a 7 en el contrato sin relación con el origen. Se puede
  documentar en el contrato la tabla de arriba, o declarar un enum numérico, pero no es necesario.
- **Nombre en la tarjeta:** la tarjeta CF-A4-05 dice `origen` y `escalon`. Debe decir `origen_categoria` y `escalon`.

## Verificación

Ejecutado el 2026-11-13 sobre el árbol de trabajo de la rama `feat/local-hardware`:

- `go build ./...`, `go vet ./...` y `go test -count=1 -short ./...`: en verde.
- Integración (`-tags integration -p 1`) de `internal/platform/httpserver` con Postgres 16 y Redis 7 efímeros: en verde, sin
  pruebas saltadas.
- `python3 -m unittest discover -s infra/voz-local -p 'test_*.py'` con `out/voz-venv/bin/python`: 12 pruebas (5 del servidor Kokoro y 7 del simulador), OK.
- Casos del 401 de sesión: `TestSintesisRouter` (`vera_sintesis_test.go:51,54`), casos "sin sesion" y "sin sintetizador y sin sesion".

## No verificado

- La latencia de `/sintetizar` con el modelo int8 de producción para mensajes de más de unos 4 s. El tope de 400
  caracteres es una estimación a partir de la medición de 1,78 s, no una medición en este hardware.
- La longitud máxima de lo que genera el asistente en producción. Con las instrucciones v7 (sin regla de brevedad)
  hubo 3 de 63 respuestas guardadas sobre 400 caracteres, con máximo de 686. Con la regla de brevedad de v8 solo hay 2
  respuestas medidas (máximo 102) y de v9, la versión actual, ninguna. Algunas respuestas reales recibirían 422 y la
  app usaría la voz del teléfono. Detalle y fuentes en `docs/stt-tts-operacion.md`.
- Si el chip de la app ya traduce los escalones con nombres propios: el código de la app no está en este repositorio
  y se leyó una copia local.
- Que `memoria` provenga solo de correcciones del usuario. Se verificó que las correcciones la escriben, no que sea la
  única fuente.
- Si la rama de la copia local de la app es la que Laura está editando.

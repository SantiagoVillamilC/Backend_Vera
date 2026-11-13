# Operación de la entrega STT/TTS

## Kokoro integrado

Configurar en la API `VERA_VOZ_TOKEN` (32 caracteres como mínimo) y opcionalmente
`VERA_VOZ_URL=http://127.0.0.1:8766`. El servidor de Kokoro debe recibir el mismo valor
como `VERA_LAB_TOKEN`. No se registran tokens ni texto de síntesis.

`VERA_VOZ_TOKEN` es el secreto entre el backend y Kokoro, nada más. La app no lo envía
ni lo conoce. Sin él la API no crea el sintetizador y la síntesis queda apagada: el endpoint
responde 200 degradado (sin audio, `voz_local` en verdadero), nunca 401. Ese caso se responde después
de la sesión, el consentimiento y la validación del cuerpo, pero **sin descontar cuota**, porque no hay
audio que entregar.

`POST /v1/vera/sintetizar` recibe `{"texto":"Hola, soy Vera."}` y la sesión de usuario.
No usa `X-Vera-Token`: si llega, se ignora. Exige consentimiento del asistente y reutiliza la cuota de voz por
usuario: por defecto 10 solicitudes por hora, compartidas con el envío de voz.
Con sintetizador configurado, las solicitudes que salen de la caché también consumen cuota. Redis no
disponible impide admitir uso: esa situación responde 503 y no se confunde con una caída del sintetizador.

Éxito: HTTP 200 con `Content-Type: audio/wav`, PCM16 mono a 24 kHz, voz `ef_dora`.
Fallo del sintetizador: HTTP 200 JSON con el envelope habitual, `data` igual a
`{"audio_disponible":false,"voz_local":true}` y cabecera `X-Vera-Audio: no-disponible`.
La aplicación conserva el mensaje recibido y usa su síntesis local. La integración
de reproducción en Flutter no forma parte de estos cambios del backend.

Límites: 400 caracteres de texto (cuenta abajo), plazo total de 25 segundos, WAV de
aproximadamente 87 segundos como máximo. Caché en memoria por SHA-256 del texto, TTL de cinco minutos,
16 entradas (aproximadamente 64 MiB de audio). Se serializan las síntesis concurrentes,
se respeta la cancelación y nunca se cachean fallos. La caché es por instancia y se
pierde al reiniciar. Los WAV salen con `Cache-Control: no-store`.

### Por qué el tope de texto es 400 caracteres

Sin token de voz del cliente, cualquier sesión con consentimiento llega a Kokoro, que calcula el audio
completo con un cerrojo tomado. Un texto largo dejaba la síntesis ocupada y los demás usuarios recibían
409 y el degradado. El tope baja de 8192 a 400 para que el peor caso quepa con margen en el plazo de
25 s. Se mide en caracteres, igual que `maxLength` en `api/openapi.yaml`, no en bytes.

Cuenta, **estimación no medida en este hardware**:

| Dato | Valor | Origen |
|---|---|---|
| Tiempo de síntesis | 1,78 s para 3,21 s de audio: 0,555 s de cómputo por segundo de audio | `docs/evidencia-stt-tts-seed.md`, Kokoro int8 en contenedor, CPU |
| Tasa de habla | 15,9 caracteres por segundo (51 caracteres en 3,21 s en esa misma medición). Como referencia general, sin medir aquí y sin fuente citada, el habla en español ronda 150 a 170 palabras por minuto | misma medición |
| Costo por carácter | 0,555 / 15,9, unos 0,035 s | cálculo |
| Tope sin margen | 25 / 0,035, unos 716 caracteres | cálculo |
| Tope elegido | 400 caracteres: unos 25 s de audio y unos 14 s de síntesis, el 56 % del plazo | cálculo |

El margen restante cubre los reintentos por 409 (hasta 2 s), la red local y la variación de la CPU. Un
texto de 8192 caracteres habría pedido unos 286 s de síntesis, once veces el plazo. Si el equipo real es
más de 1,75 veces más lento que el de la medición, un texto en el tope vence el plazo y la respuesta es
el degradado, no un error. La medición propia con pesos fp32 en CPU compartida (7,4 s para 4,4 s de audio)
no representa producción, pero indica que el margen no es holgado: medir en el equipo de despliegue antes
de subir el tope.

¿Caben los textos reales del asistente? **Solo en parte, y no está verificado para la versión que corre hoy.**
Las instrucciones del repositorio piden una a tres frases cortas, idealmente hasta 60 palabras, y permiten ampliar
si la persona pide detalle o una lista (`internal/copilot/instrucciones.go:12`). Longitudes reales de las respuestas
guardadas en el repositorio, en caracteres, contra el tope de 400:

| Fuente | Respuestas | Máximo | Sobre 400 |
|---|---|---|---|
| v6 (`docs/evaluaciones/conversacion-antes.json`) | 19 | 362 | 0 |
| v7, sin la regla de brevedad (`conversacion-despues.json`, `conversacion-coloquial.json`, `conversacion-coloquial-corregida.json`, `conversacion-voz-validada.json`) | 63 | 686, un consejo en prosa y no una lista | 3 |
| v8, con la regla (`catalina-respuestas-breves.json`) | 2 | 102 | 0 |
| v9, la versión actual (`internal/copilot/instrucciones.go:9`) | sin datos de evaluación | no medido | no medido |
| Sin versión registrada (`revision-flujo.json`, `revision-preparacion.json`, `voz-natural-*.json`) | 67 | 441 | 1 |

La regla de brevedad entró con la versión v8 (commit `357dbee`), por eso los desbordes de v7 son anteriores a ella,
pero la evidencia con la regla puesta son solo 2 respuestas. Además hay una evaluación del modelo local con otro
prompt de voz, de una o dos frases, que vive **fuera de este repositorio** (`modelos-gemma/prompt_sistema.py` y
`modelos-gemma/evaluacion/resultados/*/casos.jsonl` en el repositorio de la tesis, con otras herramientas, así que
es evidencia débil para este backend): 1741 respuestas de todas las corridas, incluidas las de referencia, máximo
815 caracteres (un listado de transacciones), solo 2 sobre 320 y solo 1 sobre 400.

Consecuencia: con 400 caracteres, algunas respuestas reales, sobre todo listas o explicaciones largas, reciben 422 y
la app usa la voz del teléfono, como con cualquier otro rechazo. Subir el tope es una decisión de producto con un
costo medible en la estimación: cada 100 caracteres añaden unos 3,5 s de síntesis. El máximo de v7 (686 caracteres)
estimaría unos 24 s, el 96 % del plazo, es decir, sin margen.

### Limitaciones conocidas

- **La caché de síntesis es compartida entre usuarios.** Su clave es el SHA-256 del texto, sin el usuario.
  Quien conozca un texto exacto puede inferir por el tiempo de respuesta (56,7 µs frente a 1,78 s en la
  medición) si otro usuario lo sintetizó en los últimos 5 minutos. Se acepta por ahora y no se cambió.
- **Kokoro no cancela el cálculo cuando el cliente abandona.** Si el backend corta a los 25 s, Kokoro
  termina el audio con el cerrojo tomado y, mientras tanto, otras síntesis reciben 409 y salen degradadas.
  El tope de 400 caracteres acota esa ventana, no la elimina. Dentro de una misma instancia de la API
  las síntesis ya se hacen de una en una (`internal/copilot/vozlocal/cache.go:34-39`), así que las demás
  esperan su turno hasta agotar los 25 s. Kokoro responde 409 si está ocupado
  (`infra/voz-local/servidor.py:90-91`) y la API reintenta hasta 4 veces cada 500 ms
  (`internal/copilot/vozlocal/voz.go:78-88`).

## Whisper local

```bash
docker build -t vera-stt infra/stt-local
docker volume create vera-whisper-modelos
docker run --rm --network host -v vera-whisper-modelos:/modelos \
  -e WHISPER_TOKEN="$WHISPER_TOKEN" -e WHISPER_GPU=no vera-stt
```

`--network host` requiere soporte de red host (Linux o Docker Desktop configurado).
Otra opción es compartir el espacio de red del contenedor de la API con
`--network container:NOMBRE_API`. No publicar el servicio para sortear loopback.
El directorio `/modelos` debe ser escribible por UID 10001.

El primer arranque descarga `small`; posteriores ejecuciones pueden usar
`HF_HUB_OFFLINE=1` y la caché de modelos, sin Internet. CPU usa `int8`; GPU usa
`float16` con `WHISPER_GPU=si` y un runtime CUDA compatible, no incluido en esta
imagen CPU. El modelo, el idioma español, `beam_size=1` y VAD se conservan.

El servicio solo admite `127.0.0.1`. `GET /healthz` y `POST /transcribir` exigen
`X-Vera-Token`. Máximo 8 MiB y 60 segundos de audio decodificado; formatos WAV, M4A/MP4
y MPEG. Una sola transcripción simultánea; otra obtiene 409. Errores públicos:
401 token, 415 tipo, 413 tamaño/duración, 422 audio inválido. El silencio devuelve
`transcripcion` vacía. No se escribe audio ni texto a disco.

El cliente Go `internal/copilot/sttlocal.Nuevo(url, token, plazo)` admite un plazo
positivo de hasta dos minutos, respeta contexto, no sigue redirecciones y devuelve
las categorías públicas de `copilot`. `Transcribir` no crea conversaciones. La capa
que lo integre debe tratar texto vacío como «audio no entendido» y conservar el chat
por texto si STT no está disponible. El proveedor compuesto con el razonador sigue
pendiente, tal como delimita la entrega original.

Se fija `av==16.1.0`: PyAV 19 elimina el argumento usado por faster-whisper 1.2.1,
una [incompatibilidad confirmada por el proyecto](https://github.com/SYSTRAN/faster-whisper/issues/1589).

## Verificación

```bash
go test -race ./...
go vet ./...
python -m unittest discover -s infra/stt-local -p test_servidor.py
```

Las pruebas Go usan servicios HTTP de prueba; las Python separan el contrato del
modelo. Las pruebas de audio real requieren los pesos. Para el conjunto oficial de
24 clips usar `infra/stt-local/probar.py` y las referencias del repositorio de modelos;
ese conjunto no está incluido en este backend.

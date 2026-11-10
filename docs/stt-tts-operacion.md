# Operación de la entrega STT/TTS

## Kokoro integrado

Configurar en la API `VERA_VOZ_TOKEN` (32 caracteres como mínimo) y opcionalmente
`VERA_VOZ_URL=http://127.0.0.1:8766`. El servidor de Kokoro debe recibir el mismo valor
como `VERA_LAB_TOKEN`. No se registran tokens ni texto de síntesis.

`POST /v1/vera/sintetizar` recibe `{"texto":"Hola, soy Vera."}`, sesión de usuario
y `X-Vera-Token`. Exige consentimiento del asistente y reutiliza la cuota de voz por
usuario: por defecto 10 solicitudes por hora, compartidas con el envío de voz.
Las solicitudes de caché también consumen cuota. Redis no disponible impide admitir
uso; esa situación responde 503, no se confunde con una caída del sintetizador.

Éxito: HTTP 200 con `Content-Type: audio/wav`, PCM16 mono a 24 kHz, voz `ef_dora`.
Fallo del sintetizador: HTTP 200 JSON con el envelope habitual, `data` igual a
`{"audio_disponible":false,"voz_local":true}` y cabecera `X-Vera-Audio: no-disponible`.
La aplicación conserva el mensaje recibido y usa su síntesis local. La integración
de reproducción en Flutter no forma parte de estos cambios del backend.

Límites: 8192 bytes de texto, plazo total de 25 segundos, WAV de aproximadamente
87 segundos como máximo. Caché en memoria por SHA-256 del texto, TTL de cinco minutos,
16 entradas (aproximadamente 64 MiB de audio). Se serializan las síntesis concurrentes,
se respeta la cancelación y nunca se cachean fallos. La caché es por instancia y se
pierde al reiniciar. Los WAV salen con `Cache-Control: no-store`.

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

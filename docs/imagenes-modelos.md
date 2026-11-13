# Imágenes de los modelos: razonador, transcripción y voz neural

Tres imágenes separadas para que el razonador, la transcripción y la voz neural no compartan ciclo de
vida. `vera-llm` y `vera-voz` montan sus pesos desde el volume `pesos` y **no llevan pesos dentro**.
`vera-kokoro` es la excepción: sus pesos (unos 0,14 GB) van dentro de la imagen, con huella verificada
en el build. Se construyen en local y no se publican en esta sesión.

## vera-llm

- Base: `ollama/ollama:0.40.10`, la versión que fija `modelos/lock.json` para los GGUF
  `gemma-4-e2b-it-q4_k_m` y `gemma-4-e4b-it-q4_k_m`.
- Pesos: `/pesos/modelos` del volume. El GGUF se elige con `OLLAMA_MODELO` y nunca entra en la capa
  de la imagen.
- Usuario: `1000:1000`, sin root. El volume se inicializa con el contenido y el dueño de la imagen.
- Healthcheck: `ollama list` falla mientras el servidor no responde y pasa cuando está arriba. El
  GGUF se carga en el primer `/api/generate`, no al arrancar: la compuerta cubre el servidor y la
  primera generación es la que paga la carga en frío.
- Recursos: 4 GB de VRAM para E2B. E4B no cabe. Sin GPU, Ollama cae a CPU y la latencia se sale del
  presupuesto de la demo.

## vera-voz

- Base: `python:3.12-slim-bookworm`, con `faster-whisper==1.2.1` y `av==16.1.0` del lockfile. Reutiliza
  `infra/stt-local/servidor.py`, el mismo contrato `/healthz` y `/transcribir`, así que el backend
  solo cambia a quién le manda el audio.
- Pesos: la caché de Hugging Face va a `/pesos/huggingface`. `whisper small` se descarga en el primer
  arranque y queda en el volume.
- Usuario: `1000:1000`. El token `WHISPER_TOKEN` llega por entorno (Infisical o el archivo del
  operador) y no se hornea.
- Healthcheck: el servidor carga el modelo antes de escuchar, así que falla mientras carga y pasa al
  responder `/healthz`. `start-period` de 120 s y 18 reintentos.
- Recursos: CPU int8 por defecto (`WHISPER_GPU=no`). Con GPU, `WHISPER_GPU=si` usa float16.
- Red: `servidor.py` exige escuchar en `127.0.0.1`, así que el compose usa `network_mode: host` para
  conservar ese invariante. No hay puertos publicados en interfaces externas. Los tres servicios
  quedan en el loopback del host.

## vera-kokoro

- Base: `python:3.12.12-slim-bookworm`, con `kokoro-onnx==0.6.1` de `infra/voz-local/requirements.txt`.
  Reutiliza `servidor.py` y `descargar.py` de `infra/voz-local` sin cambios.
- Pesos: van dentro de la imagen (unos 0,14 GB). `descargar.py` los baja durante el build con SHA256 y
  tamaño verificados, con los mismos valores que `modelos/lock.json`. No usa el volume `pesos`.
- Licencia y aislamiento: `phonemizer` es GPL-3.0, así que esta imagen va aparte de `vera-voz`. Un
  fallo del sintetizador no tumba la transcripción.
- Usuario: `1000:1000`. El token llega por `KOKORO_TOKEN`, que el compose pasa al contenedor como
  `VERA_LAB_TOKEN`. Mínimo 32 caracteres y no se hornea.
- Healthcheck: falla mientras carga ONNX y calienta la primera frase. `start-period` de 60 s y 12
  reintentos.
- Red y recursos: `network_mode: host`, escucha en `127.0.0.1:8766` y corre en CPU.

## Construir y levantar

```bash
WHISPER_TOKEN="$(cat /ruta/al/secreto-whisper)" KOKORO_TOKEN="$(cat /ruta/al/secreto-kokoro)" docker compose -f infra/modelos/compose.yaml config
WHISPER_TOKEN="$(cat /ruta/al/secreto-whisper)" KOKORO_TOKEN="$(cat /ruta/al/secreto-kokoro)" docker compose -f infra/modelos/compose.yaml up --build
```

`config` exige `WHISPER_TOKEN` y `KOKORO_TOKEN` a propósito: sin ellos la validación del compose falla
en vez de arrancar un servicio sin token. El `VERA_VOZ_TOKEN` de la API debe valer lo mismo que
`KOKORO_TOKEN` (ver `docs/pila-modelos-pod.md`). Los puertos quedan en loopback (`127.0.0.1:11434`,
`127.0.0.1:8767` y `127.0.0.1:8766`).

## Qué se verificó en esta VPS

- `docker compose -f infra/modelos/compose.yaml config` con tokens de prueba para `WHISPER_TOKEN` y
  `KOKORO_TOKEN`: válido (salida 0).
- Igual con `-f infra/modelos/compose.cuda.yaml`: válido (salida 0).
- Sin `KOKORO_TOKEN`: falla (salida 1) con `required variable KOKORO_TOKEN is missing a value`.
- `docker build -f infra/modelos/vera-voz/Dockerfile -t vera-voz:local .`: construye la imagen
  `sha256:cf34fd49fba16927219c18faf0a4581a88f412c4eb294a223a22c6ba4eb3eeca`, con faster-whisper
  1.2.1 y sin pesos dentro.
- `docker compose config` no construye nada. Esa validación no construye. La imagen `vera-llm` sí se
  construyó después en el equipo local (ver `docs/mediciones-locales-2026-11-13.md`). Las imágenes
  `vera-kokoro` y `vera-voz:cuda` siguen sin construirse ni ejecutarse.
- Arranque en frío de `vera-voz` medido en esta VPS: **39 s** desde `docker run` hasta `healthy`,
  con el volume de pesos vacío (incluye la descarga del modelo `small`), límites 512 MB y 0,5 CPU,
  `WHISPER_GPU=no` (int8 en CPU) y red del host. Sin GPU la transcripción tarda más que en un equipo
  con tarjeta. La compuerta solo mide que el servicio queda sano.

## Publicación a ghcr (no ejecutada)

Ahora existe `.github/workflows/cd-modelos.yml`, que publica las tres imágenes desde CI y emite sus
digests, y `infra/modelos/deploy-modelos.sh`, que las despliega por digest con vuelta atrás (ver
`docs/despliegue-digest-rollback.md`, sección «Pila de modelos»). Ninguno de los dos se ha ejecutado
contra el registro real. Los comandos manuales de abajo quedan como alternativa.

```bash
docker tag vera-llm:local ghcr.io/<organizacion>/vera-llm:0.40.10
docker tag vera-voz:local ghcr.io/<organizacion>/vera-voz:1.2.1
docker tag vera-kokoro:local ghcr.io/<organizacion>/vera-kokoro:0.6.1
docker push ghcr.io/<organizacion>/vera-llm:0.40.10
docker push ghcr.io/<organizacion>/vera-voz:1.2.1
docker push ghcr.io/<organizacion>/vera-kokoro:0.6.1
```

Publicar no cuesta. Levantar una VM con GPU sí. Por eso esta sesión construye y valida en local y
no publica ni contrata nada. Las imágenes se promueven por digest con R-05.

## Por qué la imagen no lleva pesos

Un GGUF de varios GB dentro de la capa duplicaría cada versión del razonador y ataría el despliegue a
un binario concreto. Montado, el digest de la imagen fija el software y `modelos/lock.json` fija los
pesos. Cambiar uno no obliga a rehacer el otro. La voz aplica la misma regla con la caché de Hugging
Face. `vera-kokoro` es la excepción: sus 0,14 GB van dentro de la imagen, con huella verificada en el
build, porque no justifican un volume aparte (ver `docs/pila-modelos-pod.md`).

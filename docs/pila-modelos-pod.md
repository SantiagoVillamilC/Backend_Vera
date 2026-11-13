# Pila de modelos del pod

Tres imágenes y un compose para el pod con GPU: razonador, transcripción y voz neural. Todo escucha
en loopback y los secretos llegan por entorno.

## Servicios

| Servicio | Imagen | Puerto (loopback) | Qué hace | Pesos |
|----------|--------|-------------------|----------|-------|
| `vera-llm` | `vera-llm:local` | 11434 | Razonador E4B con Ollama 0.40.10 | 4,98 GB, volume `pesos` |
| `vera-voz` | `vera-voz:local` o `vera-voz:cuda` | 8767 | Transcripción con faster-whisper `small` | 0,5 GB, volume `pesos` |
| `vera-kokoro` | `vera-kokoro:local` | 8766 | Voz neural Kokoro-82M, voz `ef_dora` | 0,14 GB, dentro de la imagen |

Los tres usan `network_mode: host` para conservar el invariante de loopback: los servidores exigen
escuchar en `127.0.0.1` y no hay puertos publicados en interfaces externas.

Conflicto conocido: `vera-llm` ocupa el puerto 11434 del host porque usa `network_mode: host`. Si hay un
Ollama nativo escuchando en ese puerto, el contenedor no puede levantar. En la VPS de trabajo hay un
Ollama 0.40.10 nativo en ese puerto, según `docs/e2e-voz-local-2026-11-13.md`. En un pod limpio no aplica.

Archivos: `infra/modelos/compose.yaml` (base), `infra/modelos/compose.cuda.yaml` (override de GPU),
`infra/modelos/vera-llm/Dockerfile`, `infra/modelos/vera-voz/Dockerfile`,
`infra/modelos/vera-voz/Dockerfile.cuda` y `infra/modelos/vera-kokoro/Dockerfile`.

## Por qué Kokoro va aparte

- Licencia: el sidecar de fonemización (`phonemizer`) es GPL-3.0. Dentro de `vera-kokoro` queda
  confinado a esa imagen y no contamina a `vera-voz`.
- Aislamiento de fallos: si el sintetizador cae, Whisper sigue transcribiendo. No hay `depends_on`
  entre los servicios por esa misma razón.
- Reutilización: el Dockerfile copia `servidor.py`, `descargar.py` y `requirements.txt` de
  `infra/voz-local` sin modificarlos, así que el contrato de la voz no cambia.

Una diferencia a tener presente: Kokoro descarga sus pesos durante el build, con SHA256 y tamaño
verificados por `descargar.py` (mismos valores que `modelos/lock.json`), y por eso van dentro de la
imagen. Con 0,14 GB no justifica un montaje aparte. El razonador y Whisper sí viven en el volume.

## Volumen y pesos

Un volumen de 30 GB montado en `/pesos`. Contenido: GGUF del razonador en `/pesos/modelos` y caché de
Hugging Face de Whisper en `/pesos/huggingface`.

| Peso | Tamaño |
|------|--------|
| E4B (`gemma-4-e4b-it-q4_k_m`) | 4,98 GB |
| Whisper `small` | 0,5 GB |
| Kokoro (en la imagen) | 0,14 GB |

El total ronda 5,6 GB, así que 30 GB deja margen amplio para cambiar de modelo sin repoblar.

Para usar E4B hay que fijar `OLLAMA_MODELO=gemma-4-e4b-it-q4_k_m`: el valor por defecto del compose
sigue siendo E2B.

## Variables de entorno

| Variable | Servicio | Nota |
|----------|----------|------|
| `WHISPER_TOKEN` | `vera-voz` | Obligatoria, mínimo 32 caracteres. |
| `KOKORO_TOKEN` | `vera-kokoro` | Obligatoria, mínimo 32 caracteres. El servidor la lee como `VERA_LAB_TOKEN`. |
| `OLLAMA_MODELO` | `vera-llm` | `gemma-4-e4b-it-q4_k_m` en el pod. |
| `WHISPER_GPU` | `vera-voz` | `no` (CPU int8) en la base, `si` en la variante CUDA. |

Los valores salen de Infisical o del archivo del operador. Nunca se escriben en el compose ni en la
imagen. Sin `WHISPER_TOKEN` o sin `KOKORO_TOKEN`, `docker compose config` falla a propósito.

`VERA_VOZ_TOKEN` no pertenece al compose: lo lee la API (`cmd/api/main.go`) y debe tener el mismo valor
que `KOKORO_TOKEN`. La API lo envía como `X-Vera-Token` a `VERA_VOZ_URL` (por defecto
`http://127.0.0.1:8766`, el puerto de `vera-kokoro`), y el servidor lo compara con `VERA_LAB_TOKEN`. Si no
coinciden, el healthcheck de `vera-kokoro` pasa igual, porque usa `KOKORO_TOKEN`, pero kokoro responde 401
a la API y la síntesis queda no disponible. El healthcheck no lo detecta. Los dos tokens deben tener al
menos 32 caracteres.

## Variante CUDA de vera-voz

Estado: **sin construir ni ejecutar: solo el compose fue validado con `docker compose config`.**

No se construyó ni se ejecutó en la VPS de trabajo porque no hay runtime NVIDIA. Se construye en el
pod. Lo que sí se validó aquí es que el compose con el override se interpreta bien con
`docker compose config`. El tag `nvidia/cuda:12.4.1-cudnn-runtime-ubuntu22.04` existe en el registro,
pero la imagen no se descargó, así que la instalación de dependencias sobre esa base está sin probar.

- `Dockerfile.cuda`: base CUDA 12.4 con cuDNN 9 (lo que pide ctranslate2 para faster-whisper 1.2.1),
  mismo servidor y mismo contrato que la imagen de CPU, `WHISPER_GPU=si`. Con `si` el servidor falla
  al cargar si no ve la tarjeta, en vez de caer a CPU sin avisar.
- `compose.cuda.yaml`: cambia la imagen de `vera-voz` a `vera-voz:cuda` y da `gpus: all` a `vera-llm` y
  `vera-voz`. `vera-kokoro` se queda en CPU.

## Orden de arranque

1. Montar el volume `pesos` y exportar `WHISPER_TOKEN`, `KOKORO_TOKEN` y `OLLAMA_MODELO`.
2. `vera-llm`: es el más lento. El servidor arranca rápido, pero el GGUF se carga en el primer
   `/api/generate`, así que conviene un humo corto antes de dar el pod por listo.
3. `vera-voz`: carga Whisper antes de escuchar. El healthcheck tiene 120 s de gracia. En el primer
   arranque con volume vacío incluye la descarga del modelo.
4. `vera-kokoro`: carga ONNX y calienta una frase antes de abrir el puerto. No se midió cuánto tarda.

Comando en el pod:

```bash
WHISPER_TOKEN=... KOKORO_TOKEN=... OLLAMA_MODELO=gemma-4-e4b-it-q4_k_m \
  docker compose -f infra/modelos/compose.yaml -f infra/modelos/compose.cuda.yaml up --build
```

Compose levanta los tres en paralelo, porque no hay `depends_on`. El orden de arriba es el de
verificación: confirmar `healthy` en ese orden y no abrir la sesión hasta que los tres lo estén.

## Terminate entre sesiones

Entre sesiones el pod se termina (Terminate) para no pagar GPU ociosa. Lo que persiste es el volumen
de 30 GB, siempre que sea un volumen de red del proveedor y no el disco local del pod, cosa que hay
que confirmar al crearlo. Lo que se pierde con el pod: las imágenes construidas, que se reconstruyen
(o se publican por digest, ver `docs/imagenes-modelos.md`), y el estado en memoria.

El siguiente arranque no vuelve a descargar los pesos del razonador ni de Whisper. Kokoro los
descarga de nuevo con cada build, unos 0,14 GB.

## Validación hecha

- `docker compose -f infra/modelos/compose.yaml config` con tokens de prueba: válido, tres servicios.
- Igual con `-f infra/modelos/compose.cuda.yaml`: válido, `vera-voz` apunta a `Dockerfile.cuda`.
- Sin `KOKORO_TOKEN`: falla con el mensaje esperado.
- No se ejecutó ningún `build` ni `up`.

# Pila de modelos del pod

> **Aviso (15 de noviembre de 2026): en un pod de RunPod no se puede usar Docker ni Compose.**
> RunPod no admite Docker ni Compose dentro de un pod (documentación de RunPod, Pods, Limitations).
> Todo lo que este documento describe con `docker compose` (imágenes, `compose.yaml`,
> `compose.cuda.yaml`, `infra/modelos/pod-demo.sh`, `infra/modelos/deploy-modelos.sh`) sirve para un
> host con Docker propio y no para un pod de RunPod. La campaña de capacidad corrió los tres servicios como procesos nativos. Detalle en
> la última sección.

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

No se construyó ni se ejecutó en la VPS de trabajo porque no hay runtime NVIDIA. En un pod de RunPod
tampoco se puede construir, porque no admite Docker (ver el aviso del inicio): solo aplica a un host
con Docker y runtime NVIDIA propio. Lo que sí se validó aquí es que el compose con el override se interpreta bien con
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

Comando en un host con Docker (no sirve dentro de un pod de RunPod, ver el aviso del inicio):

```bash
WHISPER_TOKEN=... KOKORO_TOKEN=... OLLAMA_MODELO=gemma-4-e4b-it-q4_k_m \
  docker compose -f infra/modelos/compose.yaml -f infra/modelos/compose.cuda.yaml up --build
```

Compose levanta los tres en paralelo, porque no hay `depends_on`. El orden de arriba es el de
verificación: confirmar `healthy` en ese orden y no abrir la sesión hasta que los tres lo estén.

## Paralelismo de los servidores

Por defecto cada servicio atiende una solicitud a la vez y responde 409 si está ocupado. Los clientes
de la API dependen de eso (STT no reintenta, TTS reintenta 4 veces cada 500 ms), así que con los
valores por defecto nada cambia. Las variables siguientes suben el paralelismo sin tocar el contrato.

| Variable | Servicio | Rango | Por defecto | Efecto |
|----------|----------|-------|-------------|--------|
| `OLLAMA_NUM_PARALLEL` | `vera-llm` | entero de Ollama | 1 | Solicitudes simultáneas por modelo cargado |
| `OLLAMA_MAX_QUEUE` | `vera-llm` | entero de Ollama | 512 | Cola de Ollama antes de rechazar |
| `WHISPER_CONCURRENCIA` | `vera-voz` | 1 a 16 | 1 | Transcripciones simultáneas. Con más de 1 el modelo se carga con `num_workers` igual a ese valor |
| `WHISPER_ESPERA_MS` | `vera-voz` | 0 a 10000 | 0 | Cuánto espera una solicitud por un cupo antes del 409. Con 0 el 409 es inmediato |
| `VERA_VOZ_ESPERA_MS` | `vera-kokoro` | 0 a 10000 | 0 | Igual que la anterior, sobre el cerrojo de Kokoro |
| `VERA_VOZ_PUERTO` | `vera-kokoro` | 1024 a 65535 | 8766 | Puerto del proceso, siempre en `127.0.0.1` |

Un valor vacío equivale a no definirlo. Un valor fuera de rango o no entero hace que el servidor
aborte al arrancar con un mensaje claro, antes de cargar el modelo.

- Los valores por defecto de Ollama (1 y 512) se leyeron del log de arranque de `ollama/ollama:0.40.10`.
- Kokoro no gana concurrencia dentro de un proceso: no se verificó que el fonemizador (espeak) sea
  seguro entre hilos. Para paralelizar se levantan varios procesos, cada uno con su
  `VERA_VOZ_PUERTO`, y el cliente reparte entre ellos. `compose.yaml` no pasa `VERA_VOZ_PUERTO` porque
  el healthcheck de la imagen y el del compose consultan el 8766 fijo. Con otro puerto hay que
  cambiar ambos.
- En CPU, `num_workers` mayor que 1 multiplica los hilos de cómputo de Whisper. No se midió el
  efecto. En GPU tampoco se midió la ganancia real de `num_workers`.
- `WHISPER_ESPERA_MS` y `VERA_VOZ_ESPERA_MS` mantienen abierta la conexión del cliente mientras
  esperan. El cliente de STT no reintenta, así que una espera mayor que su propio timeout se pierde.
- Con una espera mayor que 0, el hilo del servidor puede transcribir o sintetizar para un cliente que
  ya se desconectó. El servidor no detecta la desconexión mientras espera el cupo ni mientras
  procesa: el trabajo se completa y la respuesta se descarta al escribirla (`BrokenPipeError`). Ese
  cómputo ocupa un cupo que otro cliente vivo podría haber usado. Con la espera en 0 el rechazo es
  inmediato y este desperdicio no ocurre antes de adquirir el cupo.
- Qué paralelismo aguanta cada modelo en el pod y cuánto sube la latencia se resume en la sección
  "Capacidad medida y hallazgos de la campaña del 15 de noviembre de 2026" de este documento. El
  detalle está en el informe de la tesis `modelos-gemma/resultados-capacidad-2026-11-15.md` (el plan
  era `modelos-gemma/plan-pruebas-capacidad-2026-11-14.md`, repositorio de la tesis). Estas variables
  solo exponen la palanca.

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
- Con las variables de paralelismo: `docker compose config` válido con y sin `compose.cuda.yaml`, y los
  valores se interpolan (por ejemplo `OLLAMA_NUM_PARALLEL=4`). Tampoco se ejecutó `build` ni `up`.

## Capacidad medida y hallazgos de la campaña del 15 de noviembre de 2026

Fuente de verdad: informe de la tesis `modelos-gemma/resultados-capacidad-2026-11-15.md` y sus datos
en `modelos-gemma/carga/` (repositorio de la tesis). Marcas: **[V]** medido o verificado el 15 de
noviembre de 2026, **[I]** inferido. Se corrieron 39 de las 45 celdas prerregistradas, **una vez cada
una**, en un pod RunPod Secure con RTX 4090 de 24 GB. Todas las cifras de capacidad son estimaciones
de una sola corrida por celda.

### Cómo corrió la pila

- [V] RunPod no admite Docker ni Compose dentro de un pod. La campaña corrió Ollama 0.40.10 con
  `gemma4-e4b-local` (E4B de 4 bits), Whisper `small` y Kokoro como procesos nativos, estos dos con
  los servidores del PR 83. Las imágenes y el compose no se probaron en GPU.
- [V] `infra/modelos/pod-demo.sh` usa `docker compose` y por eso no sirve dentro de un pod de RunPod.
  Queda con una advertencia en su cabecera y sin cambios de lógica.
- [V] `nproc` dentro del pod mostró 32 vCPU. VRAM con `OLLAMA_NUM_PARALLEL=8`: 6 737 MiB estables y
  picos de 7 128 MiB (6,96 GiB, cifra del informe), con Whisper cargado. Con P=1 fueron 4 811 MiB.

### Capacidad de texto

SLO del producto: éxito de turno de al menos 99 % y p95 de turno menor a 30 s. La tasa es la
**realizada** (turnos medidos sobre la duración de la ventana), no la configurada.

| Configuración | Turnos/s realizados | Respaldo | Usuarios de texto con Z=30 s [I] |
|---------------|---------------------|----------|----------------------------------|
| P=8, `B3-final-80` (529 de 529) | 0,59 | **Demostrado** con 95 % de confianza | unos 23 |
| P=8, `B3-pelegido-x070` (274 de 274) | 0,83 | Cumple puntualmente, sin respaldo estadístico | unos 31 |
| P=1, `B3-p1-x085` (274 de 274) | 0,28 | Cumple puntualmente, sin respaldo estadístico | unos 10 |
| P=8 en torno a 1,0 y P=1 en torno a 0,30 | | Falla el SLO | sin capacidad |

- [V] Un stream va a unos 150 tok/s con E4B (el informe da una mediana por stream de 151 a 152 y un
  agregado de 145 con P=1). Con P=8 y
  2 a 4 streams en vuelo baja a 57 a 90 tok/s por stream (46 a 50 con 5 a 7 en vuelo). Es diagnóstico y no criterio del SLO.
- [V] En estimación puntual, P=8 triplica la capacidad de P=1 (0,83 contra 0,28 turnos/s realizados,
  2,9 veces). Solo hasta 0,59 turnos/s en P=8 está demostrada con 95 % de confianza.
- [V] P=16 no suma capacidad de turnos (1,10 contra 1,12 turnos/s) y rompe el SLO (97 % de éxito, p95
  de 36,7 s en `B2-p16-c16`).
- [V] Los usuarios de la tabla salen de `usuarios = λ x (R + Z)` y Z no tiene datos empíricos [I]. La
  frontera de P=8 queda entre 0,83 y 1,0 turnos/s realizados, sin celda intermedia.
- **`OLLAMA_NUM_PARALLEL=8` queda como recomendación condicionada**: faltan las 3 repeticiones
  prerregistradas, con reinicio de Ollama entre ellas, y la velocidad por stream de P=8 alternó entre
  dos regímenes sin causa identificada (`B2-p8-c2`). **El valor por defecto actual del compose sigue
  siendo 1** y esta documentación no lo cambia.
- No se midió la calidad de las respuestas bajo carga.

### La voz es el cuello de botella

- [V] Whisper y Kokoro no tienen cola: responden 409 si están ocupados. Con 1 Whisper y 1 Kokoro, a 12
  usuarios (Z=10 s) fallaron 55 % de las cadenas completas (`B4-cadena-z10`), y con 24 y 43 usuarios
  (Z=30 y 60 s) fallaron 58,5 % y 60,5 %. La etapa que falla es la síntesis (TTS).
- [V] Kokoro satura en 0,45 solicitudes/s con 1 instancia (1,3 con 4). Whisper satura en unas 12
  solicitudes/s con 1 instancia, pero sin cola rechazó 42 % de los primeros intentos a 80 % de su
  capacidad cerrada.
- [V] El servicio de Kokoro crece unos 0,035 s por carácter (r de 0,99), y se midió solo con textos de
  56 caracteres de media (19 a 92). Con respuestas reales del modelo la capacidad es peor. Con el LLM
  saturado, TTS sube 1,7 veces su tiempo de servicio, según una celda incompleta (`B4-tts-con-llm`,
  56 de 200 solicitudes).
- **No se recomienda un número de instancias de Kokoro** hasta medirlo con respuestas reales del
  modelo. Las cifras de Erlang B del informe son una cota superior optimista y sirven solo para
  comparar configuraciones. La concurrencia interna de Whisper y la espera acotada del
  PR 83 (`WHISPER_ESPERA_MS`, `VERA_VOZ_ESPERA_MS`) no se midieron, solo la multi-instancia.

### Operativos de RunPod

Estos puntos se observaron durante la campaña. Los que no figuran en el informe de la tesis salen del
encargo de documentación y se marcan como tales.

- [V] La clave que RunPod inyecta en el pod autentica con 403 en `GET /pods/{id}` (dato del encargo).
  El pod no puede terminarse a sí mismo con esa clave, así que la terminación se hace desde fuera.
- [V] `runpodctl exec` está deprecado, pierde la salida de scripts que terminan de inmediato y la de
  salidas grandes (más de unos 60 KB) (dato del encargo). El informe registra 262 errores de descarga
  de la vigía local por ese canal (`tamano distinto`, `runpodctl exec no devolvio resultado`). Para
  traer resultados se usó `ssh` y `rsync` con la clave de `~/.runpod/ssh`.
- [V] Seguridad de gasto: funcionó una guardia local con temporizador de systemd más una vigía local,
  ambas independientes del pod, y el tope de presupuesto en la creación del pod (dato del encargo). La
  descarga automática de la vigía no funcionó (ver el punto anterior). Según el informe, una vigía arrancó a
  las 06:41 UTC con tope de 4,5 h (`vigia.log`). El informe no confirma que la vigía local actuara en la
  terminación: su último ciclo registrado fue a las 10:51:44 UTC con `corriendo: true`, y la
  terminación de las 11:11 UTC es un dato del encargo.

### Precios y costo

Los precios, la oferta y el costo real están en `docs/costos-pod-demo.md`. Resumen: RTX 4090 Secure a
0,89 USD/h, campaña de 4,65 h por 4,14 USD.

### Lo que no se verificó

- Repeticiones de cualquier celda, y P=2, P=4 y P=16 en carga abierta.
- Capacidad de TTS con respuestas reales del modelo y con más de 1 instancia en la cadena.
- La causa de `B2-p8-c2`, del escalado sublineal de la voz y de que la GPU no pase de 76 % de uso.
- Que el compose y las imágenes funcionen en GPU en un host con Docker (la campaña no los usó).

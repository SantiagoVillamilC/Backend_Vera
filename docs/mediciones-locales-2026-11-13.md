# Mediciones locales de hardware, 2026-11-13

Corridas en `arch-asus` con el worktree `Backend_Vera-local`, rama `feat/local-hardware`, sobre
`d71589b`. No se modificaron pesos, no se publicó nada y no se levantó ninguna VM.

## Whisper small

- 24 clips sintéticos, 260 palabras de referencia, 21 ediciones, **WER 0,0808**.
- Tope implementado: 0,10. El WER 0,0808 cumple.
- Reproduce exactamente la línea base del 27-10-2026: cero diferencias por clip, mismas 6
  ediciones por formato de cifra y mismo WER léxico de 0,0577.
- CPU int8, `faster-whisper` 1.2.1, `av` 16.1.0, Python 3.12.14.
- `factor_tiempo_real_global` 7,779 frente a 3,174 de la línea base. La corrida tuvo una
  compilación cruzada de kernel aarch64 y carga media de 17 a 20 sobre 8 núcleos: es una medida
  contaminada, no una regresión. `segundos_carga` 88,35 s porque el modelo se descargó en esa
  corrida.

Comando reproducible:

```bash
out/hardware-2026-11-13/stt-venv/bin/python \
  modelos-gemma/evaluacion/wer_local.py \
  --salida out/hardware-2026-11-13/wer-local-2026-11-13.json
```

## Ollama nativo con Gemma 4 E2B

- Ollama 0.40.10 sin Docker, GTX 1650 de 4 GB, `gemma4-e2b-local` ya creado.
- 36 de 36 capas en GPU, 1.808 MB de 4.096 MB de VRAM, `num_ctx` 8192 servido.
- Primera petición con thinking activo y `num_predict` 64: respuesta vacía por
  `done_reason: length`. En caliente, misma petición: 4.214 ms y `ok`. Con `think:false`:
  1.007 ms y `ok`.
- Arranque en frío: 34 s de carga. Con `keep_alive` de 30 m se evita en turnos seguidos.
- Decisión pendiente para el gateway: enviar `think:false` o subir `num_predict` y filtrar el
  bloque de razonamiento.

## Imagen `vera-llm`

- Construida desde `infra/modelos/vera-llm/Dockerfile` con base `ollama/ollama:0.40.10`.
- Imagen `sha256:6d9d08510478ee69b2854f02b659c7a8427b11e7384571063ee5548c76be3128`,
  8,42 GB, sin pesos ni secretos dentro.
- `docker compose config` con token de prueba: válido. `docker run --rm vera-llm:local --version`:
  0.40.10.
- No se probó el servidor con un modelo cargado dentro del contenedor.

## Compuerta de transcripción

- `infra/stt-local/clips/`: 24 WAV, 3.582.059 bytes, copia intacta del set sintético.
- `infra/stt-local/clips/manifiesto.json`: 24 entradas con id, familia, texto, archivo relativo y
  duración; suma 74,51 s.
- `infra/stt-local/medir_wer.py`: faster-whisper small CPU int8, español, beam 1, VAD; normaliza
  y calcula WER por totales; imprime JSON y sale 1 si supera el tope.
- Corrida local histórica: `{"n": 24, "wer": 0.0808, "tope": 0.15, "pass": true}`, exit 0. El medidor y el job ahora usan 0,10; el mismo WER cumple.
  Prueba contraria con `--tope 0.0`: `pass false`, exit 1.
- `.github/workflows/calidad-modelos.yml`: job `transcripcion` con timeout 15 min, instalación de
  `faster-whisper==1.2.1`, `av==16.1.0` y `numpy`, y la medición contra el manifiesto.

## Humo del razonador local

- Comando: `python3 evaluar.py --modelo gemma4-e2b-local --por-familia 1 --think default --etiqueta humo-local-e2b` sobre los 200 casos congelados, 12 casos (uno por familia A-L).
- Resultado E2B local: **7/12 (58,3 %)**, por debajo del umbral de 9/12. Fallan B, E, F, G y H con `efecto_ok`; cero errores de cliente; mediana 20,5 s.
- El E2B no pasa la compuerta de razonador en esta máquina. El 4090 debe correr E4B (cabe en 24 GB) para el humo y la demo; el E2B queda como equipo de desarrollo.
- Resultados crudos: `modelos-gemma/evaluacion/resultados/humo-local-e2b/`.

## Pendientes

- Los jobs `voz` y `transcripcion` ya corrieron en GitHub Actions (run 43454401867) y salieron
  verdes; el card CF-A5-04 sigue parcial porque faltan el humo del razonador de 12 casos y la
  compuerta de release, que necesitan GPU. El card pide WER 0,10 y el gate ya usa 0,10.
- `vera-voz:local` se construyó y el E2E real pasó; ver `docs/e2e-voz-local-2026-11-13.md`.
- Cerrar los hallazgos menores del proveedor compuesto y ajustar el `409` de la corrida combinada
  (usar `-p 1` o reintento).
- Publicar imágenes y pesos como OCI en GHCR y cablear el despliegue por digest.

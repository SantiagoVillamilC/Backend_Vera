# E2E de voz local, 2026-11-13

Evidencia de extremo a extremo sobre hardware propio: Whisper real en un contenedor, un turno de
audio transcrito y un turno de chat servido por Ollama. Corrido en el worktree
`Backend_Vera-local`, rama `feat/local-hardware`, sin push.

## Imagen y contenedor

- Imagen: `vera-voz:local`, digest `sha256:2b623a800a936df3a263b308f76389c1f7ef890bb6373dcd71f4ed95e36f2528`.
- Construida desde `infra/modelos/vera-voz/Dockerfile` sobre `python:3.12-slim-bookworm`, con
  `faster-whisper` 1.2.1 y `ctranslate2` 4.9.0. La imagen no lleva pesos ni secretos.
- Contenedor: `vera-voz-e2e`, id `5bb9c5ffd705d82ca5ad459e801475d3623d1b26ae8377bb38b42776e0d3db4e`,
  `running`, `restarts=0`, health nativo `healthy`, red `host` escuchando en `127.0.0.1:8767`.
- Caché de pesos montada desde `/home/chris/.cache/huggingface` en `/pesos/huggingface`.
- Solo se recreó `vera-voz-e2e`. `hub-postgres` y `waffles-dev-pg` siguen intactos.
- Estado final: `vera-voz-e2e` se eliminó al terminar la fase de cierre; no queda escuchando.

## Salud

- `GET /healthz` con `X-Vera-Token`: `200` a los 3.0 s de arranque, cuerpo
  `{"transcripcion": true, "modelo": "small"}`.
- Sin cabecera de token: `401`. La autenticación se exige de verdad.
- El token vive solo en `out/e2e-2026-11-13/whisper-token` en modo 600 y no aparece en este
  documento, ni en la salida guardada, ni en los logs del contenedor.
- Era un token efímero y se eliminó al cerrar. Un `docker inspect` volcó el valor en la salida
  cruda de la sesión de la herramienta, no en archivos versionados ni logs del contenedor.

## Transcripción real

- Clip: `infra/stt-local/clips/000_eva-A-001.wav`, mono, 24 kHz, 3.88 s, 186.412 bytes.
- Salida: "en los últimos tres meses a cuanto haciéndolo pagado en el Internet hogar."
- Latencia de la reejecución aislada con `-count=1`: 6.384 ms en caliente.
- La primera petición tras el arranque del contenedor tardó 10.558 ms porque incluyó la carga
  del modelo. No se toma como latencia de régimen.
- WER contra el manifiesto con la normalización de `medir_wer.py`: 14 palabras de referencia, 2
  ediciones, 0,143. Supera el tope agregado 0,10 en ese clip aislado, pero la compuerta se mide sobre los 24 clips; el agregado 0,0808 sí cumple.

## Chat compuesto

- `compuesto.Nuevo` une `sttlocal` y `ollama`. Un turno de audio con `SoloTranscripcion` devuelve
  JSON válido con la clave `transcripcion` y el texto de arriba.
- Turno de texto "Responde solo: ok": `chat="ok"`, 645 ms en caliente, 29 tokens de entrada y 2
  de salida, `resultado=ok`.
- La primera corrida con el modelo entrando en memoria tardó 17.878 ms en el chat; no es régimen.

## Versión y modelo de Ollama

- Ollama 0.40.10 nativo en `127.0.0.1:11434`, sin Docker.
- Modelo: `gemma4-e2b-local:latest`, id `06a7eb0742bc`, 3.1 GB.
- Cliente `ollama.Nuevo` con plazo de 45 s y temperatura 0.

## Condiciones

- CPU int8, sin GPU, `WHISPER_GPU=no`, modelo `small`.
- El servidor de transcripción atiende una petición a la vez con un cerrojo no bloqueante y
  responde `409` si otra está en curso. En la primera corrida combinada de esta fase el test de
  `sttlocal` recibió ese `409` a los 0,02 s porque solapó con una petición anterior todavía en
  vuelo. La reejecución aislada pasó sin incidentes y el estado no se repitió.
- Plazos del cliente: 90 s para transcripción, 45 s para chat. El `409` se mapea a
  `ErrLimiteIA`, no a una falla de red.

## Verificación

- `gofmt -w` sobre los dos tests; `gofmt -l` sin salida.
- `go test ./internal/copilot/compuesto ./internal/copilot/sttlocal`: pasa.
- `go test -p 1 -tags e2elocal -count=1 -v ./internal/copilot/sttlocal ./internal/copilot/compuesto`:
  `TestE2ELocalCompuesto` PASS en 6,61 s; `TestE2ELocalTranscribir` PASS en 6,39 s. Sin `-p 1`,
  las pruebas de paquetes distintos se solapan y el servidor responde `409` por su cerrojo de una
  petición a la vez; es un límite del servidor, no del cliente.
- `go vet ./internal/copilot/compuesto ./internal/copilot/sttlocal` y con `-tags e2elocal`: sin
  salida.
- `go build ./...`: compila.
- Salida de las corridas redactada en `out/e2e-2026-11-13/C1-e2e-test-output.txt`, con
  verificación por script de que el token no aparece.
- `out/` está ignorado por `.gitignore:19`. En esta fase se versionan solo los dos tests nuevos y
  este documento.

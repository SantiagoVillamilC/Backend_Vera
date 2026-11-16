#!/usr/bin/env bash
# infra/modelos-runpod/pod/arrancar-modelos.sh
# Prepara y arranca los tres modelos de Vera DENTRO de un pod de RunPod ya creado (plantilla PyTorch con
# CUDA y sshd, sin Docker). Lo ejecuta modelos-control por ssh. Es idempotente: si los tres servicios ya
# responden con los tokens vigentes sale con 0 sin tocar nada (salvo VERA_FORZAR=1).
#
# Deja tres procesos nativos, todos en 127.0.0.1 del pod:
#   Whisper small (GPU)    8767   exige X-Vera-Token = WHISPER_TOKEN
#   Kokoro fp32 (CUDA)     8766   exige X-Vera-Token = VERA_LAB_TOKEN
#   Ollama + gemma4-e4b    11434  sin token (solo escucha en loopback)
#
# Derivado y simplificado de modelos-gemma/carga/pod/bootstrap_pod.sh y levantar_servicios.sh de la tesis.
# Se quitó lo propio de la campaña de medición (afinidad de CPU con taskset, tiempos TSV, ABORTO_BOOTSTRAP)
# y se conservó lo que dio problemas reales: compuerta de CUDA, sha256 del GGUF y de los pesos de Kokoro,
# archivos .parcial con mv atómico, pip --only-binary=:all:, tokens fuera de argv y orden de arranque.
#
# Uso:
#   bash arrancar-modelos.sh              prepara y arranca todo
#   bash arrancar-modelos.sh --seco       imprime el plan y las variables efectivas, no ejecuta nada
#   bash arrancar-modelos.sh --compuerta  solo comprueba herramientas y la compuerta de CUDA (sin root ni red)
#
# Entradas (todas opcionales salvo el archivo de tokens y el origen del código):
#   VERA_TOKENS_ARCHIVO    archivo 0600 con WHISPER_TOKEN y VERA_LAB_TOKEN (32 caracteres o más).
#                          Lo entrega modelos-control por ssh con "umask 077" y stdin, nunca en argv.
#   VERA_SERVIDORES_DIR    carpeta con stt-local y voz-local (variante GPU de Kokoro). Si falta, se baja de
#                          GitHub el commit VERA_CODIGO_SHA (40 hex) del repositorio VERA_REPO.
#   OLLAMA_NUM_PARALLEL    por defecto 2 (demo). Medido en una 4090: P=8 aguanta 8 usuarios, P=2 usa 5 GB de VRAM.
#   OLLAMA_KEEP_ALIVE      por defecto 24h
#   WHISPER_GPU            por defecto si (con "auto" un fallo de CUDA bajaría a CPU en silencio)
#   PIP_EXTRA_STT          requisitos extra de Whisper con versión exacta (por ejemplo bibliotecas cuDNN)
#   VERA_ESTADO_DIR, VERA_VENV_DIR, VERA_GGUF_DIR, HF_HOME, OLLAMA_MODELS, PYTHON_BIN, NVIDIA_SMI
#   VERA_FORZAR=1          reinicia los servicios aunque ya estén sanos
#
# Progreso: una línea JSON por evento en $VERA_ESTADO_DIR/progreso.jsonl (ver docs/despliegue-modelos-runpod-vm.md).
#   {"epoch": N, "paso": "...", "estado": "en_curso|ok|fallo|listo", "codigo": N, "detalle": "..."}
# La última línea es siempre "listo" o "fallo". Cada línea se escribe con un solo printf en modo append.
#
# Códigos de salida (contrato con modelos-control):
#   0 listo   1 fallo genérico   2 entrada inválida (tokens, variables, código)   3 sha256 distinto
#   4 un servicio no quedó sano a tiempo   13 Ollama no cabe entero en VRAM
#   14 el driver no ofrece CUDA 13.0 (terminar el pod y crear otro en otro host)
#   15 Kokoro abortó porque CUDAExecutionProvider no cargó (mismo remedio que 14)
set -euo pipefail

ESTADO_DIR="${VERA_ESTADO_DIR:-/workspace/vera-estado}"
PROGRESO="${ESTADO_DIR}/progreso.jsonl"
LOGS="${ESTADO_DIR}/logs"
PIDS="${ESTADO_DIR}/pids"
VENV_DIR="${VERA_VENV_DIR:-/workspace/venv}"
DIR_SERV="${VERA_SERVIDORES_DIR:-/workspace/vera/servidores}"
TOKENS_ARCHIVO="${VERA_TOKENS_ARCHIVO:-${ESTADO_DIR}/tokens.env}"
CODIGO_SHA="${VERA_CODIGO_SHA:-}"
REPO="${VERA_REPO:-SantiagoVillamilC/Backend_Vera}"
GGUF_DIR="${VERA_GGUF_DIR:-/workspace/modelos/gemma4}"
export HF_HOME="${HF_HOME:-/workspace/hf}"
export OLLAMA_MODELS="${OLLAMA_MODELS:-/workspace/ollama-modelos}"
export HF_HUB_DISABLE_TELEMETRY=1 PIP_DISABLE_PIP_VERSION_CHECK=1
NVIDIA_SMI="${NVIDIA_SMI:-nvidia-smi}"
WHISPER_GPU="${WHISPER_GPU:-si}"
OLLAMA_NUM_PARALLEL="${OLLAMA_NUM_PARALLEL:-2}"
OLLAMA_KEEP_ALIVE="${OLLAMA_KEEP_ALIVE:-24h}"
FORZAR="${VERA_FORZAR:-0}"

OLLAMA_VERSION="0.40.10"
OLLAMA_HOST_PORT="127.0.0.1:11434"
MODELO_OLLAMA="gemma4-e4b-local"
NUM_CTX=8192
PUERTO_WHISPER=8767
PUERTO_KOKORO=8766
# Revisión de Hugging Face fijada al commit visto el 2026-11-14. El sha256 coincide con el oid LFS de ese
# commit y con modelos/lock.json. Si el lockfile cambia, estos tres valores cambian con él.
GGUF_REVISION="bfc15c382204943c3a8fff0c750b94ae2364d7a3"
GGUF_NOMBRE="gemma-4-E4B-it-Q4_K_M.gguf"
GGUF_URL="https://huggingface.co/unsloth/gemma-4-E4B-it-GGUF/resolve/${GGUF_REVISION}/${GGUF_NOMBRE}"
GGUF_SHA256="85a896a047553e842f25297ee5b031d64ff30147d9c4af17b1e4b394cd1fab87"
GGUF_BYTES=4977171584
GGUF_RUTA="${GGUF_DIR}/${GGUF_NOMBRE}"
# onnxruntime-gpu 1.31.0 trae CUDA 13.0 y cuDNN 9: el driver del host debe ofrecer 13.0 o más.
CUDA_MINIMA_MAYOR=13
CUDA_MINIMA_MENOR=0
MODELOS_VOZ="/opt/voz/modelos"   # servidor.py de Kokoro tiene esta ruta fija: no se cambia

PASO_ACTUAL="inicio"
LINEA_FINAL="no"
WHISPER_TOKEN=""
VERA_LAB_TOKEN=""

json_escapar() {
  local t="$1"
  t="${t//\\/\\\\}"
  t="${t//\"/\\\"}"
  t="${t//$'\n'/ }"
  printf '%s' "${t//$'\t'/ }"
}

# progreso PASO ESTADO [CODIGO] [DETALLE]: una línea JSON, un solo printf en modo append.
progreso() {
  mkdir -p "$ESTADO_DIR"
  printf '{"epoch": %s, "paso": "%s", "estado": "%s", "codigo": %s, "detalle": "%s"}\n' \
    "$(date +%s)" "$1" "$2" "${3:-0}" "$(json_escapar "${4:-}")" >> "$PROGRESO"
}

paso() {
  PASO_ACTUAL="$1"
  progreso "$1" en_curso 0 "${2:-}"
  printf '==> %s\n' "$1"
}

ok() { progreso "$1" ok 0 "${2:-}"; }

fallar() {
  local codigo="$1" paso_fallo="$2" motivo="$3"
  progreso "$paso_fallo" fallo "$codigo" "$motivo"
  LINEA_FINAL="si"
  printf '[error] %s: %s\n' "$paso_fallo" "$motivo" >&2
  exit "$codigo"
}

# Garantiza que progreso.jsonl termine en "listo" o "fallo" aunque un comando falle sin fallar() (set -e).
al_salir() {
  local codigo=$?
  if [[ "$codigo" -ne 0 && "$LINEA_FINAL" != "si" ]]; then
    progreso "$PASO_ACTUAL" fallo "$codigo" "salida inesperada con código ${codigo}"
  fi
}
trap al_salir EXIT

# curl_con_token TOKEN ARGS...: la cabecera viaja por la configuración de curl en stdin (-K -) y no por la
# línea de comandos: un -H con el token se vería en ps de todo el pod. La here-string se escribe entera antes
# de lanzar curl, así que no hay carrera con SIGPIPE ni argv con el token.
curl_con_token() {
  local token="$1"
  shift
  curl -K - "$@" <<< "header = \"X-Vera-Token: ${token}\""
}

exigir_comandos() {
  local faltan=() c
  for c in "$@"; do
    command -v "$c" >/dev/null 2>&1 || faltan+=("$c")
  done
  if (( ${#faltan[@]} > 0 )); then
    fallar 1 "$PASO_ACTUAL" "faltan herramientas en el pod: ${faltan[*]}"
  fi
}

# Versión de CUDA que ofrece el driver, tal como la imprime la cabecera de nvidia-smi. Los drivers clásicos
# escriben "CUDA Version: 13.0" y los nuevos "CUDA UMD Version: 13.4". Se aceptan los dos.
leer_cuda_version() {
  local salida
  command -v "$NVIDIA_SMI" >/dev/null 2>&1 || return 1
  salida="$("$NVIDIA_SMI" 2>&1 || true)"
  if [[ "$salida" =~ CUDA\ (UMD\ )?Version\ *:\ *([0-9]+\.[0-9]+) ]]; then
    printf '%s' "${BASH_REMATCH[2]}"
    return 0
  fi
  return 1
}

leer_gpu() {
  local salida
  command -v "$NVIDIA_SMI" >/dev/null 2>&1 || return 1
  salida="$("$NVIDIA_SMI" --query-gpu=name,memory.total,driver_version --format=csv,noheader 2>/dev/null || true)"
  salida="${salida%%$'\n'*}"
  [[ -n "$salida" ]] || return 1
  printf '%s' "$salida"
}

cuda_suficiente() {
  local mayor="${1%%.*}" menor="${1#*.}"
  (( 10#$mayor > CUDA_MINIMA_MAYOR || (10#$mayor == CUDA_MINIMA_MAYOR && 10#$menor >= CUDA_MINIMA_MENOR) ))
}

# Primera comprobación, antes de bajar nada. SOLO lee la versión de la cabecera de nvidia-smi: no prueba que
# ONNX Runtime cargue el CUDAExecutionProvider. Esa compuerta real corre al arrancar Kokoro (código 15).
compuerta_cuda() {
  local version="" gpu="" minimo="${CUDA_MINIMA_MAYOR}.${CUDA_MINIMA_MENOR}"
  paso compuerta_cuda
  version="$(leer_cuda_version || true)"
  gpu="$(leer_gpu || true)"
  if [[ -z "$version" ]] || ! cuda_suficiente "$version"; then
    fallar 14 compuerta_cuda "se exige CUDA ${minimo} o más y nvidia-smi informa: ${version:-no se pudo leer CUDA Version}. Termina el pod y crea otro en otro host"
  fi
  ok compuerta_cuda "cuda=${version} gpu=${gpu:-desconocida}"
}

elegir_python() {
  local candidato
  if [[ -n "${PYTHON_BIN:-}" ]]; then
    command -v "$PYTHON_BIN" >/dev/null 2>&1 || fallar 2 "$PASO_ACTUAL" "PYTHON_BIN=${PYTHON_BIN} no existe"
    return 0
  fi
  # Los pines de requirements se verificaron contra wheels cp312. Con otra versión pip puede fallar.
  for candidato in python3.12 python3.11 python3; do
    if command -v "$candidato" >/dev/null 2>&1; then
      PYTHON_BIN="$candidato"
      return 0
    fi
  done
  fallar 1 "$PASO_ACTUAL" "no hay python3 en el pod"
}

validar_entradas() {
  [[ "$OLLAMA_NUM_PARALLEL" =~ ^[0-9]{1,2}$ ]] || fallar 2 validar_entradas "OLLAMA_NUM_PARALLEL debe ser un entero de 1 a 16"
  (( 10#$OLLAMA_NUM_PARALLEL >= 1 && 10#$OLLAMA_NUM_PARALLEL <= 16 )) \
    || fallar 2 validar_entradas "OLLAMA_NUM_PARALLEL debe ser un entero de 1 a 16"
  [[ "$OLLAMA_KEEP_ALIVE" =~ ^(-1|[0-9]+(s|m|h)?)$ ]] \
    || fallar 2 validar_entradas "OLLAMA_KEEP_ALIVE no es una duración válida (por ejemplo 24h)"
  [[ "$WHISPER_GPU" =~ ^(si|no|auto)$ ]] || fallar 2 validar_entradas "WHISPER_GPU debe ser si, no o auto"
}

# Lee las dos claves permitidas sin ejecutar el archivo. Nunca imprime los valores.
cargar_tokens() {
  local linea clave valor modo dueno
  paso tokens
  [[ -f "$TOKENS_ARCHIVO" ]] || fallar 2 tokens "falta ${TOKENS_ARCHIVO}"
  modo="$(stat -c %a "$TOKENS_ARCHIVO")"
  dueno="$(stat -c %u "$TOKENS_ARCHIVO")"
  [[ "$modo" == "600" ]] || fallar 2 tokens "${TOKENS_ARCHIVO} debe tener modo 600 y tiene ${modo}"
  [[ "$dueno" == "$(id -u)" ]] || fallar 2 tokens "${TOKENS_ARCHIVO} debe pertenecer al usuario que ejecuta el script"
  while IFS= read -r linea || [[ -n "$linea" ]]; do
    [[ -z "$linea" || "$linea" == \#* ]] && continue
    clave="${linea%%=*}"
    valor="${linea#*=}"
    case "$clave" in
      WHISPER_TOKEN) WHISPER_TOKEN="$valor" ;;
      VERA_LAB_TOKEN) VERA_LAB_TOKEN="$valor" ;;
      *) fallar 2 tokens "clave no permitida en el archivo de tokens" ;;
    esac
  done < "$TOKENS_ARCHIVO"
  [[ "$WHISPER_TOKEN" =~ ^[A-Za-z0-9_-]{32,}$ ]] || fallar 2 tokens "WHISPER_TOKEN ausente, corto o con caracteres no permitidos"
  [[ "$VERA_LAB_TOKEN" =~ ^[A-Za-z0-9_-]{32,}$ ]] || fallar 2 tokens "VERA_LAB_TOKEN ausente, corto o con caracteres no permitidos"
  ok tokens
}

tamano_de() { stat -c %s "$1"; }

sha256_de() {
  local huella
  huella="$(sha256sum "$1")"
  printf '%s' "${huella%% *}"
}

# ---------- sondas de salud (las mismas de salud-modelos.sh, duplicadas a propósito para que este script
# funcione solo, por ejemplo si se envía con "ssh pod bash -s") ----------
sano_ollama() {
  curl -fsS -m 5 "http://${OLLAMA_HOST_PORT}/api/version" >/dev/null 2>&1
}

sano_whisper() {
  curl_con_token "$WHISPER_TOKEN" -fsS -m 5 "http://127.0.0.1:${PUERTO_WHISPER}/healthz" >/dev/null 2>&1
}

sano_kokoro() {
  curl_con_token "$VERA_LAB_TOKEN" -fsS -m 5 "http://127.0.0.1:${PUERTO_KOKORO}/healthz" >/dev/null 2>&1
}

modelo_registrado() {
  local lista
  lista="$(OLLAMA_HOST="$OLLAMA_HOST_PORT" ollama list 2>/dev/null || true)"
  [[ "$lista" == *"$MODELO_OLLAMA"* ]]
}

todo_sano() {
  sano_ollama && sano_whisper && sano_kokoro && modelo_registrado
}

# ---------- código de los servidores ----------
obtener_codigo() {
  local tar_ruta destino_parcial
  paso codigo
  if [[ -d "${DIR_SERV}/stt-local" && -d "${DIR_SERV}/voz-local" ]] \
      && { [[ -z "$CODIGO_SHA" ]] || [[ "$(cat "${DIR_SERV}/.codigo_sha" 2>/dev/null || true)" == "$CODIGO_SHA" ]]; }; then
    ok codigo "servidores ya presentes en ${DIR_SERV}"
  else
    [[ "$CODIGO_SHA" =~ ^[0-9a-f]{40}$ ]] \
      || fallar 2 codigo "faltan los servidores en ${DIR_SERV} y VERA_CODIGO_SHA no es un commit de 40 caracteres hexadecimales"
    exigir_comandos curl tar
    tar_ruta="${ESTADO_DIR}/codigo.tar.gz"
    destino_parcial="${DIR_SERV}.parcial"
    borrar_carpeta_de_servidores "$destino_parcial"
    mkdir -p "$destino_parcial"
    curl -fsSL --retry 3 -o "$tar_ruta" "https://codeload.github.com/${REPO}/tar.gz/${CODIGO_SHA}"
    tar -xzf "$tar_ruta" -C "$destino_parcial" --strip-components=2 --wildcards '*/infra/stt-local' '*/infra/voz-local'
    printf '%s\n' "$CODIGO_SHA" > "${destino_parcial}/.codigo_sha"
    borrar_carpeta_de_servidores "$DIR_SERV"
    mv "$destino_parcial" "$DIR_SERV"
    ok codigo "servidores de ${CODIGO_SHA}"
  fi
  verificar_codigo
}

# Borra solo la carpeta de servidores o su .parcial. El pod es desechable y no tiene papelera.
borrar_carpeta_de_servidores() {
  local ruta="$1"
  [[ -n "$DIR_SERV" && "$DIR_SERV" != "/" ]] || return 0
  if [[ "$ruta" == "$DIR_SERV" || "$ruta" == "${DIR_SERV}.parcial" ]]; then
    rm -rf -- "$ruta"
  fi
}

verificar_codigo() {
  local f
  for f in stt-local/requirements.txt stt-local/servidor.py voz-local/requirements-cuda.txt voz-local/servidor.py voz-local/descargar.py; do
    [[ -f "${DIR_SERV}/${f}" ]] \
      || fallar 2 codigo "falta ${DIR_SERV}/${f}. La variante GPU de Kokoro debe estar en el commit pedido (rama feat/kokoro-gpu fusionada)"
  done
  [[ "$(< "${DIR_SERV}/voz-local/servidor.py")" == *VERA_VOZ_DISPOSITIVO* ]] \
    || fallar 2 codigo "voz-local/servidor.py no es la variante con GPU (no menciona VERA_VOZ_DISPOSITIVO)"
}

# ---------- instalación ----------
instalar_basicos() {
  local faltan=()
  paso apt
  [[ "$(id -u)" == "0" ]] || fallar 2 apt "hace falta root (apt, /usr/local y /opt/voz)"
  command -v zstd >/dev/null 2>&1 || faltan+=(zstd)
  [[ -e /usr/lib/x86_64-linux-gnu/libgomp.so.1 ]] || faltan+=(libgomp1)
  if (( ${#faltan[@]} > 0 )); then
    exigir_comandos apt-get
    apt-get update -qq
    apt-get install -y -qq "${faltan[@]}"
  fi
  ok apt "${faltan[*]:-nada que instalar}"
}

version_ollama() {
  local linea salida=""
  command -v ollama >/dev/null 2>&1 || return 1
  salida="$(ollama --version 2>&1 || true)"
  while IFS= read -r linea; do
    if [[ "$linea" =~ version\ is\ ([0-9]+\.[0-9]+\.[0-9]+) ]]; then
      printf '%s' "${BASH_REMATCH[1]}"
      return 0
    fi
  done <<< "$salida"
  return 1
}

# Mismo orden que la tesis: install.sh con la versión fijada y, si no queda esa versión, la release de GitHub.
instalar_ollama() {
  local actual="" release="${ESTADO_DIR}/ollama-linux-amd64.tar.zst"
  paso ollama_instalar
  actual="$(version_ollama || true)"
  if [[ "$actual" != "$OLLAMA_VERSION" ]]; then
    exigir_comandos curl sh tar
    curl -fsSL -o "${ESTADO_DIR}/ollama_install.sh" https://ollama.com/install.sh
    OLLAMA_VERSION="$OLLAMA_VERSION" sh "${ESTADO_DIR}/ollama_install.sh" || printf '    install.sh terminó con aviso (sin systemd es esperable)\n'
    actual="$(version_ollama || true)"
  fi
  if [[ "$actual" != "$OLLAMA_VERSION" ]]; then
    curl -fL --retry 3 -o "$release" "https://github.com/ollama/ollama/releases/download/v${OLLAMA_VERSION}/ollama-linux-amd64.tar.zst"
    tar -I zstd -xf "$release" -C /usr/local
    rm -f "$release"
    actual="$(version_ollama || true)"
  fi
  [[ "$actual" == "$OLLAMA_VERSION" ]] || fallar 1 ollama_instalar "no se pudo fijar Ollama ${OLLAMA_VERSION} (quedó '${actual:-nada}')"
  ok ollama_instalar "version ${actual}"
}

gguf_correcto() {
  [[ -f "$1" ]] && [[ "$(tamano_de "$1")" == "$GGUF_BYTES" ]] && [[ "$(sha256_de "$1")" == "$GGUF_SHA256" ]]
}

descargar_gguf() {
  local parcial="${GGUF_RUTA}.parcial"
  paso gguf
  mkdir -p "$GGUF_DIR"
  if gguf_correcto "$GGUF_RUTA"; then
    ok gguf "ya presente y verificado"
    return 0
  fi
  exigir_comandos curl sha256sum stat
  curl -fL --retry 5 --retry-delay 5 -C - -o "$parcial" "$GGUF_URL"
  [[ "$(tamano_de "$parcial")" == "$GGUF_BYTES" ]] \
    || fallar 3 gguf "el GGUF bajó $(tamano_de "$parcial") bytes y se esperaban ${GGUF_BYTES}"
  [[ "$(sha256_de "$parcial")" == "$GGUF_SHA256" ]] \
    || fallar 3 gguf "sha256 del GGUF distinto del lockfile. No se registra el modelo"
  mv "$parcial" "$GGUF_RUTA"
  ok gguf "verificado"
}

# crear_venv NOMBRE REQUISITOS [ARGS_PIP...]: se omite si el venv ya se instaló con ese mismo archivo.
# --only-binary=:all: porque un sdist ejecutaría un build como root en el pod.
crear_venv() {
  local nombre="$1" requisitos="$2" marca huella
  shift 2
  huella="$(sha256_de "$requisitos")"
  marca="${VENV_DIR}/${nombre}/.requisitos.sha256"
  if [[ -x "${VENV_DIR}/${nombre}/bin/python" && "$(cat "$marca" 2>/dev/null || true)" == "$huella" ]]; then
    return 0
  fi
  "$PYTHON_BIN" -m venv "${VENV_DIR}/${nombre}"
  "${VENV_DIR}/${nombre}/bin/python" -m pip install --no-cache-dir --only-binary=:all: -q "$@" -r "$requisitos"
  printf '%s\n' "$huella" > "$marca"
}

# Corre dentro del venv voz-cuda. kokoro-onnx declara onnxruntime (CPU) como dependencia dura y ese paquete
# comparte módulo con onnxruntime-gpu: si quedara instalado taparía al de GPU y ONNX Runtime caería a CPU sin avisar.
COMPROBACION_VENV_CUDA_PY='import importlib.metadata as m, sys
try:
    m.version("onnxruntime")
except m.PackageNotFoundError:
    pass
else:
    sys.exit("onnxruntime (CPU) esta instalado y taparia a onnxruntime-gpu")
import onnxruntime as ort
if "CUDAExecutionProvider" not in ort.get_available_providers():
    sys.exit("onnxruntime no ofrece CUDAExecutionProvider")
'

instalar_venvs() {
  paso venvs
  mkdir -p "$VENV_DIR"
  # shellcheck disable=SC2086
  crear_venv stt "${DIR_SERV}/stt-local/requirements.txt" ${PIP_EXTRA_STT:-}
  # requirements-cuda.txt es un conjunto cerrado: --no-deps es obligatorio.
  crear_venv voz-cuda "${DIR_SERV}/voz-local/requirements-cuda.txt" --no-deps
  "${VENV_DIR}/voz-cuda/bin/python" -c "$COMPROBACION_VENV_CUDA_PY" \
    || fallar 15 venvs "el venv voz-cuda no sirve: onnxruntime (CPU) instalado o sin CUDAExecutionProvider"
  ok venvs
}

descargar_pesos() {
  paso pesos
  "${VENV_DIR}/stt/bin/python" -c 'from huggingface_hub import snapshot_download as d; print(d("Systran/faster-whisper-small"))' >/dev/null
  # descargar.py verifica sha256 y tamaño y usa .parcial con os.replace. Con GPU=1 baja además el fp32.
  # El fp16 nunca: en CUDA dio audio vacío.
  VERA_VOZ_DESCARGAR_GPU=1 VERA_VOZ_DESCARGAR_FP16=0 VERA_VOZ_MODELOS_DESTINO="$MODELOS_VOZ" \
    "${VENV_DIR}/voz-cuda/bin/python" "${DIR_SERV}/voz-local/descargar.py" >/dev/null
  ok pesos
}

# ---------- servicios ----------
# lanzar NOMBRE COMANDO...: desacoplado de la sesión ssh (setsid, stdin nulo, salida a un log). Si algún hijo
# heredara los descriptores de la sesión, ssh no volvería nunca.
lanzar() {
  local nombre="$1" log="${LOGS}/$1.log"
  shift
  : > "$log"
  setsid nohup "$@" > "$log" 2>&1 < /dev/null &
  printf '%s\n' "$!" > "${PIDS}/${nombre}.pid"
}

detener_proceso() {
  local archivo="$1" pid
  [[ -f "$archivo" ]] || return 0
  pid="$(< "$archivo")"
  if [[ "$pid" =~ ^[0-9]+$ ]] && kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
    sleep 2
    kill -9 "$pid" 2>/dev/null || true
  fi
  rm -f "$archivo"
}

# Un Ollama viejo con el puerto ocupado haría que el nuevo muera con "address in use" y que el healthcheck
# pase contra el viejo. Se espera a que no quede nada.
esperar_ollama_apagado() {
  local inicio
  inicio="$(date +%s)"
  while curl -fsS -m 2 "http://${OLLAMA_HOST_PORT}/api/version" >/dev/null 2>&1 || pgrep -f "ollama (serve|runner)" >/dev/null 2>&1; do
    pkill -9 -f "ollama (serve|runner)" 2>/dev/null || true
    (( $(date +%s) - inicio < 60 )) || fallar 1 detener "ollama sigue vivo 60 s despues de detenerlo"
    sleep 2
  done
}

detener_todo() {
  local pid
  paso detener
  for pid in "${PIDS}"/*.pid; do
    [[ -e "$pid" ]] && detener_proceso "$pid"
  done
  pkill -f "ollama serve" 2>/dev/null || true
  esperar_ollama_apagado
  ok detener
}

# esperar_salud NOMBRE ARCHIVO_PID LIMITE_S SONDA: falla con la cola del log si el proceso muere o no sube.
esperar_salud() {
  local nombre="$1" archivo_pid="$2" limite="$3" sonda="$4" inicio pid
  inicio="$(date +%s)"
  while :; do
    pid="$(< "$archivo_pid")"
    kill -0 "$pid" 2>/dev/null || return 21
    if "$sonda" && kill -0 "$pid" 2>/dev/null; then
      return 0
    fi
    (( $(date +%s) - inicio < limite )) || return 22
    sleep 3
  done
}

cola_log() {
  tail -n 20 "${LOGS}/$1.log" 2>/dev/null | tr '\n' ' ' || true
}

arrancar_whisper() {
  local rc=0
  paso whisper
  (
    cd "${DIR_SERV}/stt-local"
    export WHISPER_TOKEN WHISPER_DIRECCION="127.0.0.1:${PUERTO_WHISPER}" WHISPER_MODELO=small WHISPER_GPU
    [[ -z "${WHISPER_CONCURRENCIA:-}" ]] || export WHISPER_CONCURRENCIA
    [[ -z "${WHISPER_ESPERA_MS:-}" ]] || export WHISPER_ESPERA_MS
    lanzar whisper "${VENV_DIR}/stt/bin/python" servidor.py
  )
  esperar_salud whisper "${PIDS}/whisper.pid" 600 sano_whisper || rc=$?
  (( rc == 0 )) || fallar 4 whisper "Whisper no quedó sano (rc=${rc}). Log: $(cola_log whisper)"
  ok whisper
}

# Kokoro en GPU. Si el servidor aborta porque CUDAExecutionProvider no cargó, sale con 15.
arrancar_kokoro() {
  local rc=0 log
  paso kokoro
  (
    cd "${DIR_SERV}/voz-local"
    export VERA_LAB_TOKEN VERA_VOZ_PUERTO="$PUERTO_KOKORO" VERA_VOZ_DISPOSITIVO=cuda VERA_VOZ_PRECISION=fp32
    [[ -z "${VERA_VOZ_ESPERA_MS:-}" ]] || export VERA_VOZ_ESPERA_MS
    lanzar kokoro "${VENV_DIR}/voz-cuda/bin/python" servidor.py
  )
  esperar_salud kokoro "${PIDS}/kokoro.pid" 300 sano_kokoro || rc=$?
  if (( rc != 0 )); then
    log="$(cola_log kokoro)"
    if [[ "$(< "${LOGS}/kokoro.log")" == *"CUDAExecutionProvider no carg"* ]]; then
      fallar 15 kokoro "CUDAExecutionProvider no cargó y onnxruntime habría caído a CPU. Termina el pod y crea otro"
    fi
    fallar 4 kokoro "Kokoro no quedó sano (rc=${rc}). Log: ${log}"
  fi
  ok kokoro "cuda fp32"
}

registrar_modelo() {
  if modelo_registrado; then
    return 0
  fi
  gguf_correcto "$GGUF_RUTA" || fallar 3 ollama "el GGUF no está verificado: no se registra el modelo"
  # Se ejecuta desde la carpeta del GGUF con FROM relativo, igual que la tesis (la ruta absoluta no se probó).
  printf 'FROM ./%s\n' "$GGUF_NOMBRE" > "${GGUF_DIR}/Modelfile"
  ( cd "$GGUF_DIR" && OLLAMA_HOST="$OLLAMA_HOST_PORT" ollama create "$MODELO_OLLAMA" -f Modelfile ) >/dev/null
}

# Calienta con el mismo num_ctx que usa el backend (internal/copilot/ollama/proveedor.go): con otro valor
# la primera petición real recargaría el modelo.
calentar_modelo() {
  curl -fsS -m 600 "http://${OLLAMA_HOST_PORT}/api/generate" \
    -d "{\"model\":\"${MODELO_OLLAMA}\",\"prompt\":\"hola\",\"stream\":false,\"keep_alive\":\"${OLLAMA_KEEP_ALIVE}\",\"options\":{\"num_ctx\":${NUM_CTX},\"num_predict\":1}}" \
    >/dev/null
}

comprobar_vram() {
  local mensaje rc=0
  mensaje="$(curl -fsS -m 10 "http://${OLLAMA_HOST_PORT}/api/ps" | "$PYTHON_BIN" -c '
import json, sys
modelos = json.load(sys.stdin).get("models", [])
if not modelos:
    sys.exit("ningun modelo cargado en /api/ps")
malos = [m.get("name") for m in modelos if m.get("size_vram", 0) < m.get("size", 0)]
if malos:
    sys.exit("size_vram menor que size en: " + ", ".join(malos))
print("vram ok:", ", ".join("%s %d/%d" % (m.get("name"), m.get("size_vram", 0), m.get("size", 0)) for m in modelos))
' 2>&1)" || rc=$?
  (( rc == 0 )) || fallar 13 ollama "Ollama no cabe entero en VRAM: ${mensaje}"
  ok ollama "${mensaje}"
}

arrancar_ollama() {
  local rc=0
  paso ollama
  mkdir -p "$OLLAMA_MODELS"
  (
    export OLLAMA_HOST="$OLLAMA_HOST_PORT" OLLAMA_NUM_PARALLEL OLLAMA_KEEP_ALIVE OLLAMA_MODELS
    lanzar ollama ollama serve
  )
  esperar_salud ollama "${PIDS}/ollama.pid" 180 sano_ollama || rc=$?
  (( rc == 0 )) || fallar 4 ollama "Ollama no respondió (rc=${rc}). Log: $(cola_log ollama)"
  registrar_modelo
  calentar_modelo || fallar 4 ollama "el calentamiento del modelo falló. Log: $(cola_log ollama)"
  comprobar_vram
}

plan_seco() {
  cat <<PLAN
arrancar-modelos.sh en modo seco: no se ejecuta nada.
Estado y progreso: ${PROGRESO}
Tokens (modo 600, solo se leen WHISPER_TOKEN y VERA_LAB_TOKEN): ${TOKENS_ARCHIVO}
Pasos, en este orden:
  1. compuerta de CUDA: nvidia-smi (${NVIDIA_SMI}) debe ofrecer ${CUDA_MINIMA_MAYOR}.${CUDA_MINIMA_MENOR} o más, si no sale con 14
  2. tokens: se leen sin imprimirse
  3. salud: si Ollama, Whisper y Kokoro ya responden y el modelo existe, sale con 0 (VERA_FORZAR=${FORZAR})
  4. código de los servidores: ${DIR_SERV} (VERA_CODIGO_SHA=${CODIGO_SHA:-sin definir}, repo ${REPO})
  5. apt: zstd y libgomp1 si faltan
  6. Ollama ${OLLAMA_VERSION} exacto
  7. GGUF ${GGUF_BYTES} bytes, sha256 ${GGUF_SHA256}, a ${GGUF_RUTA} (.parcial y mv)
  8. venv stt y voz-cuda en ${VENV_DIR} (pip --only-binary=:all:, voz-cuda con --no-deps)
  9. pesos: Whisper small en ${HF_HOME}, Kokoro fp32 y voces en ${MODELOS_VOZ} (descargar.py, sha256 propio)
 10. servicios, en este orden para que Whisper reserve su VRAM antes que Ollama:
     Whisper 127.0.0.1:${PUERTO_WHISPER} (WHISPER_GPU=${WHISPER_GPU}), Kokoro 127.0.0.1:${PUERTO_KOKORO} (cuda, fp32),
     Ollama ${OLLAMA_HOST_PORT} (OLLAMA_NUM_PARALLEL=${OLLAMA_NUM_PARALLEL}, OLLAMA_KEEP_ALIVE=${OLLAMA_KEEP_ALIVE})
 11. modelo ${MODELO_OLLAMA}, calentamiento con num_ctx ${NUM_CTX} y comprobación size_vram == size (13 si no cabe)
PLAN
}

preparar_estado() {
  mkdir -p "$ESTADO_DIR" "$LOGS" "$PIDS"
  chmod 700 "$ESTADO_DIR"
  : > "$PROGRESO"
}

solo_compuerta() {
  mkdir -p "$ESTADO_DIR" "$LOGS" "$PIDS"
  : > "$PROGRESO"
  paso entorno
  exigir_comandos curl tar sha256sum stat date
  ok entorno
  compuerta_cuda
  progreso compuerta listo 0 "compuerta de CUDA superada"
  LINEA_FINAL="si"
}

principal() {
  case "${1:-}" in
    --seco) plan_seco; return 0 ;;
    --compuerta) solo_compuerta; return 0 ;;
    "") ;;
    *) printf 'uso: arrancar-modelos.sh [--seco|--compuerta]\n' >&2; exit 2 ;;
  esac
  preparar_estado
  progreso inicio en_curso 0 "arranque de modelos, P=${OLLAMA_NUM_PARALLEL}"
  validar_entradas
  paso entorno
  exigir_comandos curl tar sha256sum stat date pgrep pkill "$NVIDIA_SMI"
  elegir_python
  ok entorno "python=${PYTHON_BIN}"
  compuerta_cuda
  cargar_tokens
  if [[ "$FORZAR" != "1" ]] && todo_sano; then
    progreso salud listo 0 "los tres servicios ya respondían: reutilizados sin reiniciar"
    LINEA_FINAL="si"
    return 0
  fi
  obtener_codigo
  instalar_basicos
  instalar_ollama
  descargar_gguf
  instalar_venvs
  descargar_pesos
  detener_todo
  arrancar_whisper
  arrancar_kokoro
  arrancar_ollama
  progreso listo listo 0 "ollama:${OLLAMA_HOST_PORT} whisper:${PUERTO_WHISPER} kokoro:${PUERTO_KOKORO}"
  LINEA_FINAL="si"
}

principal "$@"

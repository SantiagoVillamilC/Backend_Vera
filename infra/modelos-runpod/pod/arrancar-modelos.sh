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
#   bash arrancar-modelos.sh --preparar   instala y descarga todo (llena la caché) pero no arranca los servicios
#
# Caché en un volumen de red de RunPod (VERA_VOLUMEN_MONTADO=1):
#   modelos-control exporta VERA_VOLUMEN_MONTADO=1 cuando creó el pod con un volumen de red. Solo se usa si
#   ADEMÁS /workspace es un punto de montaje propio y escribible. Entonces todo lo pesado vive en
#   /workspace/vera-cache (GGUF, almacén de Ollama, pesos de Whisper y de Kokoro, caché de pip y los dos venv) y
#   el segundo arranque en el mismo centro de datos no descarga ni instala nada que siga vigente. Cada
#   componente se reutiliza solo si su marca .completo (en vera-cache/marcas) coincide con la firma esperada y
#   pasa su verificación. Lo que cambia por pod (tokens, pids, progreso, logs, código de los servidores) queda
#   FUERA del volumen, en /var/lib/vera-estado y /var/lib/vera. Sin volumen el comportamiento es el de siempre.
#   Ollama se reinstala en cada arranque (unos 18 s): copiar su binario y sus librerías al volumen no compensa.
#   Este script se sube suelto al pod: no puede depender de otros archivos.
#
# Entradas (todas opcionales salvo el archivo de tokens y el origen del código):
#   Tokens (32 caracteres o más), por UNO de estos dos caminos, nunca en argv:
#     a) entorno: WHISPER_TOKEN y VERA_LAB_TOKEN (o KOKORO_TOKEN), que modelos-control exporta por stdin
#        de "bash -s". Gana si están. Se guardan en VERA_TOKENS_ARCHIVO (0600) para salud-modelos.sh.
#     b) VERA_TOKENS_ARCHIVO: archivo 0600 con WHISPER_TOKEN y VERA_LAB_TOKEN, entregado con "umask 077".
#   VERA_SERVIDORES_DIR    carpeta con stt-local y voz-local (variante GPU de Kokoro). Si falta, se baja de
#                          GitHub el commit VERA_CODIGO_SHA o REPO_SHA (40 hex) del repositorio VERA_REPO.
#   OLLAMA_NUM_PARALLEL    por defecto 2 (demo). Medido en una 4090: P=8 aguanta 8 usuarios, P=2 usa 5 GB de VRAM.
#   OLLAMA_KEEP_ALIVE      por defecto 24h
#   WHISPER_GPU            por defecto si (con "auto" un fallo de CUDA bajaría a CPU en silencio)
#   PIP_EXTRA_STT          requisitos extra de Whisper con versión exacta (por ejemplo bibliotecas cuDNN)
#   VERA_ESTADO_DIR, VERA_VENV_DIR, VERA_GGUF_DIR, HF_HOME, OLLAMA_MODELS, PYTHON_BIN, NVIDIA_SMI
#   VERA_FORZAR=1          reinicia los servicios aunque ya estén sanos
#   VERA_VOLUMEN_MONTADO   1 si el pod se creó con volumen de red (lo exporta modelos-control), 0 o vacío si no
#   VERA_CANDADO_ESPERA_S  segundos que se espera el candado de la caché si otra corrida lo tiene (120)
#   Solo para pruebas: VERA_VOLUMEN_RAIZ (por defecto /workspace), VERA_MODELOS_VOZ_RUTA (por defecto
#   /opt/voz/modelos) y VERA_CARGAR_SIN_EJECUTAR=1 (carga las funciones sin ejecutar nada).
#
# Progreso: una línea JSON por evento en $VERA_ESTADO_DIR/progreso.jsonl (ver docs/despliegue-modelos-runpod-vm.md).
#   {"epoch": N, "paso": "...", "estado": "en_curso|ok|fallo|listo", "codigo": N, "detalle": "..."}
# La última línea es siempre "listo" o "fallo". Cada línea se escribe con un solo printf en modo append.
#
# Códigos de salida (contrato con modelos-control):
#   0 listo   1 fallo genérico   2 entrada inválida (tokens, variables, código)   3 sha256 distinto
#   4 un servicio no quedó sano a tiempo   5 ya hay otra corrida en curso (no toca el progreso)
#   13 Ollama no cabe entero en VRAM
#   14 el driver no ofrece CUDA 13.0 (terminar el pod y crear otro en otro host)
#   15 Kokoro abortó porque CUDAExecutionProvider no cargó (mismo remedio que 14)
#   16 el volumen de la caché no tiene espacio para lo que falta (agrandar el volumen o vaciar la caché)
set -euo pipefail

ESTADO_DIR="${VERA_ESTADO_DIR:-/workspace/vera-estado}"
PROGRESO="${ESTADO_DIR}/progreso.jsonl"
LOGS="${ESTADO_DIR}/logs"
PIDS="${ESTADO_DIR}/pids"
VENV_DIR="${VERA_VENV_DIR:-/workspace/venv}"
DIR_SERV="${VERA_SERVIDORES_DIR:-/workspace/vera/servidores}"
TOKENS_ARCHIVO="${VERA_TOKENS_ARCHIVO:-${ESTADO_DIR}/tokens.env}"
CODIGO_SHA="${VERA_CODIGO_SHA:-${REPO_SHA:-}}"
# Tokens que pudo entregar modelos-control por entorno (export en el stdin de "bash -s"). Se capturan y se
# quitan del entorno de este proceso para que no los hereden pip, curl ni ningún hijo.
TOKEN_STT_ENTORNO="${WHISPER_TOKEN:-}"
TOKEN_VOZ_ENTORNO="${VERA_LAB_TOKEN:-${KOKORO_TOKEN:-}}"
unset WHISPER_TOKEN VERA_LAB_TOKEN KOKORO_TOKEN
REPO="${VERA_REPO:-SantiagoVillamilC/Backend_Vera}"
GGUF_DIR="${VERA_GGUF_DIR:-/workspace/modelos/gemma4}"
HF_HOME_ENTRADA="${HF_HOME:-}"
OLLAMA_MODELS_ENTRADA="${OLLAMA_MODELS:-}"
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
MODELOS_VOZ="${VERA_MODELOS_VOZ_RUTA:-/opt/voz/modelos}"   # servidor.py de Kokoro tiene esta ruta fija: solo las pruebas la cambian
KOKORO_BYTES=467839094   # int8 114119327 + voces 28214398 + fp32 325505369, los tres que baja descargar.py con GPU=1

# ---------- caché en volumen de red ----------
VOLUMEN_RAIZ="${VERA_VOLUMEN_RAIZ:-/workspace}"
VOLUMEN_PEDIDO="${VERA_VOLUMEN_MONTADO:-0}"
CANDADO_ESPERA_S="${VERA_CANDADO_ESPERA_S:-120}"
CACHE_ACTIVA="no"
CACHE_RAIZ=""
CACHE_AVISO=""
ESTADO_LOCAL="/var/lib/vera-estado"        # con volumen, el estado del pod NO va al volumen (tokens, pids)
SERVIDORES_LOCAL="/var/lib/vera/servidores"
PIP_CACHE_OPCION=(--no-cache-dir)          # con volumen y espacio se vacía y pip usa PIP_CACHE_DIR
FIRMA_PYTHON=""
FIRMA_SISTEMA=""
# Tamaños para decidir si cabe lo que falta. KOKORO_BYTES y GGUF_BYTES son exactos. El resto son ESTIMACIONES
# (sin medir en un pod real): la primera corrida con volumen anota los bytes reales de cada venv en su marca.
EST_OLLAMA_BYTES=$((GGUF_BYTES + 134217728))   # el almacén de Ollama copia el GGUF (unos 5 GB más)
EST_WHISPER_BYTES=629145600                    # faster-whisper-small, unos 0,6 GB
EST_VENV_STT_BYTES=3221225472                  # 3 GiB con las librerías cuDNN y cuBLAS de PIP_EXTRA_STT
EST_VENV_VOZ_BYTES=3758096384                  # 3,5 GiB: onnxruntime-gpu y las librerías NVIDIA de CUDA 13
EST_PIP_BYTES=3758096384                       # ruedas descargadas, 3,5 GiB
MARGEN_BYTES=1073741824                        # 1 GiB libre de margen
PEND_CORE=0
PEND_PIP=0

PASO_ACTUAL="inicio"
LINEA_FINAL="no"
INICIO_TOTAL=$SECONDS
FASE_T0=$SECONDS
ORIGEN_FASE=""
TABLA_FASES=()
COMPONENTES=()
G_INST=0 G_DESC=0 G_CARGA=0 G_SERV=0
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
  FASE_T0=$SECONDS
  progreso "$1" en_curso 0 "${2:-}"
  printf '==> %s\n' "$1"
}

# ok cierra la fase y anota su duración y el origen de lo que usó (caché o descarga) para el resumen final.
ok() {
  progreso "$1" ok 0 "${2:-}"
  TABLA_FASES+=("$1|$((SECONDS - FASE_T0))|${ORIGEN_FASE}")
  ORIGEN_FASE=""
}

# componente NOMBRE ORIGEN: anota de dónde salió un componente (cache, descargado, construido, reconstruido).
componente() {
  COMPONENTES+=("$1=$2")
  ORIGEN_FASE+="${ORIGEN_FASE:+ }$1=$2"
}

# subfase NOMBRE T0: mide un tramo dentro de una fase (la carga del modelo dentro del paso ollama).
subfase() { TABLA_FASES+=("$1|$((SECONDS - $2))|subfase"); }

calcular_grupos() {
  local fila fase seg
  G_INST=0 G_DESC=0 G_CARGA=0 G_SERV=0
  for fila in "${TABLA_FASES[@]}"; do
    fase="${fila%%|*}"
    seg="${fila#*|}"
    seg="${seg%%|*}"
    case "$fase" in
      apt|ollama_instalar|venvs) G_INST=$((G_INST + seg)) ;;
      codigo|gguf|pesos) G_DESC=$((G_DESC + seg)) ;;
      ollama_carga) G_CARGA=$((G_CARGA + seg)) ;;
      detener|whisper|kokoro|ollama) G_SERV=$((G_SERV + seg)) ;;
    esac
  done
  G_SERV=$((G_SERV - G_CARGA))   # la fase ollama ya incluye su carga
}

imprimir_resumen() {
  local fila fase seg origen
  calcular_grupos
  printf '==> resumen de tiempos (cache=%s)\n' "$CACHE_ACTIVA"
  printf '%-18s %6s  %s\n' fase seg origen
  for fila in "${TABLA_FASES[@]}"; do
    IFS='|' read -r fase seg origen <<< "$fila"
    printf '%-18s %6s  %s\n' "$fase" "$seg" "$origen"
  done
  printf 'por grupo: instalacion=%ss descargas=%ss carga=%ss servicios=%ss total=%ss\n' \
    "$G_INST" "$G_DESC" "$G_CARGA" "$G_SERV" "$((SECONDS - INICIO_TOTAL))"
  printf 'componentes: %s\n' "${COMPONENTES[*]:-ninguno}"
}

cerrar_resumen() {
  imprimir_resumen
  progreso resumen ok 0 "cache=${CACHE_ACTIVA} total=$((SECONDS - INICIO_TOTAL))s instalacion=${G_INST}s descargas=${G_DESC}s carga=${G_CARGA}s servicios=${G_SERV}s componentes: ${COMPONENTES[*]:-ninguno}"
}

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
    progreso "$PASO_ACTUAL" fallo "$codigo" "salida inesperada con código ${codigo}$(pista_espacio)"
  fi
  # Un fallo a mitad de camino también deja a la vista qué tardó cuánto (solo al log, nunca al progreso).
  if [[ "$codigo" -ne 0 && ${#TABLA_FASES[@]} -gt 0 ]]; then
    imprimir_resumen || true
  fi
}

# Con la caché activa, una salida inesperada suele ser un disco lleno: se deja el espacio libre en el detalle.
pista_espacio() {
  local libre
  [[ "$CACHE_ACTIVA" == "si" && -d "$CACHE_RAIZ" ]] || return 0
  libre="$(stat -f -c '%a %S' -- "$CACHE_RAIZ" 2>/dev/null || true)"
  [[ -n "$libre" ]] || return 0
  printf ' (libre en la caché: %s bytes)' "$(( ${libre%% *} * ${libre##* } ))"
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
  [[ "$VOLUMEN_PEDIDO" =~ ^[01]$ ]] || fallar 2 validar_entradas "VERA_VOLUMEN_MONTADO debe ser 0 o 1"
  [[ "$CANDADO_ESPERA_S" =~ ^[0-9]{1,4}$ ]] || fallar 2 validar_entradas "VERA_CANDADO_ESPERA_S debe ser un entero de segundos"
}

# Lee las dos claves permitidas del archivo sin ejecutarlo. Nunca imprime los valores.
leer_tokens_archivo() {
  local linea clave valor modo dueno
  [[ -f "$TOKENS_ARCHIVO" ]] || fallar 2 tokens "faltan los tokens: ni en el entorno ni en ${TOKENS_ARCHIVO}"
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
}

# Guarda los tokens recibidos por entorno en un archivo 0600 para que salud-modelos.sh pueda leerlos después.
persistir_tokens() {
  ( umask 077 && printf 'WHISPER_TOKEN=%s\nVERA_LAB_TOKEN=%s\n' "$WHISPER_TOKEN" "$VERA_LAB_TOKEN" > "${TOKENS_ARCHIVO}.tmp" )
  chmod 600 "${TOKENS_ARCHIVO}.tmp"
  mv "${TOKENS_ARCHIVO}.tmp" "$TOKENS_ARCHIVO"
}

# Dos orígenes, el entorno gana porque es el más reciente (modelos-control lo exporta en cada arranque):
#   1. WHISPER_TOKEN y VERA_LAB_TOKEN (o KOKORO_TOKEN) en el entorno, enviados por stdin y nunca en argv
#   2. el archivo 0600 de VERA_TOKENS_ARCHIVO
cargar_tokens() {
  local desde="archivo"
  paso tokens
  if [[ -n "$TOKEN_STT_ENTORNO" && -n "$TOKEN_VOZ_ENTORNO" ]]; then
    WHISPER_TOKEN="$TOKEN_STT_ENTORNO"
    VERA_LAB_TOKEN="$TOKEN_VOZ_ENTORNO"
    desde="entorno"
  else
    leer_tokens_archivo
  fi
  [[ "$WHISPER_TOKEN" =~ ^[A-Za-z0-9_-]{32,}$ ]] || fallar 2 tokens "WHISPER_TOKEN ausente, corto o con caracteres no permitidos"
  [[ "$VERA_LAB_TOKEN" =~ ^[A-Za-z0-9_-]{32,}$ ]] || fallar 2 tokens "VERA_LAB_TOKEN ausente, corto o con caracteres no permitidos"
  if [[ "$desde" == "entorno" ]]; then
    persistir_tokens
  fi
  ok tokens "origen=${desde}"
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

# ---------- caché en el volumen de red ----------
# mountpoint (util-linux) dice si la ruta es un punto de montaje propio. Sin él se lee /proc/mounts.
volumen_montado() {
  local punto
  if command -v mountpoint >/dev/null 2>&1; then
    mountpoint -q "$VOLUMEN_RAIZ"
    return
  fi
  while read -r _ punto _; do
    [[ "$punto" == "$VOLUMEN_RAIZ" ]] && return 0
  done < /proc/mounts
  return 1
}

# Tipo y origen del montaje, para el progreso. En un pod real dice si /workspace es de verdad un volumen de red.
describir_montaje() {
  local d=""
  if command -v findmnt >/dev/null 2>&1; then
    d="$(findmnt -n -o FSTYPE,SOURCE -T "$VOLUMEN_RAIZ" 2>/dev/null || true)"
  fi
  printf '%s' "${d//$'\n'/ }"
}

# La variable sola no basta: un pod sin volumen de red también monta su disco en /workspace, y un
# modelos-control equivocado podría exportarla de más. Solo se activa la caché si además hay montaje propio.
decidir_volumen() {
  [[ "$VOLUMEN_PEDIDO" == "1" ]] || return 0
  if volumen_montado && [[ -d "$VOLUMEN_RAIZ" && -w "$VOLUMEN_RAIZ" ]]; then
    CACHE_ACTIVA="si"
    CACHE_RAIZ="${VOLUMEN_RAIZ}/vera-cache"
  else
    CACHE_AVISO="VERA_VOLUMEN_MONTADO=1 pero ${VOLUMEN_RAIZ} no es un punto de montaje escribible: se ignora el volumen y se instala todo en el pod"
  fi
}

# Con caché: lo pesado va a vera-cache y lo que cambia por pod (tokens, pids, logs, código) se queda fuera del
# volumen. Una variable VERA_* o HF_HOME/OLLAMA_MODELS dada por quien lanza el script gana sobre estos valores.
aplicar_rutas_cache() {
  [[ "$CACHE_ACTIVA" == "si" ]] || return 0
  if [[ -z "${VERA_ESTADO_DIR:-}" ]]; then
    ESTADO_DIR="$ESTADO_LOCAL"
    PROGRESO="${ESTADO_DIR}/progreso.jsonl"
    LOGS="${ESTADO_DIR}/logs"
    PIDS="${ESTADO_DIR}/pids"
    [[ -n "${VERA_TOKENS_ARCHIVO:-}" ]] || TOKENS_ARCHIVO="${ESTADO_DIR}/tokens.env"
  fi
  [[ -n "${VERA_SERVIDORES_DIR:-}" ]] || DIR_SERV="$SERVIDORES_LOCAL"
  [[ -n "${VERA_VENV_DIR:-}" ]] || VENV_DIR="${CACHE_RAIZ}/venv"
  if [[ -z "${VERA_GGUF_DIR:-}" ]]; then
    GGUF_DIR="${CACHE_RAIZ}/gguf"
    GGUF_RUTA="${GGUF_DIR}/${GGUF_NOMBRE}"
  fi
  [[ -n "$HF_HOME_ENTRADA" ]] || export HF_HOME="${CACHE_RAIZ}/hf"
  export HF_HUB_CACHE="${HF_HOME}/hub"
  [[ -n "$OLLAMA_MODELS_ENTRADA" ]] || export OLLAMA_MODELS="${CACHE_RAIZ}/ollama-modelos"
  export PIP_CACHE_DIR="${CACHE_RAIZ}/pip"
  PIP_CACHE_OPCION=()
}

huella_texto() {
  local huella
  huella="$(printf '%s' "$1" | sha256sum)"
  printf '%s' "${huella%% *}"
}

# Las marcas viven aparte, en vera-cache/marcas, para no ensuciar las carpetas que administran pip, Hugging Face
# y Ollama. La primera línea es firma=<sha256>: si falta, está truncada o no coincide, el componente no vale.
ruta_marca() { printf '%s/marcas/%s.completo' "$CACHE_RAIZ" "$1"; }

marca_valida() { # NOMBRE FIRMA
  local primera="" marca
  marca="$(ruta_marca "$1")"
  [[ -f "$marca" ]] || return 1
  IFS= read -r primera < "$marca" || true
  [[ "$primera" == "firma=$2" ]]
}

# Se escribe SOLO cuando el componente terminó y pasó su verificación. Antes se vuelca el disco (sync -f) para
# que un pod que muere justo después no deje la marca en pie con datos a medias. La marca llega por mv atómico.
escribir_marca() { # NOMBRE FIRMA [DETALLE]
  local marca
  marca="$(ruta_marca "$1")"
  mkdir -p "${marca%/*}"
  sync -f "$CACHE_RAIZ" 2>/dev/null || sync || true
  printf 'firma=%s\nterminado=%s\ncomponente=%s\ndetalle=%s\n' "$2" "$(date +%s)" "$1" "${3:-}" > "${marca}.tmp"
  mv -f -- "${marca}.tmp" "$marca"
}

# Única vía para borrar dentro del volumen: se niega si la ruta real no cuelga de vera-cache.
borrar_en_cache() {
  local ruta="$1" real raiz
  [[ "$CACHE_ACTIVA" == "si" && -n "$CACHE_RAIZ" ]] || fallar 1 "$PASO_ACTUAL" "borrado de ${ruta} pedido sin caché activa"
  [[ -e "$ruta" || -L "$ruta" ]] || return 0
  real="$(realpath -m -- "$ruta")"
  raiz="$(realpath -m -- "$CACHE_RAIZ")"
  [[ "$real" == "$raiz"/?* ]] || fallar 1 "$PASO_ACTUAL" "se rechaza borrar ${ruta}: no está dentro de ${raiz}"
  rm -rf -- "$ruta"
}

quitar_marca() { borrar_en_cache "$(ruta_marca "$1")"; }

# Con caché se borra solo dentro de vera-cache. Sin caché es el pod, que es desechable, y rige lo de siempre.
descartar() {
  if [[ "$CACHE_ACTIVA" == "si" ]]; then borrar_en_cache "$1"; else rm -rf -- "$1"; fi
}

# Identidad del entorno que fabrica los venv: un venv guarda rutas absolutas al Python del sistema, así que solo
# sirve en el mismo Python (versión completa y ruta real) y el mismo sistema. La imagen de RunPod no se ve desde
# dentro del pod: estos datos son el sustituto.
calcular_firmas() {
  [[ "$CACHE_ACTIVA" == "si" ]] || return 0
  FIRMA_PYTHON="$("$PYTHON_BIN" -c 'import os, sys; print(sys.version.replace("\n", " "), os.path.realpath(sys.executable))')" \
    || fallar 1 entorno "no se pudo leer la versión de ${PYTHON_BIN}"
  # shellcheck source=/dev/null
  FIRMA_SISTEMA="$( . /etc/os-release 2>/dev/null; printf '%s-%s-%s' "${ID:-?}" "${VERSION_ID:-?}" "$(uname -m)" )"
}

firma_venv() { # NOMBRE REQUISITOS [ARGS_PIP...]
  local nombre="$1" requisitos="$2"
  shift 2
  huella_texto "venv|${nombre}|$(sha256_de "$requisitos")|$*|${FIRMA_PYTHON}|${FIRMA_SISTEMA}"
}
firma_whisper() { huella_texto "whisper|Systran/faster-whisper-small"; }
firma_ollama() { huella_texto "ollama|${OLLAMA_VERSION}|${MODELO_OLLAMA}|${GGUF_SHA256}"; }

espacio_libre() {
  local datos
  datos="$(stat -f -c '%a %S' -- "$1")" || fallar 1 "$PASO_ACTUAL" "no se pudo leer el espacio libre de $1"
  printf '%s' "$(( ${datos%% *} * ${datos##* } ))"
}

# Bytes que habría que escribir según las marcas y los tamaños de los archivos (comprobación barata, no la de verdad).
calcular_pendientes() {
  local voz="${CACHE_RAIZ}/voz-modelos"
  PEND_CORE=0 PEND_PIP=0
  [[ -f "$GGUF_RUTA" && "$(tamano_de "$GGUF_RUTA")" == "$GGUF_BYTES" ]] || PEND_CORE=$((PEND_CORE + GGUF_BYTES))
  marca_valida ollama-modelo "$(firma_ollama)" || PEND_CORE=$((PEND_CORE + EST_OLLAMA_BYTES))
  marca_valida whisper-small "$(firma_whisper)" || PEND_CORE=$((PEND_CORE + EST_WHISPER_BYTES))
  [[ -f "${voz}/kokoro-v1.0.onnx" && -f "${voz}/kokoro-v1.0.int8.onnx" && -f "${voz}/voices-v1.0.bin" ]] \
    || PEND_CORE=$((PEND_CORE + KOKORO_BYTES))
  # shellcheck disable=SC2086
  marca_valida venv-stt "$(firma_venv stt "${DIR_SERV}/stt-local/requirements.txt" ${PIP_EXTRA_STT:-})" \
    || { PEND_CORE=$((PEND_CORE + EST_VENV_STT_BYTES)); PEND_PIP=$((PEND_PIP + EST_PIP_BYTES / 2)); }
  marca_valida venv-voz-cuda "$(firma_venv voz-cuda "${DIR_SERV}/voz-local/requirements-cuda.txt" --no-deps)" \
    || { PEND_CORE=$((PEND_CORE + EST_VENV_VOZ_BYTES)); PEND_PIP=$((PEND_PIP + EST_PIP_BYTES / 2)); }
}

# Falla con 16 y un mensaje claro si lo que falta no cabe. La caché de pip es lo único prescindible: antes de
# rendirse se apaga y se vacía. statfs de un volumen de red puede informar el espacio del servidor y no la cuota
# del volumen [N]: si es así este control no dispara y un disco lleno aparece como fallo genérico con la pista de al_salir.
verificar_espacio() {
  local libre pip="si"
  [[ "$CACHE_ACTIVA" == "si" ]] || return 0
  paso espacio
  mkdir -p "$CACHE_RAIZ"
  calcular_pendientes
  libre="$(espacio_libre "$CACHE_RAIZ")"
  if (( libre < PEND_CORE + PEND_PIP + MARGEN_BYTES )); then
    pip="no"
    PIP_CACHE_OPCION=(--no-cache-dir)
    unset PIP_CACHE_DIR
    borrar_en_cache "${CACHE_RAIZ}/pip"
    libre="$(espacio_libre "$CACHE_RAIZ")"
  fi
  (( libre >= PEND_CORE + MARGEN_BYTES )) \
    || fallar 16 espacio "el volumen no tiene espacio: libres $((libre / 1048576)) MiB en ${CACHE_RAIZ}, faltan por escribir unos $((PEND_CORE / 1048576)) MiB más 1024 MiB de margen. Agrandar el volumen en la consola de RunPod o vaciar la caché (docs/despliegue-modelos-runpod-vm.md)"
  ok espacio "libre_mib=$((libre / 1048576)) pendiente_mib=$((PEND_CORE / 1048576)) cache_pip=${pip}"
}

# servidor.py de Kokoro lee /opt/voz/modelos, ruta fija. Con caché esa ruta es un enlace al volumen. Un directorio
# real que ya esté ahí (otra corrida sin caché) no se borra: se aparta.
enlazar_modelos_voz() {
  local destino="${CACHE_RAIZ}/voz-modelos"
  mkdir -p "$destino" "${MODELOS_VOZ%/*}"
  if [[ -L "$MODELOS_VOZ" ]]; then
    [[ "$(readlink -- "$MODELOS_VOZ")" == "$destino" ]] || ln -sfn "$destino" "$MODELOS_VOZ"
    return 0
  fi
  if [[ -e "$MODELOS_VOZ" ]]; then
    mv -- "$MODELOS_VOZ" "${MODELOS_VOZ}.previo.$(date +%s)"
  fi
  ln -s "$destino" "$MODELOS_VOZ"
}

# Bytes que ocupa un componente recién construido, para anotarlos en su marca (así se reemplazan las estimaciones).
medir_bytes() {
  local salida
  salida="$(du -sb -- "$1" 2>/dev/null || true)"
  printf '%s' "${salida%%[[:space:]]*}"
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

# Baja el GGUF a .parcial y lo verifica. Un .parcial que viene de una corrida interrumpida se reanuda con -C -,
# pero si al final no verifica se borra y se reintenta UNA vez desde cero: un .parcial corrupto no puede dejar
# la caché fallando para siempre. Un .parcial más grande que el archivo (curl daría 416) se descarta antes.
bajar_gguf() {
  local parcial="$1" reanuda="no" motivo
  if [[ -e "$parcial" ]]; then
    reanuda="si"
    if (( $(tamano_de "$parcial") > GGUF_BYTES )); then
      descartar "$parcial"
      reanuda="no"
    fi
  fi
  for _ in 1 2; do
    curl -fL --retry 5 --retry-delay 5 -C - -o "$parcial" "$GGUF_URL"
    if [[ "$(tamano_de "$parcial")" != "$GGUF_BYTES" ]]; then
      motivo="el GGUF bajó $(tamano_de "$parcial") bytes y se esperaban ${GGUF_BYTES}"
    elif [[ "$(sha256_de "$parcial")" != "$GGUF_SHA256" ]]; then
      motivo="sha256 del GGUF distinto del lockfile. No se registra el modelo"
    else
      return 0
    fi
    descartar "$parcial"
    [[ "$reanuda" == "si" ]] || fallar 3 gguf "$motivo"
    printf '[aviso] el .parcial reanudado no verificó (%s): se baja de nuevo desde cero\n' "$motivo"
    reanuda="no"
  done
  fallar 3 gguf "$motivo"
}

descargar_gguf() {
  local parcial="${GGUF_RUTA}.parcial"
  paso gguf
  mkdir -p "$GGUF_DIR"
  if gguf_correcto "$GGUF_RUTA"; then
    componente gguf cache
    ok gguf "ya presente y verificado"
    return 0
  fi
  exigir_comandos curl sha256sum stat
  if [[ -e "$GGUF_RUTA" ]]; then
    printf '[aviso] el GGUF presente no coincide con el tamaño o el sha256 del lockfile: se descarta y se baja de nuevo\n'
    descartar "$GGUF_RUTA"
    descartar "$parcial"
  fi
  bajar_gguf "$parcial"
  mv "$parcial" "$GGUF_RUTA"
  componente gguf descargado
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
  "${VENV_DIR}/${nombre}/bin/python" -m pip install "${PIP_CACHE_OPCION[@]}" --only-binary=:all: -q "$@" -r "$requisitos"
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

# Comprobación de importación del venv de Whisper. Sirve de prueba de humo al reutilizarlo desde la caché.
COMPROBACION_VENV_STT_PY='import av, faster_whisper, huggingface_hub'

# Un venv de la caché sirve si su Python arranca y sus paquetes se importan. El de Kokoro además no puede tener
# onnxruntime (CPU) estorbando a onnxruntime-gpu.
venv_sano() { # NOMBRE
  local py="${VENV_DIR}/$1/bin/python"
  [[ -x "$py" ]] || return 1
  case "$1" in
    stt) "$py" -c "$COMPROBACION_VENV_STT_PY" >/dev/null 2>&1 ;;
    voz-cuda) "$py" -c "$COMPROBACION_VENV_CUDA_PY" >/dev/null 2>&1 ;;
    *) "$py" -c 'import sys' >/dev/null 2>&1 ;;
  esac
}

# venv_en_cache NOMBRE REQUISITOS [ARGS_PIP...]: reutiliza el venv del volumen solo si su marca coincide con la
# firma (requisitos, argumentos de pip, Python completo, sistema) y sus paquetes importan. Si no, lo borra entero,
# lo construye en su sitio definitivo (las rutas absolutas del venv impiden construirlo en otra carpeta y mover)
# y escribe la marca AL FINAL: un pod que muere a la mitad deja un venv sin marca, que la próxima corrida descarta.
venv_en_cache() {
  local nombre="$1" requisitos="$2" firma motivo="" dir
  shift 2
  dir="${VENV_DIR}/${nombre}"
  firma="$(firma_venv "$nombre" "$requisitos" "$@")"
  if marca_valida "venv-${nombre}" "$firma"; then
    if venv_sano "$nombre"; then
      componente "venv-${nombre}" cache
      return 0
    fi
    motivo="la marca es válida pero el venv no arranca o no importa"
  elif [[ -e "$dir" ]]; then
    motivo="marca ausente, ilegible o de otro Python, sistema o requisitos"
  fi
  if [[ -n "$motivo" ]]; then
    printf '[aviso] venv %s descartado: %s\n' "$nombre" "$motivo"
  fi
  quitar_marca "venv-${nombre}"
  borrar_en_cache "$dir"
  crear_venv "$nombre" "$requisitos" "$@"
  if ! venv_sano "$nombre"; then
    [[ "$nombre" == "voz-cuda" ]] \
      && fallar 15 venvs "el venv voz-cuda no sirve: onnxruntime (CPU) instalado o sin CUDAExecutionProvider"
    fallar 1 venvs "el venv ${nombre} recién instalado no importa sus paquetes"
  fi
  escribir_marca "venv-${nombre}" "$firma" "bytes=$(medir_bytes "$dir")"
  componente "venv-${nombre}" "${motivo:+re}construido"
}

instalar_venvs() {
  paso venvs
  mkdir -p "$VENV_DIR"
  if [[ "$CACHE_ACTIVA" == "si" ]]; then
    # shellcheck disable=SC2086
    venv_en_cache stt "${DIR_SERV}/stt-local/requirements.txt" ${PIP_EXTRA_STT:-}
    venv_en_cache voz-cuda "${DIR_SERV}/voz-local/requirements-cuda.txt" --no-deps
  else
    # shellcheck disable=SC2086
    crear_venv stt "${DIR_SERV}/stt-local/requirements.txt" ${PIP_EXTRA_STT:-}
    # requirements-cuda.txt es un conjunto cerrado: --no-deps es obligatorio.
    crear_venv voz-cuda "${DIR_SERV}/voz-local/requirements-cuda.txt" --no-deps
    "${VENV_DIR}/voz-cuda/bin/python" -c "$COMPROBACION_VENV_CUDA_PY" \
      || fallar 15 venvs "el venv voz-cuda no sirve: onnxruntime (CPU) instalado o sin CUDAExecutionProvider"
  fi
  ok venvs
}

descargar_whisper() {
  "${VENV_DIR}/stt/bin/python" -c 'from huggingface_hub import snapshot_download as d; print(d("Systran/faster-whisper-small"))' >/dev/null
}

# Los blobs LFS de Hugging Face se llaman como su sha256: es una verificación real, sin lockfile propio. Un
# .incomplete (descarga interrumpida) invalida el conjunto. Debe haber al menos un blob verificable y model.bin.
blobs_hf_validos() { # CARPETA_DEL_REPO
  local dir="$1" blob nombre contados=0
  [[ -d "${dir}/blobs" ]] || return 1
  for blob in "${dir}"/blobs/*; do
    [[ -e "$blob" ]] || continue
    nombre="${blob##*/}"
    [[ "$nombre" != *.incomplete ]] || return 1
    if [[ "$nombre" =~ ^[0-9a-f]{64}$ ]]; then
      [[ "$(sha256_de "$blob")" == "$nombre" ]] || return 1
      contados=$((contados + 1))
    fi
  done
  (( contados >= 1 )) && [[ -n "$(compgen -G "${dir}/snapshots/*/model.bin")" ]]
}

whisper_en_cache() {
  local dir="${HF_HUB_CACHE}/models--Systran--faster-whisper-small" firma motivo=""
  firma="$(firma_whisper)"
  if marca_valida whisper-small "$firma"; then
    if blobs_hf_validos "$dir"; then
      componente whisper-small cache
      return 0
    fi
    motivo="un blob no coincide con su sha256 o falta model.bin"
  elif [[ -e "$dir" ]]; then
    motivo="marca ausente o ilegible"
  fi
  if [[ -n "$motivo" ]]; then
    printf '[aviso] pesos de Whisper descartados: %s\n' "$motivo"
  fi
  quitar_marca whisper-small
  borrar_en_cache "$dir"
  descargar_whisper
  blobs_hf_validos "$dir" || fallar 3 pesos "los pesos de Whisper no pasan la verificación sha256 de sus blobs"
  escribir_marca whisper-small "$firma" "bytes=$(medir_bytes "$dir")"
  componente whisper-small "${motivo:+re}descargado"
}

descargar_pesos() {
  local salida
  paso pesos
  if [[ "$CACHE_ACTIVA" == "si" ]]; then
    enlazar_modelos_voz
    whisper_en_cache
  else
    descargar_whisper
  fi
  # descargar.py verifica sha256 y tamaño y usa .parcial con os.replace. Con GPU=1 baja además el fp32.
  # El fp16 nunca: en CUDA dio audio vacío. Con caché vuelve a verificar los tres pesos en cada arranque.
  salida="$(VERA_VOZ_DESCARGAR_GPU=1 VERA_VOZ_DESCARGAR_FP16=0 VERA_VOZ_MODELOS_DESTINO="$MODELOS_VOZ" \
    "${VENV_DIR}/voz-cuda/bin/python" "${DIR_SERV}/voz-local/descargar.py")"
  if [[ "$salida" == *"Modelo verificado:"* ]]; then
    componente kokoro-pesos descargado
  else
    componente kokoro-pesos cache
  fi
  ok pesos
}

# ---------- servicios ----------
# lanzar NOMBRE COMANDO...: desacoplado de la sesión ssh (setsid, stdin nulo, salida a un log). Si algún hijo
# heredara los descriptores de la sesión, ssh no volvería nunca.
lanzar() {
  local nombre="$1" log="${LOGS}/$1.log"
  shift
  : > "$log"
  # 9>&-: el servicio no hereda el bloqueo de la corrida (fd 9), o impediría la siguiente.
  setsid nohup "$@" > "$log" 2>&1 < /dev/null 9>&- 8>&- &
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
  if [[ "$CACHE_ACTIVA" == "si" ]]; then
    # El almacén de Ollama de la caché solo vale con su marca (escrita tras calentar y comprobar la VRAM).
    if modelo_registrado && marca_valida ollama-modelo "$(firma_ollama)"; then
      componente ollama-modelo cache
      return 0
    fi
    if modelo_registrado; then
      printf '[aviso] el modelo está registrado pero sin marca válida: se rehace desde el GGUF verificado\n'
      OLLAMA_HOST="$OLLAMA_HOST_PORT" ollama rm "$MODELO_OLLAMA" >/dev/null 2>&1 || true
    fi
    quitar_marca ollama-modelo
    componente ollama-modelo creado
  elif modelo_registrado; then
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
  local rc=0 t0
  paso ollama
  mkdir -p "$OLLAMA_MODELS"
  (
    export OLLAMA_HOST="$OLLAMA_HOST_PORT" OLLAMA_NUM_PARALLEL OLLAMA_KEEP_ALIVE OLLAMA_MODELS
    lanzar ollama ollama serve
  )
  esperar_salud ollama "${PIDS}/ollama.pid" 180 sano_ollama || rc=$?
  (( rc == 0 )) || fallar 4 ollama "Ollama no respondió (rc=${rc}). Log: $(cola_log ollama)"
  t0=$SECONDS
  registrar_modelo
  calentar_modelo || fallar 4 ollama "el calentamiento del modelo falló. Log: $(cola_log ollama)"
  subfase ollama_carga "$t0"
  comprobar_vram
  if [[ "$CACHE_ACTIVA" == "si" ]] && ! marca_valida ollama-modelo "$(firma_ollama)"; then
    escribir_marca ollama-modelo "$(firma_ollama)" "bytes=$(medir_bytes "$OLLAMA_MODELS")"
  fi
}

plan_seco() {
  decidir_volumen
  aplicar_rutas_cache
  cat <<PLAN
arrancar-modelos.sh en modo seco: no se ejecuta nada.
Caché en volumen: ${CACHE_ACTIVA} (VERA_VOLUMEN_MONTADO=${VOLUMEN_PEDIDO}, raíz ${CACHE_RAIZ:-ninguna})${CACHE_AVISO:+. AVISO: ${CACHE_AVISO}}
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

# Una sola corrida a la vez. Se toma ANTES de truncar progreso.jsonl: una segunda corrida no borra el progreso
# de la primera, sale con 5 sin escribir nada y quien la lanzó sigue leyendo el progreso de la que corre.
tomar_bloqueo() {
  local rc=0
  mkdir -p "$ESTADO_DIR"
  exec 9> "${ESTADO_DIR}/arrancar.lock"
  if ! flock -n 9; then
    LINEA_FINAL="si"
    printf '[aviso] ya hay otra corrida de arrancar-modelos.sh en curso\n' >&2
    exit 5
  fi
  [[ "$CACHE_ACTIVA" == "si" ]] || return 0
  # Segundo candado, en el propio volumen, para que dos pods sobre la misma caché no la escriban a la vez. El
  # candado de arriba ya es local al pod porque el estado no va al volumen. -E 75 separa la contención real de
  # un volumen que no soporta flock. Se espera: un pod terminado a la fuerza puede dejar el candado de red
  # vivo hasta que el servidor lo suelte.
  mkdir -p "$CACHE_RAIZ"
  exec 8> "${CACHE_RAIZ}/.candado"
  flock -w "$CANDADO_ESPERA_S" -E 75 8 || rc=$?
  case "$rc" in
    0) ;;
    75)
      LINEA_FINAL="si"
      printf '[aviso] otra corrida tiene el candado de la caché %s desde hace más de %s s\n' "$CACHE_RAIZ" "$CANDADO_ESPERA_S" >&2
      exit 5
      ;;
    *) printf '[aviso] el volumen no admite flock (rc=%s): la caché queda protegida solo por el candado local del pod\n' "$rc" ;;
  esac
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
  local solo_preparar="no"
  case "${1:-}" in
    --seco) plan_seco; return 0 ;;
    --compuerta) solo_compuerta; return 0 ;;
    --preparar) solo_preparar="si" ;;
    "") ;;
    *) printf 'uso: arrancar-modelos.sh [--seco|--compuerta|--preparar]\n' >&2; exit 2 ;;
  esac
  validar_entradas
  exigir_comandos curl tar sha256sum stat date pgrep pkill flock
  decidir_volumen
  aplicar_rutas_cache
  tomar_bloqueo
  preparar_estado
  progreso inicio en_curso 0 "arranque de modelos, P=${OLLAMA_NUM_PARALLEL}"
  if [[ "$VOLUMEN_PEDIDO" == "1" ]]; then
    paso volumen
    [[ -z "$CACHE_AVISO" ]] || printf '[aviso] %s\n' "$CACHE_AVISO" >&2
    ok volumen "cache=${CACHE_ACTIVA} raiz=${CACHE_RAIZ:-ninguna} montaje=$(describir_montaje)${CACHE_AVISO:+ aviso=${CACHE_AVISO}}"
  fi
  paso entorno
  elegir_python
  ok entorno "python=${PYTHON_BIN}"
  calcular_firmas
  compuerta_cuda
  cargar_tokens
  if [[ "$FORZAR" != "1" && "$solo_preparar" == "no" ]] && todo_sano; then
    progreso salud listo 0 "los tres servicios ya respondían: reutilizados sin reiniciar"
    LINEA_FINAL="si"
    return 0
  fi
  obtener_codigo
  verificar_espacio
  instalar_basicos
  instalar_ollama
  descargar_gguf
  instalar_venvs
  descargar_pesos
  if [[ "$solo_preparar" == "si" ]]; then
    cerrar_resumen
    progreso listo listo 0 "preparación terminada, servicios sin arrancar"
    LINEA_FINAL="si"
    return 0
  fi
  detener_todo
  arrancar_whisper
  arrancar_kokoro
  arrancar_ollama
  cerrar_resumen
  progreso listo listo 0 "ollama:${OLLAMA_HOST_PORT} whisper:${PUERTO_WHISPER} kokoro:${PUERTO_KOKORO}"
  LINEA_FINAL="si"
}

# Solo modelos-pod_test.sh carga las funciones sin ejecutar nada (para cambiar el GGUF por uno diminuto).
[[ "${VERA_CARGAR_SIN_EJECUTAR:-0}" == "1" ]] || principal "$@"

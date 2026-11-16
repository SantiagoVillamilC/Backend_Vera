#!/usr/bin/env bash
# infra/modelos-runpod/pod/modelos-pod_test.sh
# Prueba de arrancar-modelos.sh y salud-modelos.sh SIN pod, sin red y sin GPU. Usa un nvidia-smi falso, un
# curl falso que registra argv y stdin, y tres servidores HTTP falsos en puertos libres de 127.0.0.1.
# Cubre: --seco sin efectos, la compuerta de CUDA (12.4, 13.0, driver nuevo UMD, sin nvidia-smi), tokens con
# modo inválido, la reutilización de servicios ya sanos, el informe de salud y que el token nunca viaje en argv.
# No cubre: instalación de Ollama, pip, descargas, arranque real de Whisper, Kokoro y Ollama, ni la VRAM.
# Uso: bash infra/modelos-runpod/pod/modelos-pod_test.sh
set -euo pipefail

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARRANQUE="${DIRECTORIO_SCRIPT}/arrancar-modelos.sh"
SALUD="${DIRECTORIO_SCRIPT}/salud-modelos.sh"
BASE="$(mktemp -d)"
PIDS_SERVIDORES=()
limpiar() {
  local pid
  for pid in "${PIDS_SERVIDORES[@]}"; do
    kill "$pid" 2>/dev/null || true
  done
  rm -rf "${BASE}"
}
trap limpiar EXIT

TOKEN_STT="$(printf 'a%.0s' {1..40})"
TOKEN_VOZ="$(printf 'b%.0s' {1..40})"
FALLOS=0
PRUEBAS=0

verificar() { # verificar DESCRIPCION CODIGO_ESPERADO CODIGO_REAL
  PRUEBAS=$((PRUEBAS + 1))
  if [[ "$2" == "$3" ]]; then
    printf 'ok    %s\n' "$1"
  else
    printf 'FALLA %s (esperado %s, real %s)\n' "$1" "$2" "$3"
    FALLOS=$((FALLOS + 1))
  fi
}

contiene() { # contiene DESCRIPCION ARCHIVO TEXTO
  PRUEBAS=$((PRUEBAS + 1))
  if [[ "$(< "$2")" == *"$3"* ]]; then
    printf 'ok    %s\n' "$1"
  else
    printf 'FALLA %s (no aparece "%s" en %s)\n' "$1" "$3" "$2"
    FALLOS=$((FALLOS + 1))
  fi
}

no_contiene() {
  PRUEBAS=$((PRUEBAS + 1))
  if [[ "$(< "$2")" != *"$3"* ]]; then
    printf 'ok    %s\n' "$1"
  else
    printf 'FALLA %s (aparece el texto prohibido en %s)\n' "$1" "$2"
    FALLOS=$((FALLOS + 1))
  fi
}

# nvidia-smi falso: imprime la cabecera con la versión pedida en CUDA_FALSA y la etiqueta en ETIQUETA_FALSA.
crear_nvidia_smi() { # crear_nvidia_smi RUTA VERSION [ETIQUETA]
  local ruta="$1" version="$2" etiqueta="${3:-CUDA Version}"
  cat > "$ruta" <<FAKE
#!/usr/bin/env bash
if [[ "\${1:-}" == "--query-gpu=name,memory.total,driver_version" ]]; then
  printf 'NVIDIA RTX A5000, 24564 MiB, 590.00\n'
else
  printf '| NVIDIA-SMI 590.00   Driver Version: 590.00   %s: %s |\n' "${etiqueta}" "${version}"
fi
FAKE
  chmod +x "$ruta"
}

ultima_linea() { tail -n 1 "$1/progreso.jsonl"; }

correr() { # correr ESTADO ARGS... : guarda salida en ESTADO/salida.txt y devuelve el código
  local estado="$1" rc=0
  shift
  VERA_ESTADO_DIR="$estado" "$@" > "${estado}/salida.txt" 2>&1 || rc=$?
  return "$rc"
}

# ---------- arrancar-modelos.sh ----------
E="${BASE}/seco"
mkdir -p "$E"
rc=0
correr "$E" bash "$ARRANQUE" --seco || rc=$?
verificar "--seco sale con 0" 0 "$rc"
[[ ! -e "${E}/progreso.jsonl" ]] && sin_progreso=0 || sin_progreso=1
verificar "--seco no escribe progreso" 0 "$sin_progreso"
contiene "--seco imprime el plan" "${E}/salida.txt" "compuerta de CUDA"

crear_nvidia_smi "${BASE}/smi-12" "12.4"
E="${BASE}/c12"
mkdir -p "$E"
rc=0
NVIDIA_SMI="${BASE}/smi-12" correr "$E" bash "$ARRANQUE" --compuerta || rc=$?
verificar "CUDA 12.4 aborta con 14" 14 "$rc"
contiene "el progreso termina en fallo con código 14" <(ultima_linea "$E") '"estado": "fallo", "codigo": 14'

crear_nvidia_smi "${BASE}/smi-13" "13.0"
E="${BASE}/c13"
mkdir -p "$E"
rc=0
NVIDIA_SMI="${BASE}/smi-13" correr "$E" bash "$ARRANQUE" --compuerta || rc=$?
verificar "CUDA 13.0 pasa la compuerta" 0 "$rc"
contiene "el progreso termina en listo" <(ultima_linea "$E") '"paso": "compuerta", "estado": "listo"'
contiene "el detalle trae la GPU" "${E}/progreso.jsonl" 'gpu=NVIDIA RTX A5000'

crear_nvidia_smi "${BASE}/smi-umd" "13.4" "CUDA UMD Version"
E="${BASE}/cumd"
mkdir -p "$E"
rc=0
NVIDIA_SMI="${BASE}/smi-umd" correr "$E" bash "$ARRANQUE" --compuerta || rc=$?
verificar "driver nuevo (CUDA UMD Version 13.4) pasa" 0 "$rc"

E="${BASE}/csin"
mkdir -p "$E"
rc=0
NVIDIA_SMI="${BASE}/no-existe" correr "$E" bash "$ARRANQUE" --compuerta || rc=$?
verificar "sin nvidia-smi aborta con 14" 14 "$rc"

# Archivo de tokens con modo 644: el arranque completo aborta con 2 en el paso tokens.
E="${BASE}/modo"
mkdir -p "$E"
printf 'WHISPER_TOKEN=%s\nVERA_LAB_TOKEN=%s\n' "$TOKEN_STT" "$TOKEN_VOZ" > "${E}/tokens.env"
chmod 644 "${E}/tokens.env"
rc=0
NVIDIA_SMI="${BASE}/smi-13" correr "$E" bash "$ARRANQUE" || rc=$?
verificar "tokens con modo 644 abortan con 2" 2 "$rc"
contiene "el fallo queda en el paso tokens" <(ultima_linea "$E") '"paso": "tokens", "estado": "fallo", "codigo": 2'
no_contiene "el progreso no contiene el token" "${E}/progreso.jsonl" "$TOKEN_STT"

# Token corto: aborta con 2.
E="${BASE}/corto"
mkdir -p "$E"
printf 'WHISPER_TOKEN=corto\nVERA_LAB_TOKEN=%s\n' "$TOKEN_VOZ" > "${E}/tokens.env"
chmod 600 "${E}/tokens.env"
rc=0
NVIDIA_SMI="${BASE}/smi-13" correr "$E" bash "$ARRANQUE" || rc=$?
verificar "token corto aborta con 2" 2 "$rc"

# OLLAMA_NUM_PARALLEL fuera de rango.
E="${BASE}/paralelo"
mkdir -p "$E"
rc=0
OLLAMA_NUM_PARALLEL=99 NVIDIA_SMI="${BASE}/smi-13" correr "$E" bash "$ARRANQUE" || rc=$?
verificar "OLLAMA_NUM_PARALLEL=99 aborta con 2" 2 "$rc"

# ---------- servidores HTTP falsos ----------
cat > "${BASE}/servidor_falso.py" <<'PY'
import http.server, sys

token = sys.argv[1]


class Manejador(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/api/version":
            cuerpo = b'{"version": "0.40.10"}'
        elif self.path == "/api/tags":
            cuerpo = b'{"models": [{"name": "gemma4-e4b-local:latest"}]}'
        elif self.path == "/healthz" and self.headers.get("X-Vera-Token", "") == token:
            cuerpo = b'{"ok": true}'
        else:
            self.send_response(401)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        self.send_response(200)
        self.send_header("Content-Length", str(len(cuerpo)))
        self.end_headers()
        self.wfile.write(cuerpo)

    def log_message(self, *args):
        pass


servidor = http.server.HTTPServer(("127.0.0.1", 0), Manejador)
print(servidor.server_address[1], flush=True)
servidor.serve_forever()
PY

levantar_falso() { # levantar_falso TOKEN ARCHIVO_PUERTO
  python3 -I "${BASE}/servidor_falso.py" "$1" > "$2" &
  PIDS_SERVIDORES+=("$!")
  local intentos=0
  until [[ -s "$2" ]]; do
    intentos=$((intentos + 1))
    (( intentos < 50 )) || { printf 'el servidor falso no arrancó\n' >&2; exit 1; }
    sleep 0.1
  done
}

levantar_falso "" "${BASE}/p-ollama"
levantar_falso "$TOKEN_STT" "${BASE}/p-whisper"
levantar_falso "$TOKEN_VOZ" "${BASE}/p-kokoro"
P_OLLAMA="$(< "${BASE}/p-ollama")"
P_WHISPER="$(< "${BASE}/p-whisper")"
P_KOKORO="$(< "${BASE}/p-kokoro")"

# ---------- salud-modelos.sh ----------
S="${BASE}/salud"
mkdir -p "$S"
printf 'WHISPER_TOKEN=%s\nVERA_LAB_TOKEN=%s\n' "$TOKEN_STT" "$TOKEN_VOZ" > "${S}/tokens.env"
chmod 600 "${S}/tokens.env"
export VERA_PUERTO_OLLAMA="$P_OLLAMA" VERA_PUERTO_WHISPER="$P_WHISPER" VERA_PUERTO_KOKORO="$P_KOKORO"

rc=0
correr "$S" bash "$SALUD" --json || rc=$?
verificar "salud con los tres servicios sale con 0" 0 "$rc"
contiene "el JSON dice sano" "${S}/salida.txt" '"sano": true'

# Token de Whisper equivocado en el archivo: Whisper responde 401 y el script sale con 1.
S2="${BASE}/salud-mal"
mkdir -p "$S2"
printf 'WHISPER_TOKEN=%s\nVERA_LAB_TOKEN=%s\n' "$(printf 'z%.0s' {1..40})" "$TOKEN_VOZ" > "${S2}/tokens.env"
chmod 600 "${S2}/tokens.env"
rc=0
correr "$S2" bash "$SALUD" --json || rc=$?
verificar "token de Whisper equivocado sale con 1" 1 "$rc"
contiene "el JSON señala a Whisper" "${S2}/salida.txt" '"whisper": "fallo"'
contiene "y deja sano en false" "${S2}/salida.txt" '"sano": false'

# Servicio caído: puerto cerrado para Kokoro.
rc=0
VERA_PUERTO_KOKORO=1 correr "$S" bash "$SALUD" || rc=$?
verificar "Kokoro caído sale con 1" 1 "$rc"

# Archivo de tokens con modo 644.
S3="${BASE}/salud-modo"
mkdir -p "$S3"
cp "${S}/tokens.env" "${S3}/tokens.env"
chmod 644 "${S3}/tokens.env"
rc=0
correr "$S3" bash "$SALUD" || rc=$?
verificar "salud con tokens modo 644 sale con 2" 2 "$rc"

# ---------- el token nunca viaja en argv ----------
mkdir -p "${BASE}/bin-falso"
cat > "${BASE}/bin-falso/curl" <<'FAKE'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${CURL_ARGV}"
if [[ "$*" == *"-K -"* ]]; then cat >> "${CURL_STDIN}"; fi
exit 0
FAKE
chmod +x "${BASE}/bin-falso/curl"
export CURL_ARGV="${BASE}/curl-argv.txt" CURL_STDIN="${BASE}/curl-stdin.txt"
: > "$CURL_ARGV"
: > "$CURL_STDIN"
rc=0
PATH="${BASE}/bin-falso:${PATH}" correr "$S" bash "$SALUD" || rc=$?
# El curl falso no devuelve cuerpo, así que /api/tags no trae el modelo y el resultado global es 1.
# Lo que se prueba aquí es solo por dónde viajan los tokens.
verificar "con curl falso la salud corre y sale con 1 por el modelo ausente" 1 "$rc"
no_contiene "el token de Whisper no aparece en argv" "$CURL_ARGV" "$TOKEN_STT"
no_contiene "el token de Kokoro no aparece en argv" "$CURL_ARGV" "$TOKEN_VOZ"
contiene "el token de Whisper sí viaja por stdin" "$CURL_STDIN" "X-Vera-Token: ${TOKEN_STT}"

# ---------- reutilización: los tres sanos, sale sin reiniciar nada ----------
cat > "${BASE}/bin-falso/ollama" <<'FAKE'
#!/usr/bin/env bash
if [[ "${1:-}" == "list" ]]; then printf 'NAME\ngemma4-e4b-local:latest\n'; fi
exit 0
FAKE
chmod +x "${BASE}/bin-falso/ollama"
R="${BASE}/reuso"
mkdir -p "$R"
cp "${S}/tokens.env" "${R}/tokens.env"
chmod 600 "${R}/tokens.env"
rc=0
PATH="${BASE}/bin-falso:${PATH}" NVIDIA_SMI="${BASE}/smi-13" correr "$R" bash "$ARRANQUE" || rc=$?
verificar "con los tres sanos el arranque se reutiliza y sale con 0" 0 "$rc"
contiene "el progreso dice reutilizados" <(ultima_linea "$R") 'reutilizados sin reiniciar'

# ---------- tokens por entorno (el camino real de modelos-control), alias KOKORO_TOKEN y bloqueo ----------
R2="${BASE}/reuso-entorno"
mkdir -p "$R2"
rc=0
WHISPER_TOKEN="$TOKEN_STT" KOKORO_TOKEN="$TOKEN_VOZ" PATH="${BASE}/bin-falso:${PATH}" NVIDIA_SMI="${BASE}/smi-13" \
  correr "$R2" bash "$ARRANQUE" || rc=$?
verificar "tokens por entorno (KOKORO_TOKEN) con los tres sanos sale con 0" 0 "$rc"
verificar "los tokens del entorno se guardan con modo 600" 600 "$(stat -c %a "${R2}/tokens.env")"
no_contiene "el progreso no contiene el token de Whisper" "${R2}/progreso.jsonl" "$TOKEN_STT"
no_contiene "la salida no contiene el token de Kokoro" "${R2}/salida.txt" "$TOKEN_VOZ"
contiene "el origen de los tokens queda anotado" "${R2}/progreso.jsonl" "origen=entorno"

# Contrato literal con modelos-control: el texto que su preámbulo envía por stdin de "bash -s" (ver preambulo()
# en internal/modelos/control/encendido.go, que otra prueba fija en Go) ejecutado tal cual contra este script.
R2B="${BASE}/reuso-stdin"
mkdir -p "$R2B"
PREAMBULO="export WHISPER_TOKEN='${TOKEN_STT}'
export KOKORO_TOKEN='${TOKEN_VOZ}'
export REPO_SHA='0123456789abcdef0123456789abcdef01234567'
bash ${ARRANQUE} >${R2B}/remoto.log 2>&1 </dev/null || { c=\$?; tail -n 20 ${R2B}/remoto.log >&2; exit \$c; }
"
rc=0
printf '%s' "$PREAMBULO" | VERA_ESTADO_DIR="$R2B" PATH="${BASE}/bin-falso:${PATH}" NVIDIA_SMI="${BASE}/smi-13" bash -s \
  > "${R2B}/salida.txt" 2>&1 || rc=$?
verificar "el stdin literal de modelos-control sale con 0" 0 "$rc"
contiene "el progreso dice reutilizados con el stdin literal" <(ultima_linea "$R2B") 'reutilizados sin reiniciar'
no_contiene "el stdin literal no filtra el token de Whisper al progreso" "${R2B}/progreso.jsonl" "$TOKEN_STT"

# Un token del entorno demasiado corto aborta con 2 aunque exista un archivo válido.
R3="${BASE}/entorno-corto"
mkdir -p "$R3"
cp "${S}/tokens.env" "${R3}/tokens.env"
chmod 600 "${R3}/tokens.env"
rc=0
WHISPER_TOKEN="corto" KOKORO_TOKEN="$TOKEN_VOZ" NVIDIA_SMI="${BASE}/smi-13" correr "$R3" bash "$ARRANQUE" || rc=$?
verificar "token del entorno corto aborta con 2" 2 "$rc"

# Sin nvidia-smi en la corrida completa el código es 14 (terminar el pod), no 1 (reintentar).
R4="${BASE}/sin-smi-completo"
mkdir -p "$R4"
rc=0
NVIDIA_SMI="${BASE}/no-existe" correr "$R4" bash "$ARRANQUE" || rc=$?
verificar "sin nvidia-smi en la corrida completa sale con 14" 14 "$rc"

# Bloqueo: con otra corrida en curso sale con 5 y no toca el progreso.
R5="${BASE}/bloqueo"
mkdir -p "$R5"
printf 'progreso de la corrida anterior\n' > "${R5}/progreso.jsonl"
flock "${R5}/arrancar.lock" sleep 20 &
PIDS_SERVIDORES+=("$!")
sleep 0.5
rc=0
NVIDIA_SMI="${BASE}/smi-13" correr "$R5" bash "$ARRANQUE" || rc=$?
verificar "con otra corrida en curso sale con 5" 5 "$rc"
contiene "el progreso de la corrida en curso queda intacto" "${R5}/progreso.jsonl" "progreso de la corrida anterior"

# ---------- caché en volumen de red: arrancar-modelos.sh --preparar con dobles ----------
# Corre el flujo real (volumen, candado, espacio, firmas, venv, GGUF, pesos, resumen) hasta antes de arrancar los
# servicios. Los dobles: python que registra cada "pip install" y cada descarga, curl que sirve un GGUF diminuto
# (por eso correr.sh cambia GGUF_BYTES, GGUF_SHA256 y GGUF_URL tras cargar el script con VERA_CARGAR_SIN_EJECUTAR),
# mountpoint que dice qué rutas están montadas, id, apt-get y ollama. No cubre: pip, Hugging Face ni GitHub reales.
PREP="${BASE}/prep"
mkdir -p "${PREP}/bin" "${PREP}/serv/stt-local" "${PREP}/serv/voz-local"
printf 'faster-whisper==1.2.1\n' > "${PREP}/serv/stt-local/requirements.txt"
printf 'print(1)\n' > "${PREP}/serv/stt-local/servidor.py"
printf 'kokoro-onnx==0.6.1\n' > "${PREP}/serv/voz-local/requirements-cuda.txt"
printf 'VERA_VOZ_DISPOSITIVO\n' > "${PREP}/serv/voz-local/servidor.py"
printf 'print(1)\n' > "${PREP}/serv/voz-local/descargar.py"
printf 'gguf-falso' > "${PREP}/gguf.bin"
GGUF_FALSO_BYTES="$(stat -c %s "${PREP}/gguf.bin")"
GGUF_FALSO_SHA="$(sha256sum "${PREP}/gguf.bin")"
GGUF_FALSO_SHA="${GGUF_FALSO_SHA%% *}"
GGUF_FINAL="gemma-4-E4B-it-Q4_K_M.gguf"

cat > "${PREP}/bin/id" <<'FAKE'
#!/usr/bin/env bash
if [[ "${1:-}" == "-u" ]]; then echo 0; else exec /usr/bin/id "$@"; fi
FAKE
printf '#!/usr/bin/env bash\nexit 0\n' > "${PREP}/bin/apt-get"
cat > "${PREP}/bin/ollama" <<'FAKE'
#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then echo "ollama version is 0.40.10"; fi
exit 0
FAKE
cat > "${PREP}/bin/mountpoint" <<'FAKE'
#!/usr/bin/env bash
[[ ":${MONTADOS_FALSOS:-}:" == *":${2:-}:"* ]]
FAKE
cat > "${PREP}/bin/curl" <<'FAKE'
#!/usr/bin/env bash
printf 'curl %s\n' "$*" >> "${PREP_LOG}"
salida="" url=""
while (($#)); do
  case "$1" in
    -o) salida="$2"; shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
if [[ "$url" == "http://gguf.falso/x" ]]; then cp "${PREP_GGUF}" "$salida"; exit 0; fi
exit 22
FAKE
cat > "${PREP}/python-falso" <<'FAKE'
#!/usr/bin/env bash
case "${1:-}" in
  -m)
    if [[ "${2:-}" == "venv" ]]; then
      mkdir -p "$3/bin"
      cp "${PREP_VENV_PY}" "$3/bin/python"
      chmod +x "$3/bin/python"
      printf 'venv %s\n' "$3" >> "${PREP_LOG}"
    fi
    exit 0 ;;
  -c)
    if [[ "${2:-}" == *"sys.version"* ]]; then printf '%s /usr/bin/python3.12\n' "${PY_VERSION_FALSA:-3.12.3}"; fi
    exit 0 ;;
esac
exit 0
FAKE
cat > "${PREP}/venv-python" <<'FAKE'
#!/usr/bin/env bash
dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
case "${1:-}" in
  -m)
    printf 'pip %s %s PIP_CACHE_DIR=%s\n' "$dir" "$*" "${PIP_CACHE_DIR:-}" >> "${PREP_LOG}"
    : > "${dir}/instalado-$$"
    exit 0 ;;
  -c)
    if [[ "${2:-}" == *snapshot_download* ]]; then
      base="${HF_HUB_CACHE:-${HF_HOME}/hub}/models--Systran--faster-whisper-small"
      mkdir -p "${base}/blobs" "${base}/snapshots/abc"
      huella="$(printf 'pesos-whisper-falsos' | sha256sum)"
      huella="${huella%% *}"
      printf 'pesos-whisper-falsos' > "${base}/blobs/${huella}"
      ln -sf "../../blobs/${huella}" "${base}/snapshots/abc/model.bin"
      printf 'snapshot\n' >> "${PREP_LOG}"
      exit 0
    fi
    if [[ "${2:-}" == *onnxruntime* ]]; then exit "${ONNX_FALLA:-0}"; fi
    exit 0 ;;
  *descargar.py)
    destino="${VERA_VOZ_MODELOS_DESTINO}"
    mkdir -p "$destino"
    if [[ -f "${destino}/kokoro-v1.0.onnx" ]]; then
      echo "Modelo ya presente y verificado: kokoro"
    else
      for f in kokoro-v1.0.onnx kokoro-v1.0.int8.onnx voices-v1.0.bin; do echo x > "${destino}/${f}"; done
      printf 'descarga-kokoro\n' >> "${PREP_LOG}"
      echo "Modelo verificado: kokoro"
    fi
    exit 0 ;;
esac
exit 0
FAKE
cat > "${PREP}/correr.sh" <<'FAKE'
#!/usr/bin/env bash
export VERA_CARGAR_SIN_EJECUTAR=1
# shellcheck source=/dev/null
source "${ARRANQUE}"
GGUF_BYTES="${GGUF_FALSO_BYTES}"
GGUF_SHA256="${GGUF_FALSO_SHA}"
GGUF_URL="http://gguf.falso/x"
EST_OLLAMA_BYTES=1000 EST_WHISPER_BYTES=1000 KOKORO_BYTES=1000 EST_VENV_STT_BYTES=1000
EST_VENV_VOZ_BYTES=1000 EST_PIP_BYTES=1000 MARGEN_BYTES=1000
principal --preparar
FAKE
chmod +x "${PREP}"/bin/* "${PREP}/python-falso" "${PREP}/venv-python" "${PREP}/correr.sh"
export PREP_LOG="${PREP}/registro.log" PREP_GGUF="${PREP}/gguf.bin" PREP_VENV_PY="${PREP}/venv-python"
export ARRANQUE GGUF_FALSO_BYTES GGUF_FALSO_SHA

preparar() { # preparar ESTADO [VAR=valor...]: ejecuta --preparar y deja la salida en ESTADO/salida.txt
  local estado="$1" rc=0
  shift
  mkdir -p "$estado"
  env PATH="${PREP}/bin:${PATH}" NVIDIA_SMI="${BASE}/smi-13" PYTHON_BIN="${PREP}/python-falso" \
    VERA_SERVIDORES_DIR="${PREP}/serv" VERA_ESTADO_DIR="$estado" VERA_MODELOS_VOZ_RUTA="${estado}/opt-voz" \
    WHISPER_TOKEN="$TOKEN_STT" KOKORO_TOKEN="$TOKEN_VOZ" "$@" bash "${PREP}/correr.sh" > "${estado}/salida.txt" 2>&1 || rc=$?
  return "$rc"
}
contar() { # contar ARCHIVO TEXTO: líneas que contienen TEXTO
  local n=0 linea
  while IFS= read -r linea; do
    if [[ "$linea" == *"$2"* ]]; then n=$((n + 1)); fi
  done < "$1"
  printf '%s' "$n"
}
existe() { if [[ -e "$1" || -L "$1" ]]; then printf 'si'; else printf 'no'; fi; }
cantidad() { local a=("$@"); local n=0 x; for x in "${a[@]}"; do [[ -e "$x" ]] && n=$((n + 1)); done; printf '%s' "$n"; }

# 1. Sin volumen: el comportamiento de siempre (pip sin caché, sin marcas, sin enlace, resumen igualmente).
SV="${BASE}/sv"
: > "$PREP_LOG"
rc=0
preparar "${BASE}/sv-estado" VERA_VENV_DIR="${SV}/venv" VERA_GGUF_DIR="${SV}/gguf" HF_HOME="${SV}/hf" OLLAMA_MODELS="${SV}/ollama" || rc=$?
verificar "sin volumen: --preparar sale con 0" 0 "$rc"
verificar "sin volumen: dos pip install con --no-cache-dir" 2 "$(contar "$PREP_LOG" "--no-cache-dir")"
verificar "sin volumen: el GGUF se baja una vez" 1 "$(contar "$PREP_LOG" "gguf.falso")"
verificar "sin volumen: no hay marcas ni vera-cache" no "$(existe "${SV}/vera-cache")"
verificar "sin volumen: /opt/voz/modelos no es un enlace" no "$([[ -L "${BASE}/sv-estado/opt-voz" ]] && echo si || echo no)"
verificar "sin volumen: queda la marca .requisitos.sha256 de siempre" si "$(existe "${SV}/venv/stt/.requisitos.sha256")"
contiene "sin volumen: imprime el resumen con cache=no" "${BASE}/sv-estado/salida.txt" "resumen de tiempos (cache=no)"
contiene "el progreso trae la línea resumen" "${BASE}/sv-estado/progreso.jsonl" '"paso": "resumen"'
contiene "el progreso termina en listo" <(ultima_linea "${BASE}/sv-estado") '"paso": "listo", "estado": "listo"'

# 2. Con volumen vacío: llena la caché y deja marcas, con el estado fuera del volumen.
WS="${BASE}/ws"
CACHE="${WS}/vera-cache"
mkdir -p "$WS" "${BASE}/pb-estado/opt-voz"
printf 'viejo' > "${BASE}/pb-estado/opt-voz/viejo.bin"
: > "$PREP_LOG"
rc=0
preparar "${BASE}/pb-estado" VERA_VOLUMEN_MONTADO=1 VERA_VOLUMEN_RAIZ="$WS" MONTADOS_FALSOS="$WS" || rc=$?
verificar "volumen vacío: --preparar sale con 0" 0 "$rc"
verificar "volumen vacío: marca del venv de Whisper" si "$(existe "${CACHE}/marcas/venv-stt.completo")"
verificar "volumen vacío: marca del venv de Kokoro" si "$(existe "${CACHE}/marcas/venv-voz-cuda.completo")"
verificar "volumen vacío: marca de los pesos de Whisper" si "$(existe "${CACHE}/marcas/whisper-small.completo")"
verificar "volumen vacío: el GGUF queda en la caché" si "$(existe "${CACHE}/gguf/${GGUF_FINAL}")"
verificar "volumen vacío: sin .parcial" no "$(existe "${CACHE}/gguf/${GGUF_FINAL}.parcial")"
verificar "volumen vacío: /opt/voz/modelos apunta a la caché" "${CACHE}/voz-modelos" "$(readlink "${BASE}/pb-estado/opt-voz")"
verificar "volumen vacío: el directorio real previo se aparta, no se borra" 1 "$(cantidad "${BASE}"/pb-estado/opt-voz.previo.*)"
verificar "volumen vacío: pip usa la caché del volumen (sin --no-cache-dir)" 0 "$(contar "$PREP_LOG" "--no-cache-dir")"
contiene "volumen vacío: PIP_CACHE_DIR cuelga de la caché" "$PREP_LOG" "PIP_CACHE_DIR=${CACHE}/pip"
verificar "volumen vacío: Kokoro se descarga una vez" 1 "$(contar "$PREP_LOG" "descarga-kokoro")"
verificar "el estado y los tokens no van al volumen" no "$(existe "${WS}/vera-estado")"
verificar "ningún token dentro de la caché" 0 "$(contar "${CACHE}/marcas/venv-stt.completo" "$TOKEN_STT")"
contiene "el resumen anota los componentes de la caché" "${BASE}/pb-estado/progreso.jsonl" "venv-stt=construido"
contiene "la marca guarda la firma en la primera línea" <(head -n 1 "${CACHE}/marcas/venv-stt.completo") "firma="

# 3. Con volumen lleno: reutiliza y no descarga ni instala nada.
: > "$PREP_LOG"
rc=0
preparar "${BASE}/pb-estado" VERA_VOLUMEN_MONTADO=1 VERA_VOLUMEN_RAIZ="$WS" MONTADOS_FALSOS="$WS" || rc=$?
verificar "volumen lleno: sale con 0" 0 "$rc"
verificar "volumen lleno: ningún pip install" 0 "$(contar "$PREP_LOG" "pip ")"
verificar "volumen lleno: ninguna descarga del GGUF" 0 "$(contar "$PREP_LOG" "gguf.falso")"
verificar "volumen lleno: ninguna descarga de Whisper" 0 "$(contar "$PREP_LOG" "snapshot")"
verificar "volumen lleno: ninguna descarga de Kokoro" 0 "$(contar "$PREP_LOG" "descarga-kokoro")"
contiene "volumen lleno: venv-stt viene de la caché" "${BASE}/pb-estado/salida.txt" "venv-stt=cache"
contiene "volumen lleno: whisper-small viene de la caché" "${BASE}/pb-estado/salida.txt" "whisper-small=cache"
contiene "volumen lleno: el GGUF viene de la caché" "${BASE}/pb-estado/salida.txt" "gguf=cache"

# 4. Marca corrupta: se rehace solo ese componente y no quedan restos del venv anterior.
printf 'basura sin firma\n' > "${CACHE}/marcas/venv-stt.completo"
: > "$PREP_LOG"
rc=0
preparar "${BASE}/pb-estado" VERA_VOLUMEN_MONTADO=1 VERA_VOLUMEN_RAIZ="$WS" MONTADOS_FALSOS="$WS" || rc=$?
verificar "marca corrupta: sale con 0" 0 "$rc"
verificar "marca corrupta: se reinstala solo el venv de Whisper" 1 "$(contar "$PREP_LOG" "pip ")"
contiene "marca corrupta: el pip fue el de stt" "$PREP_LOG" "venv/stt "
verificar "marca corrupta: el venv anterior se borró entero" 1 "$(cantidad "${CACHE}"/venv/stt/instalado-*)"
contiene "marca corrupta: queda reconstruido" "${BASE}/pb-estado/salida.txt" "venv-stt=reconstruido"

# 5. Arranque interrumpido: venv sin marca y con restos a medias nunca se reutiliza.
rm -f "${CACHE}/marcas/venv-voz-cuda.completo"
printf 'a medias' > "${CACHE}/venv/voz-cuda/medio-escrito"
: > "$PREP_LOG"
rc=0
preparar "${BASE}/pb-estado" VERA_VOLUMEN_MONTADO=1 VERA_VOLUMEN_RAIZ="$WS" MONTADOS_FALSOS="$WS" || rc=$?
verificar "venv sin marca: sale con 0" 0 "$rc"
verificar "venv sin marca: se rehace solo el de Kokoro" 1 "$(contar "$PREP_LOG" "pip ")"
verificar "venv sin marca: los restos a medias desaparecen" no "$(existe "${CACHE}/venv/voz-cuda/medio-escrito")"
printf 'a medias' > "${CACHE}/gguf/${GGUF_FINAL}.parcial.x"

# 6. Otra versión de Python: los dos venv se rehacen, los pesos no.
: > "$PREP_LOG"
rc=0
preparar "${BASE}/pb-estado" VERA_VOLUMEN_MONTADO=1 VERA_VOLUMEN_RAIZ="$WS" MONTADOS_FALSOS="$WS" PY_VERSION_FALSA="3.13.0" || rc=$?
verificar "otro Python: sale con 0" 0 "$rc"
verificar "otro Python: los dos venv se rehacen" 2 "$(contar "$PREP_LOG" "pip ")"
verificar "otro Python: Whisper no se vuelve a bajar" 0 "$(contar "$PREP_LOG" "snapshot")"

# 7. sha256 del GGUF distinto: se descarta y se baja de nuevo (con un .parcial corrupto de una corrida anterior).
printf 'XXXXXXXXXX' > "${CACHE}/gguf/${GGUF_FINAL}"
printf 'corrupto' > "${CACHE}/gguf/${GGUF_FINAL}.parcial"
: > "$PREP_LOG"
rc=0
preparar "${BASE}/pb-estado" VERA_VOLUMEN_MONTADO=1 VERA_VOLUMEN_RAIZ="$WS" MONTADOS_FALSOS="$WS" || rc=$?
verificar "GGUF corrupto: sale con 0" 0 "$rc"
verificar "GGUF corrupto: se baja de nuevo una vez" 1 "$(contar "$PREP_LOG" "gguf.falso")"
verificar "GGUF corrupto: el archivo final vuelve a verificar" "$GGUF_FALSO_SHA" "$(sha256sum "${CACHE}/gguf/${GGUF_FINAL}" | cut -d' ' -f1)"
contiene "GGUF corrupto: avisa que lo descarta" "${BASE}/pb-estado/salida.txt" "se descarta y se baja de nuevo"

# 8. Pesos de Whisper con un blob que no coincide con su sha256.
BLOB="$(printf '%s\n' "${CACHE}"/hf/hub/models--Systran--faster-whisper-small/blobs/*)"
printf 'alterado' > "$BLOB"
: > "$PREP_LOG"
rc=0
preparar "${BASE}/pb-estado" VERA_VOLUMEN_MONTADO=1 VERA_VOLUMEN_RAIZ="$WS" MONTADOS_FALSOS="$WS" || rc=$?
verificar "blob alterado: sale con 0" 0 "$rc"
verificar "blob alterado: Whisper se baja de nuevo" 1 "$(contar "$PREP_LOG" "snapshot")"
verificar "blob alterado: no se reinstala ningún venv" 0 "$(contar "$PREP_LOG" "pip ")"

# 9. Variable sin montaje real: se ignora el volumen con un aviso y no se crea vera-cache.
WS2="${BASE}/ws2"
mkdir -p "$WS2"
: > "$PREP_LOG"
rc=0
preparar "${BASE}/sm-estado" VERA_VOLUMEN_MONTADO=1 VERA_VOLUMEN_RAIZ="$WS2" MONTADOS_FALSOS="" \
  VERA_VENV_DIR="${WS2}/venv" VERA_GGUF_DIR="${WS2}/gguf" HF_HOME="${WS2}/hf" OLLAMA_MODELS="${WS2}/ollama" || rc=$?
verificar "sin montaje real: sale con 0" 0 "$rc"
contiene "sin montaje real: avisa que ignora el volumen" "${BASE}/sm-estado/salida.txt" "se ignora el volumen"
verificar "sin montaje real: no crea vera-cache" no "$(existe "${WS2}/vera-cache")"
verificar "sin montaje real: pip sin caché, como siempre" 2 "$(contar "$PREP_LOG" "--no-cache-dir")"

# 10. Espacio insuficiente: sale con 16 antes de descargar nada. stat falso con 1 bloque libre.
WS3="${BASE}/ws3"
mkdir -p "$WS3" "${PREP}/bin-stat"
cat > "${PREP}/bin-stat/stat" <<'FAKE'
#!/usr/bin/env bash
if [[ "${1:-}" == "-f" ]]; then printf '1 4096\n'; else exec /usr/bin/stat "$@"; fi
FAKE
chmod +x "${PREP}/bin-stat/stat"
: > "$PREP_LOG"
rc=0
PATH="${PREP}/bin-stat:${PATH}" preparar "${BASE}/ne-estado" VERA_VOLUMEN_MONTADO=1 VERA_VOLUMEN_RAIZ="$WS3" MONTADOS_FALSOS="$WS3" || rc=$?
verificar "espacio insuficiente: sale con 16" 16 "$rc"
contiene "espacio insuficiente: el progreso termina en fallo 16" <(ultima_linea "${BASE}/ne-estado") '"estado": "fallo", "codigo": 16'
verificar "espacio insuficiente: no se descargó nada" 0 "$(contar "$PREP_LOG" "gguf.falso")"

# 11. Candado del volumen: otra corrida lo tiene, sale con 5 sin tocar el progreso.
flock "${CACHE}/.candado" sleep 20 &
PIDS_SERVIDORES+=("$!")
sleep 0.5
printf 'progreso previo\n' > "${BASE}/pb-estado/progreso.jsonl"
rc=0
preparar "${BASE}/pb-estado" VERA_VOLUMEN_MONTADO=1 VERA_VOLUMEN_RAIZ="$WS" MONTADOS_FALSOS="$WS" VERA_CANDADO_ESPERA_S=1 || rc=$?
verificar "candado del volumen tomado: sale con 5" 5 "$rc"

# 12. Borrado acotado: nunca fuera de vera-cache, ni siquiera por un enlace simbólico dentro de ella.
printf 'no me borres' > "${WS}/fuera.txt"
ln -sfn "${WS}/fuera.txt" "${CACHE}/enlace-afuera"
borrar() { # borrar RUTA : borra con borrar_en_cache dentro de una caché activa en CACHE
  local rc=0
  bash -c 'export VERA_CARGAR_SIN_EJECUTAR=1; source "$1"; CACHE_ACTIVA=si; CACHE_RAIZ="$2"; borrar_en_cache "$3"' _ "$ARRANQUE" "$CACHE" "$1" \
    > "${BASE}/borrar.txt" 2>&1 || rc=$?
  return "$rc"
}
rc=0; borrar "${WS}/fuera.txt" || rc=$?
verificar "borrar fuera de la caché sale con 1" 1 "$rc"
verificar "el archivo de fuera sobrevive" si "$(existe "${WS}/fuera.txt")"
rc=0; borrar "${CACHE}/enlace-afuera" || rc=$?
verificar "borrar un enlace que sale de la caché sale con 1" 1 "$rc"
verificar "el destino del enlace sobrevive" si "$(existe "${WS}/fuera.txt")"
mkdir -p "${CACHE}/prueba"
rc=0; borrar "${CACHE}/prueba" || rc=$?
verificar "borrar dentro de la caché sale con 0" 0 "$rc"
verificar "lo de dentro se borró" no "$(existe "${CACHE}/prueba")"

# 13. --seco: dice si usa la caché y dónde queda el estado (sin tocar nada).
rc=0
VERA_VOLUMEN_MONTADO=1 VERA_VOLUMEN_RAIZ="$WS" MONTADOS_FALSOS="$WS" PATH="${PREP}/bin:${PATH}" bash "$ARRANQUE" --seco > "${BASE}/seco-vol.txt" 2>&1 || rc=$?
verificar "--seco con volumen sale con 0" 0 "$rc"
contiene "--seco con volumen anuncia la caché" "${BASE}/seco-vol.txt" "Caché en volumen: si"
contiene "--seco con volumen deja el estado fuera del volumen" "${BASE}/seco-vol.txt" "/var/lib/vera-estado/progreso.jsonl"
rc=0
bash "$ARRANQUE" --seco > "${BASE}/seco-sin.txt" 2>&1 || rc=$?
contiene "--seco sin volumen conserva el estado de siempre" "${BASE}/seco-sin.txt" "/workspace/vera-estado/progreso.jsonl"
contiene "--seco sin volumen dice que no hay caché" "${BASE}/seco-sin.txt" "Caché en volumen: no"

printf '\n%s pruebas, %s fallas\n' "$PRUEBAS" "$FALLOS"
[[ "$FALLOS" -eq 0 ]]

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

printf '\n%s pruebas, %s fallas\n' "$PRUEBAS" "$FALLOS"
[[ "$FALLOS" -eq 0 ]]

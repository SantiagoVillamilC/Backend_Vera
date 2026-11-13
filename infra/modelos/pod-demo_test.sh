#!/usr/bin/env bash
# infra/modelos/pod-demo_test.sh
# Prueba de pod-demo.sh con docker, curl y nvidia-smi simulados. No usa el docker real, ni red, ni
# el Ollama nativo ni el puerto 11434: los tres comandos salen de un directorio temporal puesto al
# frente del PATH. Casos de código de salida: sin variables, token corto, sin docker, camino feliz,
# 404 al precalentar, servicio unhealthy, verificar con una sonda en 401, apagar y nvidia-smi roto.
# Uso: bash infra/modelos/pod-demo_test.sh
set -euo pipefail

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="${DIRECTORIO_SCRIPT}/pod-demo.sh"
BASE="$(mktemp -d)"
trap 'rm -rf "${BASE}"' EXIT

BIN="${BASE}/bin"
SOLO_SIN_DOCKER="${BASE}/sin-docker"
export FAKE_DIR="${BASE}/control"
mkdir -p "${BIN}" "${SOLO_SIN_DOCKER}" "${FAKE_DIR}"

TOKEN_A="$(printf 'a%.0s' {1..32})"
TOKEN_B="$(printf 'b%.0s' {1..32})"
MODELO="gemma-4-e4b-it-q4_k_m"

# docker simulado. Estado de cada servicio en FAKE_DIR/estado-<servicio> (por defecto healthy).
cat >"${BIN}/docker" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${FAKE_DIR}/docker.log"
case "$1" in
  inspect)
    id="${@: -1}"
    servicio="${id#id-}"
    if [[ -s "${FAKE_DIR}/estado-${servicio}" ]]; then
      cat "${FAKE_DIR}/estado-${servicio}"
    else
      printf 'healthy\n'
    fi
    ;;
  compose)
    for argumento in "$@"; do
      case "$argumento" in
        version) exit 0 ;;
        ps) printf 'id-%s\n' "${@: -1}"; exit 0 ;;
        up | down) exit 0 ;;
      esac
    done
    ;;
esac
FAKE

# curl simulado. Código por sonda en FAKE_DIR/codigo-<clave>, por defecto 200. Las claves son
# generate, tags, whisper y kokoro. Registra los argumentos para comprobar que no lleven tokens.
cat >"${BIN}/curl" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${FAKE_DIR}/curl.log"
salida="" formato="" url="" lee_stdin=0
while [[ "$#" -gt 0 ]]; do
  case "$1" in
    -o) salida="$2"; shift ;;
    -w) formato="$2"; shift ;;
    -K) lee_stdin=1; shift ;;
    --max-time | -H | -d) shift ;;
    -*) ;;
    *) url="$1" ;;
  esac
  shift
done
if (( lee_stdin == 1 )); then cat >/dev/null; fi
case "${url}" in
  */api/generate) clave=generate ;;
  */api/tags) clave=tags ;;
  */api/ps) clave=ps ;;
  *8767/healthz) clave=whisper ;;
  *8766/healthz) clave=kokoro ;;
  *) clave=otra ;;
esac
codigo=200
if [[ -s "${FAKE_DIR}/codigo-${clave}" ]]; then codigo="$(cat "${FAKE_DIR}/codigo-${clave}")"; fi
if [[ -n "${salida}" && "${salida}" != "/dev/null" ]]; then printf '{}' >"${salida}"; fi
if [[ "${formato}" == *http_code* ]]; then
  printf '%s' "${codigo}"
else
  printf '{"models":[]}'
fi
FAKE

# nvidia-smi simulado. Si existe FAKE_DIR/nvidia-rc sale con ese código y escribe en stderr.
cat >"${BIN}/nvidia-smi" <<'FAKE'
#!/usr/bin/env bash
if [[ -s "${FAKE_DIR}/nvidia-rc" ]]; then
  printf 'NVIDIA-SMI no pudo comunicarse con el driver\n' >&2
  exit "$(cat "${FAKE_DIR}/nvidia-rc")"
fi
printf 'NVIDIA GPU de prueba, 1 MiB, 24000 MiB\n'
FAKE
chmod +x "${BIN}/docker" "${BIN}/curl" "${BIN}/nvidia-smi"

# PATH mínimo sin docker: solo lo que el script usa antes de comprobar docker.
ln -s "$(command -v dirname)" "${SOLO_SIN_DOCKER}/dirname"

FALLAS=0
SALIDA=""

fallar() {
  printf 'FALLA: %s\n' "$1" >&2
  FALLAS=$((FALLAS + 1))
}

reiniciar() {
  find "${FAKE_DIR}" -mindepth 1 -delete
}

# probar <nombre> <rc esperado> <texto esperado en la salida> <comando...>
probar() {
  local nombre="$1" esperado="$2" texto="$3" rc=0
  shift 3
  SALIDA="$("$@" 2>&1)" || rc=$?
  if (( rc != esperado )); then
    fallar "${nombre}: salió con ${rc} y se esperaba ${esperado}"
    printf '%s\n' "${SALIDA}" >&2
    return 0
  fi
  if [[ -n "${texto}" && "${SALIDA}" != *"${texto}"* ]]; then
    fallar "${nombre}: la salida no contiene \"${texto}\""
    printf '%s\n' "${SALIDA}" >&2
    return 0
  fi
  printf 'ok: %s (rc %s)\n' "${nombre}" "${rc}"
}

ejecutar() {
  env PATH="${BIN}:${PATH}" "$@"
}

# 1. Sin variables
reiniciar
probar "sin variables" 1 "faltan variables" \
  env -u WHISPER_TOKEN -u KOKORO_TOKEN -u OLLAMA_MODELO PATH="${BIN}:${PATH}" "${BASH}" "${SCRIPT}" encender
[[ ! -e "${FAKE_DIR}/docker.log" ]] || fallar "sin variables: llamó a docker"

# 2. Token corto
reiniciar
probar "token corto" 1 "menos de 32 caracteres" \
  ejecutar WHISPER_TOKEN="corto" KOKORO_TOKEN="${TOKEN_B}" OLLAMA_MODELO="${MODELO}" "${BASH}" "${SCRIPT}" encender

# 3. Sin docker en el PATH
reiniciar
probar "sin docker" 1 "no hay docker en el PATH" \
  env PATH="${SOLO_SIN_DOCKER}" WHISPER_TOKEN="${TOKEN_A}" KOKORO_TOKEN="${TOKEN_B}" \
  OLLAMA_MODELO="${MODELO}" "${BASH}" "${SCRIPT}" encender

# 4. Camino feliz
reiniciar
probar "camino feliz" 0 "pila lista" \
  ejecutar WHISPER_TOKEN="${TOKEN_A}" KOKORO_TOKEN="${TOKEN_B}" OLLAMA_MODELO="${MODELO}" "${BASH}" "${SCRIPT}" encender
[[ "$(<"${FAKE_DIR}/docker.log")" == *"up -d --build"* ]] || fallar "camino feliz: no ejecutó up -d --build"
[[ "$(<"${FAKE_DIR}/curl.log")" != *"${TOKEN_A}"* && "$(<"${FAKE_DIR}/curl.log")" != *"${TOKEN_B}"* ]] \
  || fallar "camino feliz: un token apareció en los argumentos de curl"

# 5. 404 al precalentar: Ollama no tiene registrado el modelo
reiniciar
printf '404' >"${FAKE_DIR}/codigo-generate"
probar "precalentamiento 404" 1 "no tiene registrado el modelo" \
  ejecutar WHISPER_TOKEN="${TOKEN_A}" KOKORO_TOKEN="${TOKEN_B}" OLLAMA_MODELO="${MODELO}" "${BASH}" "${SCRIPT}" encender

# 6. Servicio unhealthy
reiniciar
printf 'unhealthy' >"${FAKE_DIR}/estado-vera-voz"
probar "servicio unhealthy" 1 "vera-voz quedó unhealthy" \
  ejecutar WHISPER_TOKEN="${TOKEN_A}" KOKORO_TOKEN="${TOKEN_B}" OLLAMA_MODELO="${MODELO}" "${BASH}" "${SCRIPT}" encender

# 7. Verificar con una sonda que falla
reiniciar
printf '401' >"${FAKE_DIR}/codigo-whisper"
probar "verificar con whisper 401" 1 "whisper /healthz: 401 (FALLA)" \
  ejecutar WHISPER_TOKEN="${TOKEN_A}" KOKORO_TOKEN="${TOKEN_B}" OLLAMA_MODELO="${MODELO}" "${BASH}" "${SCRIPT}" verificar

# 8. Apagar: sin variables, conserva el volumen (sin -v)
reiniciar
probar "apagar" 0 "volumen conservado" \
  env -u WHISPER_TOKEN -u KOKORO_TOKEN -u OLLAMA_MODELO PATH="${BIN}:${PATH}" "${BASH}" "${SCRIPT}" apagar
[[ "$(<"${FAKE_DIR}/docker.log")" == *" down"* ]] || fallar "apagar: no ejecutó down"
[[ "$(<"${FAKE_DIR}/docker.log")" != *" -v"* ]] || fallar "apagar: pasó -v y borraría el volumen"

# 9. nvidia-smi roto (sale con 9): avisa y no mata el script
reiniciar
printf '9' >"${FAKE_DIR}/nvidia-rc"
probar "nvidia-smi roto" 0 "nvidia-smi falló con código 9" \
  ejecutar WHISPER_TOKEN="${TOKEN_A}" KOKORO_TOKEN="${TOKEN_B}" OLLAMA_MODELO="${MODELO}" "${BASH}" "${SCRIPT}" verificar
[[ "${SALIDA}" == *"pila lista"* ]] || fallar "nvidia-smi roto: el script no llegó a pila lista"

if (( FALLAS > 0 )); then
  printf '\n%s casos fallaron\n' "${FALLAS}" >&2
  exit 1
fi
printf '\ntodos los casos pasaron\n'

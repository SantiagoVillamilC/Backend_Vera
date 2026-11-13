#!/usr/bin/env bash
# infra/modelos/pod-demo.sh
# Operación de la pila de modelos DENTRO de un pod de GPU que ya existe: levanta el compose con el
# override CUDA, espera los healthchecks de los tres servicios, precalienta el razonador y muestra
# la salud. No llama a la API de RunPod ni usa su CLI: crear y terminar el pod es manual, en la
# consola del proveedor (ver la sección "Pod de GPU" de docs/operacion-demo.md).
#
# Uso:
#   WHISPER_TOKEN=... KOKORO_TOKEN=... OLLAMA_MODELO=gemma-4-e4b-it-q4_k_m \
#     bash infra/modelos/pod-demo.sh encender
#   bash infra/modelos/pod-demo.sh verificar     (mismas tres variables)
#   bash infra/modelos/pod-demo.sh apagar        (no pide variables)
#
# Variables obligatorias para encender y verificar: WHISPER_TOKEN y KOKORO_TOKEN (mínimo 32
# caracteres, vienen de Infisical o del archivo del operador y nunca se escriben en un archivo) y
# OLLAMA_MODELO. VERA_VOZ_TOKEN es opcional: si está definida, se comprueba que valga lo mismo que
# KOKORO_TOKEN.
# Variable opcional: ESPERA_MAX, segundos máximos para cada espera (por defecto 600). Es holgura sobre
# lo que declara el compose para vera-voz (120 s de gracia más 18 reintentos de 10 s) y no es una
# medición. Con el volumen vacío, la descarga de pesos puede necesitar más.
#
# ESTADO: no se ha ejecutado en un pod. Validado solo con bash -n, shellcheck y la prueba
# infra/modelos/pod-demo_test.sh (docker, curl y nvidia-smi simulados, sin tocar el docker real).
# Queda sin probar que el pod permita correr Docker, que el runtime NVIDIA esté disponible y la
# carga real del razonador en la GPU. No hace build ni up fuera del subcomando encender.
set -euo pipefail

RAIZ="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$RAIZ"

COMPOSE=(docker compose -f infra/modelos/compose.yaml -f infra/modelos/compose.cuda.yaml)
SERVICIOS=(vera-llm vera-voz vera-kokoro)
OLLAMA_URL="http://127.0.0.1:11434"
WHISPER_URL="http://127.0.0.1:8767"
KOKORO_URL="http://127.0.0.1:8766"
ESPERA_MAX="${ESPERA_MAX:-600}"
SONDEO=5
RESPUESTA=""

paso() { printf '\n==> %s\n' "$1"; }
info() { printf '    %s\n' "$1"; }
aviso() { printf '[aviso] %s\n' "$1" >&2; }
error() { printf '[error] %s\n' "$1" >&2; exit 1; }

limpiar() {
  if [[ -n "$RESPUESTA" ]]; then
    rm -f "$RESPUESTA"
  fi
}
trap limpiar EXIT

ayuda() {
  cat <<'AYUDA'
Uso: bash infra/modelos/pod-demo.sh <encender|verificar|apagar>

  encender   levanta el compose con el override CUDA, espera los tres healthchecks,
             precalienta el razonador y muestra la salud
  verificar  muestra la salud de los tres servicios y falla si alguno no responde
  apagar     baja los contenedores y conserva el volumen. Terminar el pod es manual

Variables para encender y verificar: WHISPER_TOKEN, KOKORO_TOKEN y OLLAMA_MODELO.
AYUDA
}

exigir_herramientas() {
  command -v docker >/dev/null 2>&1 || error "no hay docker en el PATH: este script corre dentro del pod y necesita Docker"
  docker compose version >/dev/null 2>&1 || error "docker compose no está disponible"
}

exigir_curl() {
  command -v curl >/dev/null 2>&1 || error "no hay curl en el PATH"
}

exigir_entorno() {
  local faltan=() variable
  for variable in WHISPER_TOKEN KOKORO_TOKEN OLLAMA_MODELO; do
    if [[ -z "${!variable:-}" ]]; then
      faltan+=("$variable")
    fi
  done
  if (( ${#faltan[@]} > 0 )); then
    error "faltan variables: ${faltan[*]}. Exporta WHISPER_TOKEN y KOKORO_TOKEN desde Infisical y fija OLLAMA_MODELO (gemma-4-e4b-it-q4_k_m en el pod)"
  fi
  if (( ${#WHISPER_TOKEN} < 32 )); then
    error "WHISPER_TOKEN tiene menos de 32 caracteres"
  fi
  if (( ${#KOKORO_TOKEN} < 32 )); then
    error "KOKORO_TOKEN tiene menos de 32 caracteres"
  fi
  if [[ ! "$OLLAMA_MODELO" =~ ^[A-Za-z0-9._:/-]+$ ]]; then
    error "OLLAMA_MODELO tiene caracteres no válidos para un nombre de modelo"
  fi
  if [[ -n "${VERA_VOZ_TOKEN:-}" && "$VERA_VOZ_TOKEN" != "$KOKORO_TOKEN" ]]; then
    aviso "VERA_VOZ_TOKEN no vale lo mismo que KOKORO_TOKEN: la API recibiría 401 de Kokoro y el healthcheck no lo detecta"
  fi
}

estado_servicio() {
  local contenedor
  contenedor="$("${COMPOSE[@]}" ps -q "$1" 2>/dev/null || true)"
  if [[ -z "$contenedor" ]]; then
    printf 'ausente'
    return 0
  fi
  docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$contenedor" 2>/dev/null || printf 'desconocido'
}

# Espera a que un servicio quede healthy. Falla en cuanto Docker lo marca unhealthy, porque el
# healthcheck ya agotó sus reintentos, o al vencer ESPERA_MAX.
esperar_sano() {
  local servicio="$1" estado="" previo="" limite=$((SECONDS + ESPERA_MAX))
  while :; do
    estado="$(estado_servicio "$servicio")"
    if [[ "$estado" != "$previo" ]]; then
      info "$servicio: $estado"
      previo="$estado"
    fi
    case "$estado" in
      healthy) return 0 ;;
      unhealthy | exited | dead)
        error "$servicio quedó $estado. Revisa: docker compose -f infra/modelos/compose.yaml -f infra/modelos/compose.cuda.yaml logs --tail 40 $servicio" ;;
    esac
    if (( SECONDS >= limite )); then
      error "$servicio no llegó a healthy en ${ESPERA_MAX} s (último estado: $estado)"
    fi
    sleep "$SONDEO"
  done
}

esperar_servicios() {
  local servicio
  for servicio in "${SERVICIOS[@]}"; do
    esperar_sano "$servicio"
  done
}

# Una petición mínima fuerza la carga del GGUF en la GPU, que Ollama hace en el primer
# /api/generate y no al arrancar. Basta con un 200: con thinking activo la respuesta puede venir
# vacía por num_predict y aun así el modelo queda cargado.
precalentar() {
  local codigo inicio="$SECONDS" cuerpo
  RESPUESTA="$(mktemp)"
  cuerpo="$(printf '{"model":"%s","prompt":"Responde solo: listo","stream":false,"options":{"num_predict":8}}' "$OLLAMA_MODELO")"
  codigo="$(curl -sS -o "$RESPUESTA" -w '%{http_code}' --max-time "$ESPERA_MAX" \
    -H 'Content-Type: application/json' -d "$cuerpo" "$OLLAMA_URL/api/generate" || true)"
  case "$codigo" in
    200) info "precalentado $OLLAMA_MODELO en $((SECONDS - inicio)) s" ;;
    404) error "Ollama no tiene registrado el modelo $OLLAMA_MODELO. El compose no lo crea desde el GGUF: regístralo en el volumen antes (ollama create) y vuelve a encender" ;;
    *) error "el precalentamiento falló (HTTP ${codigo:-sin respuesta}) para $OLLAMA_MODELO" ;;
  esac
}

# GET autenticado sin dejar el token en la lista de procesos: la cabecera entra por stdin.
sondear_con_token() {
  local token="$1" url="$2"
  printf 'header = "X-Vera-Token: %s"\n' "$token" \
    | curl -sS -K - -o /dev/null -w '%{http_code}' --max-time 5 "$url" 2>/dev/null || true
}

# Informativa: si nvidia-smi falla (por ejemplo el driver no responde) se avisa con su código de
# salida y el script sigue, porque con set -e y pipefail un fallo aquí mataría el script sin mensaje.
mostrar_gpu() {
  local salida linea codigo=0
  if ! command -v nvidia-smi >/dev/null 2>&1; then
    aviso "nvidia-smi no está en el PATH: no se puede mostrar la GPU"
    return 0
  fi
  salida="$(nvidia-smi --query-gpu=name,memory.used,memory.total --format=csv,noheader 2>&1)" || codigo=$?
  if (( codigo != 0 )); then
    aviso "nvidia-smi falló con código $codigo: no se puede mostrar la GPU (${salida:-sin salida})"
    return 0
  fi
  while IFS= read -r linea; do
    info "GPU: $linea"
  done <<<"$salida"
}

verificar() {
  local fallas=0 servicio estado codigo
  paso "Salud de la pila"
  for servicio in "${SERVICIOS[@]}"; do
    estado="$(estado_servicio "$servicio")"
    if [[ "$estado" == "healthy" ]]; then
      info "$servicio: healthy"
    else
      info "$servicio: $estado (FALLA)"
      fallas=$((fallas + 1))
    fi
  done

  codigo="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 "$OLLAMA_URL/api/tags" 2>/dev/null || true)"
  if [[ "$codigo" == "200" ]]; then
    info "ollama /api/tags: 200"
  else
    info "ollama /api/tags: ${codigo:-sin respuesta} (FALLA)"
    fallas=$((fallas + 1))
  fi

  codigo="$(sondear_con_token "$WHISPER_TOKEN" "$WHISPER_URL/healthz")"
  if [[ "$codigo" == "200" ]]; then
    info "whisper /healthz: 200"
  else
    info "whisper /healthz: ${codigo:-sin respuesta} (FALLA)"
    fallas=$((fallas + 1))
  fi

  codigo="$(sondear_con_token "$KOKORO_TOKEN" "$KOKORO_URL/healthz")"
  if [[ "$codigo" == "200" ]]; then
    info "kokoro /healthz: 200"
  else
    info "kokoro /healthz: ${codigo:-sin respuesta} (FALLA)"
    fallas=$((fallas + 1))
  fi

  info "modelos cargados en Ollama (/api/ps):"
  curl -sS --max-time 5 "$OLLAMA_URL/api/ps" 2>/dev/null | head -c 600 | sed 's/^/        /' || true
  printf '\n'
  mostrar_gpu

  if (( fallas > 0 )); then
    error "$fallas comprobaciones fallaron: no abras la sesión"
  fi
  info "pila lista"
}

encender() {
  exigir_entorno
  exigir_herramientas
  exigir_curl
  mostrar_gpu
  paso "Levantando el compose con el override CUDA"
  "${COMPOSE[@]}" up -d --build
  paso "Esperando los tres healthchecks, en orden"
  esperar_servicios
  paso "Precalentando el razonador"
  precalentar
  verificar
}

apagar() {
  exigir_herramientas
  paso "Bajando los contenedores"
  # Compose interpola las variables obligatorias aun para bajar, así que se pasan valores
  # de relleno que no llegan a ningún contenedor. El volumen pesos no se borra (sin -v).
  WHISPER_TOKEN="${WHISPER_TOKEN:-apagar}" KOKORO_TOKEN="${KOKORO_TOKEN:-apagar}" "${COMPOSE[@]}" down
  info "contenedores abajo, volumen conservado"
  aviso "el gasto sigue hasta que termines el pod (Terminate, no Stop) en la consola del proveedor"
}

main() {
  case "${1:-}" in
    encender) encender ;;
    verificar)
      exigir_entorno
      exigir_herramientas
      exigir_curl
      verificar ;;
    apagar) apagar ;;
    ayuda | -h | --help) ayuda ;;
    *) ayuda >&2
       exit 2 ;;
  esac
}

main "$@"

#!/usr/bin/env bash
# infra/modelos-runpod/pod/salud-modelos.sh
# Comprueba, DENTRO del pod, que Ollama, Whisper y Kokoro responden con sus tokens y que el modelo
# gemma4-e4b-local está registrado. Sale con 0 solo si los tres responden. No imprime tokens.
#
# Uso:
#   bash salud-modelos.sh            texto legible, una línea por servicio
#   bash salud-modelos.sh --json     una línea JSON, para que modelos-control la lea por ssh
#
# Códigos de salida: 0 todo sano, 1 algún servicio no responde, 2 uso o tokens inválidos.
#
# Variables opcionales (mismos valores por defecto que arrancar-modelos.sh):
#   VERA_TOKENS_ARCHIVO   archivo 0600 con WHISPER_TOKEN y VERA_LAB_TOKEN
#   VERA_PUERTO_OLLAMA, VERA_PUERTO_WHISPER, VERA_PUERTO_KOKORO   (11434, 8767, 8766)
#   VERA_ESTADO_DIR       carpeta de estado (por defecto /workspace/vera-estado, o /var/lib/vera-estado si existe ahí tokens.env)
#
# No hay un /readyz unificado en el pod a propósito: exigiría un cuarto proceso y un cuarto puerto en el
# túnel. modelos-control ya sondea los tres puertos por el túnel y puede ejecutar este script por ssh.
set -euo pipefail

# Con caché en volumen, arrancar-modelos.sh deja el estado (y los tokens) fuera del volumen, en /var/lib/vera-estado.
if [[ -n "${VERA_ESTADO_DIR:-}" ]]; then
  ESTADO_DIR="$VERA_ESTADO_DIR"
elif [[ -f /var/lib/vera-estado/tokens.env ]]; then
  ESTADO_DIR="/var/lib/vera-estado"
else
  ESTADO_DIR="/workspace/vera-estado"
fi
TOKENS_ARCHIVO="${VERA_TOKENS_ARCHIVO:-${ESTADO_DIR}/tokens.env}"
PUERTO_OLLAMA="${VERA_PUERTO_OLLAMA:-11434}"
PUERTO_WHISPER="${VERA_PUERTO_WHISPER:-8767}"
PUERTO_KOKORO="${VERA_PUERTO_KOKORO:-8766}"
MODELO_OLLAMA="gemma4-e4b-local"
FORMATO="texto"
WHISPER_TOKEN=""
VERA_LAB_TOKEN=""

error() { printf '[error] %s\n' "$1" >&2; exit 2; }

# La cabecera viaja por la configuración de curl en stdin (-K -), no por argv: no aparece en ps.
curl_con_token() {
  local token="$1"
  shift
  curl -K - "$@" <<< "header = \"X-Vera-Token: ${token}\""
}

cargar_tokens() {
  local linea clave valor modo
  [[ -f "$TOKENS_ARCHIVO" ]] || error "falta ${TOKENS_ARCHIVO}"
  modo="$(stat -c %a "$TOKENS_ARCHIVO")"
  [[ "$modo" == "600" ]] || error "${TOKENS_ARCHIVO} debe tener modo 600 y tiene ${modo}"
  while IFS= read -r linea || [[ -n "$linea" ]]; do
    [[ -z "$linea" || "$linea" == \#* ]] && continue
    clave="${linea%%=*}"
    valor="${linea#*=}"
    case "$clave" in
      WHISPER_TOKEN) WHISPER_TOKEN="$valor" ;;
      VERA_LAB_TOKEN) VERA_LAB_TOKEN="$valor" ;;
      *) error "clave no permitida en el archivo de tokens" ;;
    esac
  done < "$TOKENS_ARCHIVO"
  [[ "$WHISPER_TOKEN" =~ ^[A-Za-z0-9_-]{32,}$ ]] || error "WHISPER_TOKEN ausente, corto o con caracteres no permitidos"
  [[ "$VERA_LAB_TOKEN" =~ ^[A-Za-z0-9_-]{32,}$ ]] || error "VERA_LAB_TOKEN ausente, corto o con caracteres no permitidos"
}

sonda_ollama() {
  curl -fsS -m 5 "http://127.0.0.1:${PUERTO_OLLAMA}/api/version" >/dev/null 2>&1
}

sonda_modelo() {
  local etiquetas
  etiquetas="$(curl -fsS -m 5 "http://127.0.0.1:${PUERTO_OLLAMA}/api/tags" 2>/dev/null || true)"
  [[ "$etiquetas" == *"\"${MODELO_OLLAMA}"* ]]
}

sonda_whisper() {
  curl_con_token "$WHISPER_TOKEN" -fsS -m 5 "http://127.0.0.1:${PUERTO_WHISPER}/healthz" >/dev/null 2>&1
}

sonda_kokoro() {
  curl_con_token "$VERA_LAB_TOKEN" -fsS -m 5 "http://127.0.0.1:${PUERTO_KOKORO}/healthz" >/dev/null 2>&1
}

# resultado NOMBRE SONDA: imprime "ok" o "fallo" y no falla nunca (set -e no debe cortar el informe).
resultado() {
  if "$2"; then printf 'ok'; else printf 'fallo'; fi
}

principal() {
  local ollama modelo whisper kokoro rc=0
  case "${1:-}" in
    --json) FORMATO="json" ;;
    "") ;;
    *) error "uso: salud-modelos.sh [--json]" ;;
  esac
  cargar_tokens
  ollama="$(resultado ollama sonda_ollama)"
  modelo="$(resultado modelo sonda_modelo)"
  whisper="$(resultado whisper sonda_whisper)"
  kokoro="$(resultado kokoro sonda_kokoro)"
  [[ "$ollama" == ok && "$modelo" == ok && "$whisper" == ok && "$kokoro" == ok ]] || rc=1
  if [[ "$FORMATO" == "json" ]]; then
    printf '{"ollama": "%s", "modelo": "%s", "whisper": "%s", "kokoro": "%s", "sano": %s}\n' \
      "$ollama" "$modelo" "$whisper" "$kokoro" "$([[ $rc -eq 0 ]] && printf true || printf false)"
  else
    printf 'ollama  :%s %s\nmodelo  %s %s\nwhisper :%s %s\nkokoro  :%s %s\n' \
      "$PUERTO_OLLAMA" "$ollama" "$MODELO_OLLAMA" "$modelo" "$PUERTO_WHISPER" "$whisper" "$PUERTO_KOKORO" "$kokoro"
  fi
  return "$rc"
}

principal "$@"

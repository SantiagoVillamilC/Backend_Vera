#!/usr/bin/env bash
# infra/modelos-runpod/vm/generar-secretos_test.sh
# Prueba de generar-secretos.sh fuera de la VM (--sin-usuario, rutas temporales). Cubre: --seco no escribe
# nada, los modos 0600 de los cuatro archivos, que ningún secreto aparezca en la salida, que la clave de RunPod
# se lea de stdin y solo viva en el archivo de root, que los tokens de la API coincidan con los de modelos-control, la idempotencia, --rotar y el rechazo
# de claves inválidas. No cubre useradd ni chown (necesitan root real).
# Uso: bash infra/modelos-runpod/vm/generar-secretos_test.sh
set -euo pipefail

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="${DIRECTORIO_SCRIPT}/generar-secretos.sh"
BASE="$(mktemp -d)"
trap 'rm -rf "${BASE}"' EXIT
FALLOS=0
PRUEBAS=0
CLAVE_FALSA="rpa_CLAVE_DE_PRUEBA_0123456789abcdef"

verificar() {
  PRUEBAS=$((PRUEBAS + 1))
  if [[ "$2" == "$3" ]]; then printf 'ok    %s\n' "$1"; else printf 'FALLA %s (esperado %s, real %s)\n' "$1" "$2" "$3"; FALLOS=$((FALLOS + 1)); fi
}

contiene() {
  PRUEBAS=$((PRUEBAS + 1))
  if [[ "$(< "$2")" == *"$3"* ]]; then printf 'ok    %s\n' "$1"; else printf 'FALLA %s (no aparece "%s")\n' "$1" "$3"; FALLOS=$((FALLOS + 1)); fi
}

no_contiene() {
  PRUEBAS=$((PRUEBAS + 1))
  if [[ "$(< "$2")" != *"$3"* ]]; then printf 'ok    %s\n' "$1"; else printf 'FALLA %s (aparece un secreto)\n' "$1"; FALLOS=$((FALLOS + 1)); fi
}

valor() { # valor ARCHIVO CLAVE
  local linea
  while IFS= read -r linea; do
    [[ "$linea" == "$2="* ]] && { printf '%s' "${linea#*=}"; return 0; }
  done < "$1"
  return 1
}

correr() { # correr ENTRADA ARGS..., la salida queda en ${BASE}/salida.txt
  local entrada="$1" rc=0
  shift
  printf '%s\n' "$entrada" | VERA_MODELOS_ETC="${BASE}/etc" ARCHIVO_TOKENS_API="${BASE}/api/modelos.env" \
    bash "$SCRIPT" --sin-usuario "$@" > "${BASE}/salida.txt" 2>&1 || rc=$?
  return "$rc"
}

# --seco no escribe nada
rc=0
correr "" --seco || rc=$?
verificar "--seco sale con 0" 0 "$rc"
[[ ! -e "${BASE}/etc/modelos-control.env" ]] && nada=0 || nada=1
verificar "--seco no crea el archivo de entorno" 0 "$nada"

# creación
rc=0
correr "$CLAVE_FALSA" --codigo-sha 0123456789abcdef0123456789abcdef01234567 || rc=$?
verificar "la creación sale con 0" 0 "$rc"
verificar "modelos-control.env es 0600" 600 "$(stat -c %a "${BASE}/etc/modelos-control.env")"
verificar "tokens.env es 0600" 600 "$(stat -c %a "${BASE}/etc/tokens.env")"
verificar "control.token es 0600" 600 "$(stat -c %a "${BASE}/etc/control.token")"
verificar "modelos.env de la API es 0600" 600 "$(stat -c %a "${BASE}/api/modelos.env")"
verificar "la llave privada es 0600" 600 "$(stat -c %a "${BASE}/etc/ssh/tunel_ed25519")"
no_contiene "la salida no imprime la clave de RunPod" "${BASE}/salida.txt" "$CLAVE_FALSA"
TOKEN_VOZ="$(valor "${BASE}/etc/tokens.env" VERA_VOZ_TOKEN)"
TOKEN_STT="$(valor "${BASE}/etc/tokens.env" VERA_STT_TOKEN)"
TOKEN_CONTROL="$(< "${BASE}/etc/control.token")"
no_contiene "la salida no imprime el token de voz" "${BASE}/salida.txt" "$TOKEN_VOZ"
no_contiene "la salida no imprime el token de control" "${BASE}/salida.txt" "$TOKEN_CONTROL"
verificar "el token de voz tiene 64 caracteres" 64 "${#TOKEN_VOZ}"
verificar "el token de control tiene 64 caracteres" 64 "${#TOKEN_CONTROL}"
verificar "voz y STT son distintos" 1 "$([[ "$TOKEN_VOZ" != "$TOKEN_STT" ]] && printf 1 || printf 0)"
verificar "la clave de RunPod quedó en el archivo de root" "$CLAVE_FALSA" "$(valor "${BASE}/etc/modelos-control.env" RUNPOD_API_KEY)"
no_contiene "la clave de RunPod no está en el archivo de tokens" "${BASE}/etc/tokens.env" "$CLAVE_FALSA"
no_contiene "la clave de RunPod no está en el archivo de la API" "${BASE}/api/modelos.env" "$CLAVE_FALSA"
verificar "MODELOS_REPO_SHA quedó fijado" 0123456789abcdef0123456789abcdef01234567 "$(valor "${BASE}/etc/modelos-control.env" MODELOS_REPO_SHA)"
verificar "el token de voz de la API coincide" "$TOKEN_VOZ" "$(valor "${BASE}/api/modelos.env" VERA_VOZ_TOKEN)"
verificar "el token de STT de la API coincide" "$TOKEN_STT" "$(valor "${BASE}/api/modelos.env" VERA_STT_TOKEN)"
contiene "se conservan los comentarios de la plantilla" "${BASE}/etc/modelos-control.env" "# ---------- Estado ----------"
verificar "los valores no secretos se conservan" "0.35" "$(valor "${BASE}/etc/modelos-control.env" MODELOS_TARIFA_TOPE_USD_H)"

# idempotencia: no regenera nada y no vuelve a pedir la clave
HUELLA_ANTES="$(sha256sum "${BASE}/etc/modelos-control.env" "${BASE}/etc/tokens.env" "${BASE}/etc/control.token")"
rc=0
correr "" || rc=$?
verificar "la segunda corrida sale con 0" 0 "$rc"
verificar "la segunda corrida no cambia los archivos" "$HUELLA_ANTES" "$(sha256sum "${BASE}/etc/modelos-control.env" "${BASE}/etc/tokens.env" "${BASE}/etc/control.token")"

# rotar: tokens nuevos, clave de RunPod intacta, API consistente
rc=0
correr "" --rotar || rc=$?
verificar "--rotar sale con 0" 0 "$rc"
verificar "el token de voz cambió" 1 "$([[ "$(valor "${BASE}/etc/tokens.env" VERA_VOZ_TOKEN)" != "$TOKEN_VOZ" ]] && printf 1 || printf 0)"
verificar "el token de control cambió" 1 "$([[ "$(< "${BASE}/etc/control.token")" != "$TOKEN_CONTROL" ]] && printf 1 || printf 0)"
verificar "la clave de RunPod se conserva al rotar" "$CLAVE_FALSA" "$(valor "${BASE}/etc/modelos-control.env" RUNPOD_API_KEY)"
verificar "tras rotar la API sigue coincidiendo" "$(valor "${BASE}/etc/tokens.env" VERA_VOZ_TOKEN)" "$(valor "${BASE}/api/modelos.env" VERA_VOZ_TOKEN)"
verificar "tras rotar el SHA se conserva" 0123456789abcdef0123456789abcdef01234567 "$(valor "${BASE}/etc/modelos-control.env" MODELOS_REPO_SHA)"

# clave inválida en una instalación nueva
rc=0
VERA_MODELOS_ETC="${BASE}/etc2" ARCHIVO_TOKENS_API="${BASE}/api2/modelos.env" \
  bash "$SCRIPT" --sin-usuario > "${BASE}/salida.txt" 2>&1 <<< 'clave con espacios' || rc=$?
verificar "una clave con espacios se rechaza (65)" 65 "$rc"

# argumentos inválidos
rc=0
correr "" --codigo-sha corto || rc=$?
verificar "--codigo-sha inválido se rechaza (64)" 64 "$rc"

printf '\n%s pruebas, %s fallas\n' "$PRUEBAS" "$FALLOS"
[[ "$FALLOS" -eq 0 ]]

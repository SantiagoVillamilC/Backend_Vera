#!/usr/bin/env bash
# infra/modelos-runpod/vm/red-api_test.sh
# Prueba de red-api.sh con un docker simulado y directorios temporales. No usa el docker real ni toca /opt
# ni /etc. Cubre: lectura del gateway, rechazo de gateways no privados, instalar --seco (no escribe nada),
# instalar (gateway en el entorno y en el override, clave de RunPod intacta y fuera del override, modo 0600),
# idempotencia, verificar (coincide, el gateway cambió, SHA pendiente, sin instalación), aplicar y desinstalar.
# No cubre el comportamiento real de docker compose ni de modelos-control: eso se prueba con docker local.
# Uso: bash infra/modelos-runpod/vm/red-api_test.sh
set -euo pipefail

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="${DIRECTORIO_SCRIPT}/red-api.sh"
BASE="$(mktemp -d)"
trap 'rm -rf "${BASE}"' EXIT
FALLOS=0
PRUEBAS=0
CLAVE_FALSA="rpa_NO_ES_UN_SECRETO_REAL"
SHA_REAL="0123456789abcdef0123456789abcdef01234567"

verificar() {
  PRUEBAS=$((PRUEBAS + 1))
  if [[ "$2" == "$3" ]]; then printf 'ok    %s\n' "$1"; else printf 'FALLA %s (esperado %s, real %s)\n' "$1" "$2" "$3"; FALLOS=$((FALLOS + 1)); fi
}

contiene() { # contiene DESCRIPCION ARCHIVO TEXTO
  PRUEBAS=$((PRUEBAS + 1))
  if [[ "$(< "$2")" == *"$3"* ]]; then printf 'ok    %s\n' "$1"; else printf 'FALLA %s (no aparece "%s")\n' "$1" "$3"; FALLOS=$((FALLOS + 1)); fi
}

no_contiene() {
  PRUEBAS=$((PRUEBAS + 1))
  if [[ "$(< "$2")" != *"$3"* ]]; then printf 'ok    %s\n' "$1"; else printf 'FALLA %s (aparece "%s")\n' "$1" "$3"; FALLOS=$((FALLOS + 1)); fi
}

export FAKE_DIR="${BASE}/fake"
mkdir -p "$FAKE_DIR" "${BASE}/compose" "${BASE}/api"
cat > "${BASE}/docker" <<'FAKE'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${FAKE_DIR}/docker.log"
if [[ "$1" == "network" ]]; then
  cat "${FAKE_DIR}/gateway"
  exit 0
fi
exit 0
FAKE
chmod +x "${BASE}/docker"
printf '172.25.0.1\n' > "${FAKE_DIR}/gateway"
printf 'compose.yaml\n' > "${BASE}/compose/compose.yaml"
printf 'compose.api.yaml\n' > "${BASE}/compose/compose.api.yaml"
printf 'VERA_STT_TOKEN=x\n' > "${BASE}/api/modelos.env"

nuevo_entorno() { # nuevo_entorno SHA: copia del .env.example con la clave falsa y el SHA indicado
  local linea salida=""
  while IFS= read -r linea || [[ -n "$linea" ]]; do
    case "${linea%%=*}" in
      RUNPOD_API_KEY) salida+="RUNPOD_API_KEY=${CLAVE_FALSA}"$'\n' ;;
      MODELOS_REPO_SHA) salida+="MODELOS_REPO_SHA=$1"$'\n' ;;
      *) salida+="${linea}"$'\n' ;;
    esac
  done < "${DIRECTORIO_SCRIPT}/../.env.example"
  ( umask 077 && printf '%s' "$salida" > "${BASE}/entorno.env" )
}

correr() { # correr ARGS... (guarda la salida en ${BASE}/salida.txt)
  local rc=0
  DOCKER="${BASE}/docker" VERA_MODELOS_ENV="${BASE}/entorno.env" DIR_COMPOSE="${BASE}/compose" \
    ARCHIVO_TOKENS_API="${BASE}/api/modelos.env" VERA_MODELOS_LOCK="${BASE}/lock" \
    bash "$SCRIPT" "$@" > "${BASE}/salida.txt" 2>&1 || rc=$?
  return "$rc"
}

nuevo_entorno "$SHA_REAL"
rc=0
correr gateway || rc=$?
verificar "gateway sale con 0" 0 "$rc"
verificar "gateway imprime la IP de la red" "172.25.0.1" "$(< "${BASE}/salida.txt")"

# Gateways que no se aceptan
for malo in "8.8.8.8" "127.0.0.1" "172.32.0.1" "no-es-ip" "" "300.1.1.1"; do
  printf '%s\n' "$malo" > "${FAKE_DIR}/gateway"
  rc=0
  correr instalar || rc=$?
  verificar "gateway '${malo}' se rechaza (69)" 69 "$rc"
done
printf '10.20.0.1\n' > "${FAKE_DIR}/gateway"
rc=0
correr gateway || rc=$?
verificar "10.0.0.0/8 se acepta" 0 "$rc"
printf '192.168.1.1\n' > "${FAKE_DIR}/gateway"
rc=0
correr gateway || rc=$?
verificar "192.168.0.0/16 se acepta" 0 "$rc"
printf '172.25.0.1\n' > "${FAKE_DIR}/gateway"

# instalar --seco: imprime y no escribe nada
HUELLA_ENTORNO="$(sha256sum "${BASE}/entorno.env")"
rc=0
correr instalar --seco || rc=$?
verificar "instalar --seco sale con 0" 0 "$rc"
contiene "el plan muestra el gateway" "${BASE}/salida.txt" "MODELOS_ESCUCHA_VOZ=172.25.0.1:8766"
contiene "el plan muestra la URL de Ollama" "${BASE}/salida.txt" 'VERA_OLLAMA_URL: "http://172.25.0.1:11434"'
no_contiene "el plan no imprime la clave de RunPod" "${BASE}/salida.txt" "$CLAVE_FALSA"
verificar "instalar --seco no cambia el entorno" "$HUELLA_ENTORNO" "$(sha256sum "${BASE}/entorno.env")"
[[ ! -e "${BASE}/compose/compose.z-modelos.yaml" ]] && ninguno=0 || ninguno=1
verificar "instalar --seco no escribe el override" 0 "$ninguno"

# verificar antes de instalar
rc=0
correr verificar || rc=$?
verificar "sin instalación verificar sale con 2" 2 "$rc"

# instalar
rc=0
correr instalar || rc=$?
verificar "instalar sale con 0" 0 "$rc"
OV="${BASE}/compose/compose.z-modelos.yaml"
contiene "el override fija VERA_PROVEEDOR=local" "$OV" 'VERA_PROVEEDOR: "local"'
contiene "el override autoriza solo el gateway" "$OV" 'VERA_MODELOS_HOST_PERMITIDO: "172.25.0.1"'
contiene "el override apunta voz al gateway" "$OV" 'VERA_VOZ_URL: "http://172.25.0.1:8766"'
contiene "el override apunta STT al gateway" "$OV" 'VERA_STT_URL: "http://172.25.0.1:8767"'
contiene "el override apunta Ollama al gateway" "$OV" 'VERA_OLLAMA_URL: "http://172.25.0.1:11434"'
contiene "el override usa el archivo de tokens de la API" "$OV" "- ${BASE}/api/modelos.env"
no_contiene "el override no trae la clave de RunPod" "$OV" "rpa_"
no_contiene "el override no trae marcadores sin sustituir" "$OV" "GATEWAY_DE_DOCKER"
no_contiene "el override no trae marcadores @...@" "$OV" "@PUERTO"
no_contiene "el override no define puentes ni red de host" "$OV" "network_mode"
contiene "el entorno escucha Ollama en el gateway" "${BASE}/entorno.env" "MODELOS_ESCUCHA_OLLAMA=172.25.0.1:11434"
contiene "el entorno escucha voz en el gateway" "${BASE}/entorno.env" "MODELOS_ESCUCHA_VOZ=172.25.0.1:8766"
contiene "el entorno escucha STT en el gateway" "${BASE}/entorno.env" "MODELOS_ESCUCHA_STT=172.25.0.1:8767"
no_contiene "el entorno ya no tiene el marcador como valor" "${BASE}/entorno.env" "=<GATEWAY_DE_DOCKER>"
contiene "la clave de RunPod se conserva en el entorno" "${BASE}/entorno.env" "RUNPOD_API_KEY=${CLAVE_FALSA}"
contiene "el control sigue en loopback" "${BASE}/entorno.env" "MODELOS_PUERTO_CONTROL=127.0.0.1:8790"
verificar "el entorno queda 0600" 600 "$(stat -c %a "${BASE}/entorno.env")"
no_contiene "la salida no imprime la clave de RunPod" "${BASE}/salida.txt" "$CLAVE_FALSA"

# idempotencia
HUELLA="$(sha256sum "$OV" "${BASE}/entorno.env")"
correr instalar
verificar "una segunda instalación no cambia nada" "$HUELLA" "$(sha256sum "$OV" "${BASE}/entorno.env")"

# verificar: coincide, gateway cambió, SHA pendiente
rc=0
correr verificar || rc=$?
verificar "todo coincide: verificar sale con 0" 0 "$rc"
contiene "el mensaje dice OK" "${BASE}/salida.txt" "RESULTADO=OK"
printf '172.30.0.1\n' > "${FAKE_DIR}/gateway"
rc=0
correr verificar || rc=$?
verificar "el gateway cambió: verificar sale con 1" 1 "$rc"
contiene "el mensaje nombra la discrepancia" "${BASE}/salida.txt" "DISCREPANCIA"
printf '172.25.0.1\n' > "${FAKE_DIR}/gateway"
nuevo_entorno "0000000000000000000000000000000000000000"
correr instalar
rc=0
correr verificar || rc=$?
verificar "SHA en ceros: verificar sale con 1" 1 "$rc"
contiene "el mensaje dice PENDIENTE" "${BASE}/salida.txt" "PENDIENTE MODELOS_REPO_SHA"

# aplicar y desinstalar
: > "${FAKE_DIR}/docker.log"
rc=0
correr aplicar || rc=$?
verificar "aplicar sale con 0" 0 "$rc"
contiene "aplicar recrea solo la API" "${FAKE_DIR}/docker.log" "up -d --force-recreate api"
contiene "aplicar une todos los compose del directorio" "${FAKE_DIR}/docker.log" "compose.z-modelos.yaml"
rc=0
correr aplicar --seco || rc=$?
verificar "aplicar --seco sale con 0" 0 "$rc"
correr desinstalar --seco
[[ -e "$OV" ]] && sigue=0 || sigue=1
verificar "desinstalar --seco conserva el override" 0 "$sigue"
correr desinstalar
[[ ! -e "$OV" ]] && quitado=0 || quitado=1
verificar "desinstalar quita el override" 0 "$quitado"
rm -f "${BASE}/compose/compose.z-modelos.yaml"
rc=0
correr aplicar || rc=$?
verificar "aplicar sin instalación falla con 2" 2 "$rc"

# argumentos inválidos
rc=0
correr nada || rc=$?
verificar "una acción desconocida se rechaza (64)" 64 "$rc"

printf '\n%s pruebas, %s fallas\n' "$PRUEBAS" "$FALLOS"
[[ "$FALLOS" -eq 0 ]]

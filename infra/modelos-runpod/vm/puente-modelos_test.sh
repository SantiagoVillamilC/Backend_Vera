#!/usr/bin/env bash
# infra/modelos-runpod/vm/puente-modelos_test.sh
# Prueba de puente-modelos.sh con un docker simulado y directorios temporales. No usa el docker real ni
# toca /opt ni /etc. Cubre: el rechazo de 0.0.0.0 y de IP inválidas, instalar --seco (no escribe nada),
# instalar (genera los dos archivos con los valores), la detección de un puente huérfano tras recrear la API,
# reparar (recrea solo el puente), reparar sin instalación y desinstalar --seco.
# No cubre el comportamiento real de nginx stream ni de docker compose: eso se probó a mano con docker local.
# Uso: bash infra/modelos-runpod/vm/puente-modelos_test.sh
set -euo pipefail

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="${DIRECTORIO_SCRIPT}/puente-modelos.sh"
BASE="$(mktemp -d)"
trap 'rm -rf "${BASE}"' EXIT
FALLOS=0
PRUEBAS=0

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
if [[ "$1" == "inspect" ]]; then
  formato="$3"
  nombre="$4"
  case "${nombre}:${formato}" in
    centinela-api:'{{.Id}}') cat "${FAKE_DIR}/id-api" ;;
    centinela-modelos-puente:'{{.HostConfig.NetworkMode}}') cat "${FAKE_DIR}/modo-puente" ;;
    centinela-modelos-puente:'{{.State.Running}}') printf 'true\n' ;;
    *) exit 1 ;;
  esac
fi
exit 0
FAKE
chmod +x "${BASE}/docker"

cat > "${BASE}/entorno.env" <<EOF
RUNPOD_API_KEY=rpa_NO_ES_UN_SECRETO
VERA_MODELOS_ESCUCHA=172.25.0.1
VERA_MODELOS_PUERTO_OLLAMA=11434
VERA_MODELOS_PUERTO_VOZ=8766
VERA_MODELOS_PUERTO_STT=8767
VERA_MODELOS_PUENTE_IMAGEN=nginx:1.27.5-alpine
EOF
printf 'VERA_STT_TOKEN=x\n' > "${BASE}/api/modelos.env"

correr() { # correr ARGS... (usa el entorno de prueba, guarda salida en ${BASE}/salida.txt)
  local rc=0
  DOCKER="${BASE}/docker" VERA_MODELOS_ENV="${ENTORNO:-${BASE}/entorno.env}" DIR_COMPOSE="${BASE}/compose" \
    ARCHIVO_TOKENS_API="${BASE}/api/modelos.env" VERA_MODELOS_LOCK="${BASE}/lock" \
    bash "$SCRIPT" "$@" > "${BASE}/salida.txt" 2>&1 || rc=$?
  return "$rc"
}

# 0.0.0.0 y entradas inválidas
printf 'VERA_MODELOS_ESCUCHA=0.0.0.0\n' > "${BASE}/malo.env"
rc=0
ENTORNO="${BASE}/malo.env" correr instalar || rc=$?
verificar "ESCUCHA=0.0.0.0 se rechaza" 64 "$rc"
printf 'VERA_MODELOS_ESCUCHA=no-es-ip\n' > "${BASE}/malo2.env"
rc=0
ENTORNO="${BASE}/malo2.env" correr instalar || rc=$?
verificar "ESCUCHA inválida se rechaza" 64 "$rc"

# instalar --seco: imprime y no escribe
rc=0
correr instalar --seco || rc=$?
verificar "instalar --seco sale con 0" 0 "$rc"
contiene "el plan trae el proxy_pass al gateway" "${BASE}/salida.txt" "proxy_pass 172.25.0.1:8766;"
[[ ! -e "${BASE}/compose/compose.z-modelos.yaml" ]] && ninguno=0 || ninguno=1
verificar "instalar --seco no escribe el override" 0 "$ninguno"

# instalar: genera los dos archivos
rc=0
correr instalar || rc=$?
verificar "instalar sale con 0" 0 "$rc"
contiene "el override fija VERA_PROVEEDOR=local" "${BASE}/compose/compose.z-modelos.yaml" 'VERA_PROVEEDOR: "local"'
contiene "el override apunta a loopback para voz" "${BASE}/compose/compose.z-modelos.yaml" 'VERA_VOZ_URL: "http://127.0.0.1:8766"'
contiene "el puente comparte la red de la API" "${BASE}/compose/compose.z-modelos.yaml" 'network_mode: "service:api"'
no_contiene "el override no trae la clave de RunPod" "${BASE}/compose/compose.z-modelos.yaml" "rpa_"
contiene "la configuración escucha en loopback" "${BASE}/compose/modelos-puente/puente-modelos.conf" "listen 127.0.0.1:11434;"

# verificar: sano, huérfano
printf 'abc123\n' > "${FAKE_DIR}/id-api"
printf 'container:abc123\n' > "${FAKE_DIR}/modo-puente"
rc=0
correr verificar || rc=$?
verificar "puente alineado con la API sale con 0" 0 "$rc"
printf 'def456\n' > "${FAKE_DIR}/id-api"
rc=0
correr verificar || rc=$?
verificar "API recreada deja el puente huérfano (1)" 1 "$rc"
contiene "el mensaje dice HUERFANO" "${BASE}/salida.txt" "RESULTADO=HUERFANO"

# reparar recrea solo el puente
: > "${FAKE_DIR}/docker.log"
rc=0
correr reparar || rc=$?
verificar "reparar sale con 0" 0 "$rc"
contiene "reparar recrea puente-modelos" "${FAKE_DIR}/docker.log" "up -d --force-recreate puente-modelos"
no_contiene "reparar no recrea la API" "${FAKE_DIR}/docker.log" "force-recreate api"

# reparar con el puente sano no hace nada
printf 'container:def456\n' > "${FAKE_DIR}/modo-puente"
: > "${FAKE_DIR}/docker.log"
rc=0
correr reparar || rc=$?
verificar "reparar con puente sano sale con 0" 0 "$rc"
no_contiene "y no ejecuta compose" "${FAKE_DIR}/docker.log" "compose"

# sin instalación
rm -f "${BASE}/compose/compose.z-modelos.yaml"
rc=0
correr verificar || rc=$?
verificar "sin instalación verificar sale con 2" 2 "$rc"
rc=0
correr reparar || rc=$?
verificar "sin instalación reparar sale con 0" 0 "$rc"

# desinstalar --seco no borra nada
correr instalar
rc=0
correr desinstalar --seco || rc=$?
verificar "desinstalar --seco sale con 0" 0 "$rc"
[[ -e "${BASE}/compose/compose.z-modelos.yaml" ]] && sigue=0 || sigue=1
verificar "desinstalar --seco conserva el override" 0 "$sigue"

printf '\n%s pruebas, %s fallas\n' "$PRUEBAS" "$FALLOS"
[[ "$FALLOS" -eq 0 ]]

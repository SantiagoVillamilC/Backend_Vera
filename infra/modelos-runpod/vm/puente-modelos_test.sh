#!/usr/bin/env bash
# infra/modelos-runpod/vm/puente-modelos_test.sh
# Prueba de puente-modelos.sh con un docker simulado y directorios temporales. No usa el docker real ni
# toca /opt ni /etc. Cubre: el rechazo de 0.0.0.0, de IP inválidas y de IP distintas por servicio, el modo
# salto (modelos-control en loopback) y el modo directo, instalar --seco (no escribe nada), la detección de
# un puente huérfano tras recrear la API, reparar (recrea solo el puente y con --no-deps), reparar sin
# instalación y desinstalar --seco.
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
if [[ "$1" == "network" ]]; then
  printf '172.25.0.1\n'
  exit 0
fi
if [[ "$1" == "inspect" ]]; then
  formato="$3"
  nombre="$4"
  case "${nombre}:${formato}" in
    centinela-api:'{{.Id}}') cat "${FAKE_DIR}/id-api" ;;
    centinela-modelos-puente:'{{.HostConfig.NetworkMode}}') cat "${FAKE_DIR}/modo-puente" ;;
    centinela-modelos-puente:'{{.State.Running}}') printf 'true\n' ;;
    centinela-modelos-puente-host:'{{.State.Running}}') cat "${FAKE_DIR}/host-corriendo" ;;
    *) exit 1 ;;
  esac
fi
exit 0
FAKE
chmod +x "${BASE}/docker"
printf 'true\n' > "${FAKE_DIR}/host-corriendo"

# Entorno con modelos-control en loopback (lo que acepta hoy). RUNPOD_API_KEY está para comprobar que no se filtra.
cat > "${BASE}/entorno.env" <<EOF
RUNPOD_API_KEY=rpa_NO_ES_UN_SECRETO
MODELOS_ESCUCHA_OLLAMA=127.0.0.1:11434
MODELOS_ESCUCHA_VOZ=127.0.0.1:8766
MODELOS_ESCUCHA_STT=127.0.0.1:8767
MODELOS_PUENTE_IMAGEN=nginx:1.27.5-alpine
EOF
printf 'VERA_STT_TOKEN=x\n' > "${BASE}/api/modelos.env"

correr() { # correr ARGS... (usa el entorno de prueba, guarda salida en ${BASE}/salida.txt)
  local rc=0
  DOCKER="${BASE}/docker" VERA_MODELOS_ENV="${ENTORNO:-${BASE}/entorno.env}" DIR_COMPOSE="${BASE}/compose" \
    ARCHIVO_TOKENS_API="${BASE}/api/modelos.env" VERA_MODELOS_LOCK="${BASE}/lock" \
    bash "$SCRIPT" "$@" > "${BASE}/salida.txt" 2>&1 || rc=$?
  return "$rc"
}

# Entradas inválidas
printf 'MODELOS_ESCUCHA_OLLAMA=0.0.0.0:11434\nMODELOS_ESCUCHA_VOZ=0.0.0.0:8766\nMODELOS_ESCUCHA_STT=0.0.0.0:8767\n' > "${BASE}/malo.env"
rc=0
ENTORNO="${BASE}/malo.env" correr instalar || rc=$?
verificar "escuchar en 0.0.0.0 se rechaza" 64 "$rc"
printf 'MODELOS_ESCUCHA_OLLAMA=no-es-ip:11434\n' > "${BASE}/malo2.env"
rc=0
ENTORNO="${BASE}/malo2.env" correr instalar || rc=$?
verificar "una dirección inválida se rechaza" 64 "$rc"
printf 'MODELOS_ESCUCHA_OLLAMA=127.0.0.1:11434\nMODELOS_ESCUCHA_VOZ=127.0.0.1:8766\nMODELOS_ESCUCHA_STT=172.25.0.1:8767\n' > "${BASE}/malo3.env"
rc=0
ENTORNO="${BASE}/malo3.env" correr instalar || rc=$?
verificar "IP distinta por servicio se rechaza" 64 "$rc"

# instalar --seco: imprime y no escribe
rc=0
correr instalar --seco || rc=$?
verificar "instalar --seco sale con 0" 0 "$rc"
contiene "el plan trae el proxy_pass al gateway" "${BASE}/salida.txt" "proxy_pass 172.25.0.1:8766;"
contiene "el plan trae el salto en el host al loopback" "${BASE}/salida.txt" "proxy_pass 127.0.0.1:8766;"
[[ ! -e "${BASE}/compose/compose.z-modelos.yaml" ]] && ninguno=0 || ninguno=1
verificar "instalar --seco no escribe el override" 0 "$ninguno"

# instalar en modo salto: genera tres archivos
rc=0
correr instalar || rc=$?
verificar "instalar sale con 0" 0 "$rc"
contiene "el override fija VERA_PROVEEDOR=local" "${BASE}/compose/compose.z-modelos.yaml" 'VERA_PROVEEDOR: "local"'
contiene "el override apunta a loopback para voz" "${BASE}/compose/compose.z-modelos.yaml" 'VERA_VOZ_URL: "http://127.0.0.1:8766"'
contiene "el puente comparte la red de la API" "${BASE}/compose/compose.z-modelos.yaml" 'network_mode: "service:api"'
contiene "el salto corre en la red del host" "${BASE}/compose/compose.z-modelos.yaml" 'network_mode: host'
no_contiene "el override no trae la clave de RunPod" "${BASE}/compose/compose.z-modelos.yaml" "rpa_"
no_contiene "no quedan marcadores sin sustituir" "${BASE}/compose/compose.z-modelos.yaml" "@"
contiene "el puente de la API escucha en loopback" "${BASE}/compose/modelos-puente/puente-modelos.conf" "listen 127.0.0.1:11434;"
contiene "el salto escucha en la IP del gateway" "${BASE}/compose/modelos-puente/puente-host.conf" "listen 172.25.0.1:11434;"
no_contiene "ningún proxy escucha en 0.0.0.0" "${BASE}/compose/modelos-puente/puente-host.conf" "listen 0.0.0.0"

# verificar: sano, huérfano, salto caído
printf 'abc123\n' > "${FAKE_DIR}/id-api"
printf 'container:abc123\n' > "${FAKE_DIR}/modo-puente"
rc=0
correr verificar || rc=$?
verificar "puente alineado con la API sale con 0" 0 "$rc"
printf 'false\n' > "${FAKE_DIR}/host-corriendo"
rc=0
correr verificar || rc=$?
verificar "salto caído deja el puente no sano (1)" 1 "$rc"
printf 'true\n' > "${FAKE_DIR}/host-corriendo"
printf 'def456\n' > "${FAKE_DIR}/id-api"
rc=0
correr verificar || rc=$?
verificar "API recreada deja el puente huérfano (1)" 1 "$rc"
contiene "el mensaje dice HUERFANO" "${BASE}/salida.txt" "RESULTADO=HUERFANO"

# reparar recrea solo los puentes y con --no-deps
: > "${FAKE_DIR}/docker.log"
rc=0
correr reparar || rc=$?
verificar "reparar sale con 0" 0 "$rc"
contiene "reparar recrea puente-modelos sin dependencias" "${FAKE_DIR}/docker.log" "up -d --no-deps --force-recreate puente-modelos"
contiene "reparar recrea también el salto" "${FAKE_DIR}/docker.log" "up -d --no-deps --force-recreate puente-host-modelos"
no_contiene "reparar no recrea la API" "${FAKE_DIR}/docker.log" "force-recreate api"

# reparar con el puente sano no hace nada
printf 'container:def456\n' > "${FAKE_DIR}/modo-puente"
: > "${FAKE_DIR}/docker.log"
rc=0
correr reparar || rc=$?
verificar "reparar con puente sano sale con 0" 0 "$rc"
no_contiene "y no ejecuta compose" "${FAKE_DIR}/docker.log" "compose"

# aplicar --seco lista los servicios de puente y la API
rc=0
correr aplicar --seco || rc=$?
verificar "aplicar --seco sale con 0" 0 "$rc"
contiene "aplicar recrea la API y ambos puentes" "${BASE}/salida.txt" "up -d --force-recreate api puente-modelos puente-host-modelos"

# modo directo: modelos-control escucha en una IP privada concreta, sin salto
cat > "${BASE}/directo.env" <<EOF
MODELOS_ESCUCHA_OLLAMA=172.25.0.1:11434
MODELOS_ESCUCHA_VOZ=172.25.0.1:8766
MODELOS_ESCUCHA_STT=172.25.0.1:8767
EOF
rm -rf "${BASE}/compose"
mkdir -p "${BASE}/compose"
rc=0
ENTORNO="${BASE}/directo.env" correr instalar || rc=$?
verificar "instalar en modo directo sale con 0" 0 "$rc"
no_contiene "sin salto: el override no usa la red del host" "${BASE}/compose/compose.z-modelos.yaml" "network_mode: host"
[[ ! -e "${BASE}/compose/modelos-puente/puente-host.conf" ]] && sin_salto=0 || sin_salto=1
verificar "sin salto no se genera puente-host.conf" 0 "$sin_salto"
contiene "el puente va directo a modelos-control" "${BASE}/compose/modelos-puente/puente-modelos.conf" "proxy_pass 172.25.0.1:8766;"

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

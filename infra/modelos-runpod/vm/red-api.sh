#!/usr/bin/env bash
# infra/modelos-runpod/vm/red-api.sh
# Conecta modelos-control (servicio systemd en la VM) con la API de Vera (contenedor centinela-api).
#
# Por qué existe: la API corre en la red docker "centinela", donde 127.0.0.1 es el loopback del propio
# contenedor y no llega a lo que escucha en el host. Decisión de red aprobada (opción B, sin puentes nginx):
#   modelos-control escucha en la IP del gateway de esa red (MODELOS_ESCUCHA_*=GATEWAY:puerto) y la API
#   la usa como destino (VERA_*_URL) y la autoriza con VERA_MODELOS_HOST_PERMITIDO.
# El gateway se LEE de la VM real con "docker network inspect", nunca se inventa. Este script lo escribe en
# los dos únicos sitios donde debe coincidir: modelos-control.env y compose.z-modelos.yaml.
#
# Subcomandos (los que cambian algo aceptan --seco: imprimen y no ejecutan):
#   gateway              imprime la IP del gateway de la red docker (solo lectura)
#   instalar [--seco]    escribe el gateway en modelos-control.env y genera compose.z-modelos.yaml.
#                        No toca contenedores ni servicios
#   aplicar  [--seco]    recrea la API con el override (VERA_PROVEEDOR=local). Unos segundos de 502
#   verificar            0 todo coincide, 1 hay una discrepancia o un pendiente, 2 falta la instalación
#   desinstalar [--seco] quita el override y recrea la API con su configuración base (simulado)
#
# Variables: VERA_MODELOS_ENV (por defecto /etc/vera-modelos/modelos-control.env), DIR_COMPOSE (por defecto
# /opt/centinela/infra-servidor), ARCHIVO_TOKENS_API (por defecto /opt/centinela/api/modelos.env), RED_DOCKER
# (por defecto centinela), DOCKER, VERA_MODELOS_LOCK. Del archivo de entorno solo se leen claves NO secretas.
set -euo pipefail

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARCHIVO_ENV="${VERA_MODELOS_ENV:-/etc/vera-modelos/modelos-control.env}"
DIR_COMPOSE="${DIR_COMPOSE:-/opt/centinela/infra-servidor}"
ARCHIVO_TOKENS_API="${ARCHIVO_TOKENS_API:-/opt/centinela/api/modelos.env}"
RED_DOCKER="${RED_DOCKER:-centinela}"
DOCKER="${DOCKER:-docker}"
LOCK="${VERA_MODELOS_LOCK:-/run/lock/vera-modelos-red.lock}"
OVERRIDE="${DIR_COMPOSE}/compose.z-modelos.yaml"
MARCADOR="<GATEWAY_DE_DOCKER>"
SECO="no"

GATEWAY=""
PUERTO_OLLAMA="11434"
PUERTO_VOZ="8766"
PUERTO_STT="8767"

error() { printf 'red-api: %s\n' "$1" >&2; exit "${2:-1}"; }
info() { printf 'red-api: %s\n' "$1"; }

# Ejecuta o, con --seco, solo imprime el comando.
ejecutar() {
  if [[ "$SECO" == "si" ]]; then printf '  [seco] %s\n' "$*"; else "$@"; fi
}

# es_privada IP: IPv4 literal en 10/8, 172.16/12 o 192.168/16 (lo mismo que acepta modelos-control).
es_privada() {
  local a b c d
  [[ "$1" =~ ^([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})$ ]] || return 1
  a=$((10#${BASH_REMATCH[1]})); b=$((10#${BASH_REMATCH[2]})); c=$((10#${BASH_REMATCH[3]})); d=$((10#${BASH_REMATCH[4]}))
  (( a <= 255 && b <= 255 && c <= 255 && d <= 255 )) || return 1
  (( a == 10 || (a == 172 && b >= 16 && b <= 31) || (a == 192 && b == 168) ))
}

leer_gateway() {
  local ip
  ip="$("$DOCKER" network inspect "$RED_DOCKER" -f '{{(index .IPAM.Config 0).Gateway}}' 2>/dev/null)" \
    || error "no se pudo leer la red docker ${RED_DOCKER}" 69
  es_privada "$ip" || error "el gateway de ${RED_DOCKER} no es una IPv4 privada (leído: '${ip}')" 69
  GATEWAY="$ip"
}

# valor_de LINEA: lo que va tras el signo igual, sin comillas dobles.
valor_de() {
  local v="${1#*=}"
  v="${v#\"}"
  printf '%s' "${v%\"}"
}

# Lee los puertos de MODELOS_ESCUCHA_* y el SHA, sin ejecutar el archivo ni imprimir nada.
leer_configuracion() {
  local linea clave
  [[ -r "$ARCHIVO_ENV" ]] || error "no se puede leer ${ARCHIVO_ENV}" 66
  while IFS= read -r linea || [[ -n "$linea" ]]; do
    [[ "$linea" =~ ^MODELOS_(ESCUCHA_(OLLAMA|VOZ|STT)|REPO_SHA)= ]] || continue
    clave="${linea%%=*}"
    case "$clave" in
      MODELOS_ESCUCHA_OLLAMA) PUERTO_OLLAMA="$(valor_de "$linea")"; PUERTO_OLLAMA="${PUERTO_OLLAMA##*:}" ;;
      MODELOS_ESCUCHA_VOZ) PUERTO_VOZ="$(valor_de "$linea")"; PUERTO_VOZ="${PUERTO_VOZ##*:}" ;;
      MODELOS_ESCUCHA_STT) PUERTO_STT="$(valor_de "$linea")"; PUERTO_STT="${PUERTO_STT##*:}" ;;
      *) ;;
    esac
  done < "$ARCHIVO_ENV"
  local p
  for p in "$PUERTO_OLLAMA" "$PUERTO_VOZ" "$PUERTO_STT"; do
    if [[ ! "$p" =~ ^[0-9]{4,5}$ ]] || (( 10#$p < 1024 || 10#$p > 65535 )); then
      error "puerto inválido en MODELOS_ESCUCHA_*: '${p}'" 64
    fi
  done
}

banderas_compose() {
  local archivo
  for archivo in "${DIR_COMPOSE}"/compose*.yaml; do
    [[ -f "$archivo" ]] && printf -- '-f\n%s\n' "$archivo"
  done
}

compose() {
  local -a banderas=()
  mapfile -t banderas < <(banderas_compose)
  ( cd "$DIR_COMPOSE" && ejecutar "$DOCKER" compose "${banderas[@]}" "$@" )
}

# Reemplaza los marcadores de la plantilla con valores ya validados.
renderizar_compose() {
  local texto
  texto="$(< "${DIRECTORIO_SCRIPT}/compose.z-modelos.yaml.plantilla")"
  texto="${texto//${MARCADOR}/${GATEWAY}}"
  texto="${texto//@PUERTO_OLLAMA@/${PUERTO_OLLAMA}}"
  texto="${texto//@PUERTO_VOZ@/${PUERTO_VOZ}}"
  texto="${texto//@PUERTO_STT@/${PUERTO_STT}}"
  texto="${texto//@ARCHIVO_TOKENS_API@/${ARCHIVO_TOKENS_API}}"
  printf '%s\n' "$texto"
}

# Devuelve el archivo de entorno con el host de MODELOS_ESCUCHA_* cambiado por el gateway. El resto, incluida
# la clave de RunPod, pasa intacto y nunca se imprime. Si falta una línea de escucha, se agrega.
entorno_con_gateway() {
  local linea clave vistas="" salida=""
  while IFS= read -r linea || [[ -n "$linea" ]]; do
    clave="${linea%%=*}"
    case "$clave" in
      MODELOS_ESCUCHA_OLLAMA) salida+="${clave}=${GATEWAY}:${PUERTO_OLLAMA}"$'\n'; vistas+=" ${clave}" ;;
      MODELOS_ESCUCHA_VOZ) salida+="${clave}=${GATEWAY}:${PUERTO_VOZ}"$'\n'; vistas+=" ${clave}" ;;
      MODELOS_ESCUCHA_STT) salida+="${clave}=${GATEWAY}:${PUERTO_STT}"$'\n'; vistas+=" ${clave}" ;;
      *) salida+="${linea}"$'\n' ;;
    esac
  done < "$ARCHIVO_ENV"
  [[ "$vistas" == *MODELOS_ESCUCHA_OLLAMA* ]] || salida+="MODELOS_ESCUCHA_OLLAMA=${GATEWAY}:${PUERTO_OLLAMA}"$'\n'
  [[ "$vistas" == *MODELOS_ESCUCHA_VOZ* ]] || salida+="MODELOS_ESCUCHA_VOZ=${GATEWAY}:${PUERTO_VOZ}"$'\n'
  [[ "$vistas" == *MODELOS_ESCUCHA_STT* ]] || salida+="MODELOS_ESCUCHA_STT=${GATEWAY}:${PUERTO_STT}"$'\n'
  printf '%s' "$salida"
}

# Escribe con umask 077, modo 0600 y mv atómico: el archivo contiene la clave de RunPod.
escribir_secreto() { # escribir_secreto DESTINO CONTENIDO
  ( umask 077 && printf '%s' "$2" > "${1}.tmp" )
  chmod 0600 "${1}.tmp"
  mv "${1}.tmp" "$1"
}

cmd_instalar() {
  local compose_yaml
  [[ -f "$ARCHIVO_TOKENS_API" ]] || error "falta ${ARCHIVO_TOKENS_API}: ejecuta generar-secretos.sh primero" 66
  leer_configuracion
  leer_gateway
  compose_yaml="$(renderizar_compose)"
  if [[ "$SECO" == "si" ]]; then
    info "gateway leído de la red ${RED_DOCKER}: ${GATEWAY}"
    printf '  [seco] en %s: MODELOS_ESCUCHA_OLLAMA=%s:%s, MODELOS_ESCUCHA_VOZ=%s:%s, MODELOS_ESCUCHA_STT=%s:%s\n' \
      "$ARCHIVO_ENV" "$GATEWAY" "$PUERTO_OLLAMA" "$GATEWAY" "$PUERTO_VOZ" "$GATEWAY" "$PUERTO_STT"
    printf -- '--- %s\n%s\n' "$OVERRIDE" "$compose_yaml"
    return 0
  fi
  escribir_secreto "$ARCHIVO_ENV" "$(entorno_con_gateway)"$'\n'
  printf '%s\n' "$compose_yaml" > "${OVERRIDE}.tmp"
  chmod 0644 "${OVERRIDE}.tmp"
  mv "${OVERRIDE}.tmp" "$OVERRIDE"
  info "gateway ${GATEWAY} escrito en ${ARCHIVO_ENV} y en ${OVERRIDE}. Ningún contenedor ni servicio se tocó"
  info "siguiente: reiniciar vera-modelos-control (para que escuche en ${GATEWAY}) y luego 'aplicar'"
}

cmd_aplicar() {
  [[ -f "$OVERRIDE" ]] || error "no hay ${OVERRIDE}: ejecuta instalar primero" 2
  # --force-recreate: la API toma el env_file y el environment nuevos.
  compose up -d --force-recreate api
  info "API recreada con VERA_PROVEEDOR=local hacia modelos-control en el gateway"
}

# 0 coincide, 1 discrepancia o pendiente, 2 sin instalación. Solo imprime IPs, puertos y avisos.
cmd_verificar() {
  local rc=0 en_env linea actual sha=""
  [[ -f "$OVERRIDE" ]] || { info "RESULTADO=SIN_INSTALAR no existe ${OVERRIDE}"; return 2; }
  leer_configuracion
  leer_gateway
  actual="$GATEWAY"
  while IFS= read -r linea || [[ -n "$linea" ]]; do
    [[ "$linea" =~ ^MODELOS_ESCUCHA_(OLLAMA|VOZ|STT)= ]] || continue
    en_env="$(valor_de "$linea")"
    if [[ "${en_env%:*}" != "$actual" ]]; then
      info "DISCREPANCIA ${linea%%=*} escucha en '${en_env%:*}' y el gateway actual es ${actual}"
      rc=1
    fi
  done < "$ARCHIVO_ENV"
  if [[ "$(< "$OVERRIDE")" != *"VERA_MODELOS_HOST_PERMITIDO: \"${actual}\""* ]]; then
    info "DISCREPANCIA el override de compose no usa el gateway actual ${actual}: ejecuta instalar y aplicar"
    rc=1
  fi
  while IFS= read -r linea || [[ -n "$linea" ]]; do
    [[ "$linea" == MODELOS_REPO_SHA=* ]] && sha="$(valor_de "$linea")"
  done < "$ARCHIVO_ENV"
  if [[ ! "$sha" =~ ^[0-9a-f]{40}$ || "$sha" =~ ^0+$ ]]; then
    info "PENDIENTE MODELOS_REPO_SHA no es un commit real de 40 hexadecimales: el pod saldría con código 2"
    rc=1
  fi
  [[ "$rc" -ne 0 ]] || info "RESULTADO=OK gateway ${actual} coincide en modelos-control.env y en el override"
  return "$rc"
}

cmd_desinstalar() {
  ejecutar rm -f "$OVERRIDE"
  # Sin el override, la API vuelve a los valores de compose.api.yaml (simulado).
  compose up -d --force-recreate api
  info "override quitado, API devuelta a su configuración base. modelos-control.env conserva el gateway"
}

cmd_bloqueado() {
  exec 9>"$LOCK"
  flock -w 60 9 || error "otra operación de red-api está en curso" 75
  "$1"
}

principal() {
  local accion="${1:-}"
  [[ -n "$accion" ]] || error "uso: gateway | instalar | aplicar | verificar | desinstalar [--seco]" 64
  shift
  [[ "${1:-}" != "--seco" ]] || SECO="si"
  case "$accion" in
    gateway) leer_gateway; printf '%s\n' "$GATEWAY" ;;
    instalar) cmd_bloqueado cmd_instalar ;;
    aplicar) cmd_bloqueado cmd_aplicar ;;
    verificar) cmd_verificar ;;
    desinstalar) cmd_bloqueado cmd_desinstalar ;;
    *) error "acción desconocida: ${accion}" 64 ;;
  esac
}

principal "$@"

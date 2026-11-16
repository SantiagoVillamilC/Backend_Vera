#!/usr/bin/env bash
# infra/modelos-runpod/vm/puente-modelos.sh
# Puente entre modelos-control (servicio systemd en la VM) y la API de Vera (contenedor centinela-api).
#
# Por qué existe: la API corre en la red docker "centinela", donde 127.0.0.1 es el loopback del propio
# contenedor, y los clientes de voz y de transcripción solo aceptan http://127.0.0.1
# (internal/copilot/vozlocal/voz.go y sttlocal/cliente.go). Un servicio del host escuchando en el
# 127.0.0.1 de la VM no es alcanzable desde ahí. La solución sin tocar Go: modelos-control escucha en la IP
# del gateway de la red docker y un contenedor puente, dentro del espacio de red de la API
# (network_mode: service:api), reenvía su 127.0.0.1:{11434,8767,8766} hacia esa IP.
#
# Riesgo conocido: si la API se recrea (cualquier despliegue del CD), el puente se queda en el espacio de
# red del contenedor viejo y sigue "running" sin servir a nadie. "verificar" lo detecta comparando el
# contenedor al que apunta el puente con el id actual de centinela-api, y "reparar" lo recrea.
#
# Subcomandos (los que cambian algo aceptan --seco: imprimen y no ejecutan):
#   gateway              imprime la IP del gateway de la red docker (el valor de VERA_MODELOS_ESCUCHA)
#   instalar [--seco]    genera compose.z-modelos.yaml y la configuración del puente. No toca contenedores
#   aplicar  [--seco]    recrea la API y levanta el puente (la API en modo local). Unos segundos de 502
#   verificar            0 sano, 1 puente ausente o huérfano, 2 falta la instalación
#   reparar  [--seco]    si el puente quedó huérfano lo recrea. Idempotente
#   desinstalar [--seco] quita el puente y los archivos y devuelve la API a simulado (rollback)
#
# Variables: VERA_MODELOS_ENV (por defecto /etc/vera-modelos/modelos-control.env), DIR_COMPOSE
# (por defecto /opt/centinela/infra-servidor), ARCHIVO_TOKENS_API (por defecto /opt/centinela/api/modelos.env),
# DOCKER, VERA_MODELOS_LOCK. Del archivo de entorno solo se leen las claves NO secretas de la lista blanca.
set -euo pipefail

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARCHIVO_ENV="${VERA_MODELOS_ENV:-/etc/vera-modelos/modelos-control.env}"
DIR_COMPOSE="${DIR_COMPOSE:-/opt/centinela/infra-servidor}"
ARCHIVO_TOKENS_API="${ARCHIVO_TOKENS_API:-/opt/centinela/api/modelos.env}"
DOCKER="${DOCKER:-docker}"
LOCK="${VERA_MODELOS_LOCK:-/run/lock/vera-modelos-puente.lock}"
OVERRIDE="${DIR_COMPOSE}/compose.z-modelos.yaml"
DIR_PUENTE="${DIR_COMPOSE}/modelos-puente"
ARCHIVO_STREAM="${DIR_PUENTE}/puente-modelos.conf"
CONTENEDOR_PUENTE="centinela-modelos-puente"
CONTENEDOR_API="centinela-api"
SECO="no"

ESCUCHA=""
PUERTO_OLLAMA="11434"
PUERTO_VOZ="8766"
PUERTO_STT="8767"
IMAGEN_PUENTE="nginx:1.27.5-alpine"
RED_DOCKER="centinela"

error() { printf 'puente-modelos: %s\n' "$1" >&2; exit "${2:-1}"; }
info() { printf 'puente-modelos: %s\n' "$1"; }

# Ejecuta o, con --seco, solo imprime el comando.
ejecutar() {
  if [[ "$SECO" == "si" ]]; then
    printf '  [seco] %s\n' "$*"
  else
    "$@"
  fi
}

# Lee SOLO claves no secretas del archivo de entorno, sin ejecutarlo. Acepta valores con comillas dobles.
leer_configuracion() {
  local linea clave valor
  [[ -r "$ARCHIVO_ENV" ]] || error "no se puede leer ${ARCHIVO_ENV}" 66
  while IFS= read -r linea || [[ -n "$linea" ]]; do
    [[ "$linea" =~ ^[A-Z_]+= ]] || continue
    clave="${linea%%=*}"
    valor="${linea#*=}"
    valor="${valor#\"}"
    valor="${valor%\"}"
    case "$clave" in
      VERA_MODELOS_ESCUCHA) ESCUCHA="$valor" ;;
      VERA_MODELOS_PUERTO_OLLAMA) PUERTO_OLLAMA="$valor" ;;
      VERA_MODELOS_PUERTO_VOZ) PUERTO_VOZ="$valor" ;;
      VERA_MODELOS_PUERTO_STT) PUERTO_STT="$valor" ;;
      VERA_MODELOS_PUENTE_IMAGEN) IMAGEN_PUENTE="$valor" ;;
      VERA_MODELOS_RED_DOCKER) RED_DOCKER="$valor" ;;
      *) ;;
    esac
  done < "$ARCHIVO_ENV"
}

validar_configuracion() {
  local puerto
  [[ "$ESCUCHA" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || error "VERA_MODELOS_ESCUCHA debe ser una IPv4 (recibido '${ESCUCHA}')" 64
  [[ "$ESCUCHA" != "0.0.0.0" ]] || error "VERA_MODELOS_ESCUCHA no puede ser 0.0.0.0" 64
  for puerto in "$PUERTO_OLLAMA" "$PUERTO_VOZ" "$PUERTO_STT"; do
    [[ "$puerto" =~ ^[0-9]{4,5}$ ]] || error "puerto inválido: '${puerto}'" 64
    (( 10#$puerto >= 1024 && 10#$puerto <= 65535 )) || error "puerto fuera de rango: '${puerto}'" 64
  done
  [[ "$IMAGEN_PUENTE" =~ ^[A-Za-z0-9._/:@-]+$ ]] || error "VERA_MODELOS_PUENTE_IMAGEN tiene caracteres no permitidos" 64
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

cmd_gateway() {
  "$DOCKER" network inspect "$RED_DOCKER" -f '{{(index .IPAM.Config 0).Gateway}}'
}

# Reemplaza los marcadores @CLAVE@ de una plantilla con los valores ya validados.
renderizar() {
  local contenido
  contenido="$(< "$1")"
  contenido="${contenido//@ESCUCHA@/$ESCUCHA}"
  contenido="${contenido//@PUERTO_OLLAMA@/$PUERTO_OLLAMA}"
  contenido="${contenido//@PUERTO_VOZ@/$PUERTO_VOZ}"
  contenido="${contenido//@PUERTO_STT@/$PUERTO_STT}"
  contenido="${contenido//@IMAGEN_PUENTE@/$IMAGEN_PUENTE}"
  contenido="${contenido//@RUTA_STREAM@/$ARCHIVO_STREAM}"
  contenido="${contenido//@ARCHIVO_TOKENS_API@/$ARCHIVO_TOKENS_API}"
  printf '%s\n' "$contenido"
}

escribir_atomico() { # escribir_atomico DESTINO CONTENIDO
  local destino="$1" contenido="$2"
  printf '%s\n' "$contenido" > "${destino}.tmp"
  chmod 644 "${destino}.tmp"
  mv "${destino}.tmp" "$destino"
}

cmd_instalar() {
  local stream compose_yaml
  leer_configuracion
  validar_configuracion
  [[ -f "$ARCHIVO_TOKENS_API" ]] || error "falta ${ARCHIVO_TOKENS_API}: ejecuta generar-secretos.sh primero" 66
  stream="$(renderizar "${DIRECTORIO_SCRIPT}/puente-stream.conf.plantilla")"
  compose_yaml="$(renderizar "${DIRECTORIO_SCRIPT}/compose.z-modelos.yaml.plantilla")"
  if [[ "$SECO" == "si" ]]; then
    printf -- '--- %s\n%s\n--- %s\n%s\n' "$ARCHIVO_STREAM" "$stream" "$OVERRIDE" "$compose_yaml"
    return 0
  fi
  mkdir -p "$DIR_PUENTE"
  escribir_atomico "$ARCHIVO_STREAM" "$stream"
  escribir_atomico "$OVERRIDE" "$compose_yaml"
  info "instalado: ${OVERRIDE} y ${ARCHIVO_STREAM}. Ningún contenedor se tocó"
}

cmd_aplicar() {
  [[ -f "$OVERRIDE" ]] || error "no hay ${OVERRIDE}: ejecuta instalar primero" 2
  # --force-recreate en la API: toma el env_file y el environment nuevos y el puente se une a su espacio de red.
  compose up -d --force-recreate api puente-modelos
  info "API en VERA_PROVEEDOR=local con el puente a modelos-control"
}

# 0 sano, 1 puente ausente o huérfano, 2 sin instalación.
estado_puente() {
  local modo id_api
  [[ -f "$OVERRIDE" ]] || return 2
  modo="$("$DOCKER" inspect -f '{{.HostConfig.NetworkMode}}' "$CONTENEDOR_PUENTE" 2>/dev/null)" || return 1
  id_api="$("$DOCKER" inspect -f '{{.Id}}' "$CONTENEDOR_API" 2>/dev/null)" || return 1
  [[ "$("$DOCKER" inspect -f '{{.State.Running}}' "$CONTENEDOR_PUENTE" 2>/dev/null)" == "true" ]] || return 1
  [[ "$modo" == "container:${id_api}" ]]
}

cmd_verificar() {
  local rc=0
  estado_puente || rc=$?
  case "$rc" in
    0) info "RESULTADO=OK el puente comparte el espacio de red de la API actual" ;;
    1) info "RESULTADO=HUERFANO el puente falta o apunta a un contenedor de API anterior" ;;
    *) info "RESULTADO=SIN_INSTALAR no existe ${OVERRIDE}" ;;
  esac
  return "$rc"
}

cmd_reparar() {
  local rc=0
  estado_puente || rc=$?
  case "$rc" in
    0) info "puente sano, nada que reparar" ;;
    2) info "no hay instalación, nada que reparar" ;;
    *)
      info "puente huérfano o ausente: se recrea"
      compose up -d --force-recreate puente-modelos
      ;;
  esac
}

cmd_desinstalar() {
  ejecutar "$DOCKER" rm -f "$CONTENEDOR_PUENTE" >/dev/null 2>&1 || true
  ejecutar rm -f "$OVERRIDE" "$ARCHIVO_STREAM"
  # Sin el override, la API vuelve a los valores de compose.api.yaml (simulado).
  compose up -d --force-recreate api
  info "puente quitado, API devuelta a su configuración base"
}

cmd_bloqueado() {
  exec 9>"$LOCK"
  flock -w 60 9 || error "otra operación del puente está en curso" 75
  "$1"
}

principal() {
  local accion="${1:-}"
  [[ -n "$accion" ]] || error "uso: gateway | instalar | aplicar | verificar | reparar | desinstalar [--seco]" 64
  shift
  [[ "${1:-}" != "--seco" ]] || SECO="si"
  case "$accion" in
    gateway)
      # Solo necesita el nombre de la red: funciona antes de que exista el archivo de entorno.
      if [[ -r "$ARCHIVO_ENV" ]]; then leer_configuracion; fi
      cmd_gateway
      ;;
    instalar) cmd_bloqueado cmd_instalar ;;
    aplicar) cmd_bloqueado cmd_aplicar ;;
    verificar) cmd_verificar ;;
    reparar) cmd_bloqueado cmd_reparar ;;
    desinstalar) cmd_bloqueado cmd_desinstalar ;;
    *) error "acción desconocida: ${accion}" 64 ;;
  esac
}

principal "$@"

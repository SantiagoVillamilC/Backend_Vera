#!/usr/bin/env bash
# infra/modelos-runpod/vm/puente-modelos.sh
# Puente entre modelos-control (servicio systemd en la VM) y la API de Vera (contenedor centinela-api).
#
# Por qué existe: la API corre en la red docker "centinela", donde 127.0.0.1 es el loopback del propio
# contenedor, y los clientes de voz y de transcripción solo aceptan http://127.0.0.1
# (internal/copilot/vozlocal/voz.go y sttlocal/cliente.go). Un servicio del host escuchando en el
# 127.0.0.1 de la VM no es alcanzable desde ahí. Solución sin tocar Go, dos formas según dónde escuche
# modelos-control (variables MODELOS_ESCUCHA_OLLAMA, MODELOS_ESCUCHA_VOZ y MODELOS_ESCUCHA_STT):
#   salto    (escucha en loopback, que es lo único que acepta hoy modelos-control)
#            API:127.0.0.1:P -> [puente-modelos, en la red de la API] -> GATEWAY:P
#                            -> [puente-host-modelos, en la red del host] -> 127.0.0.1:P (modelos-control)
#   directo  (escucha en una IP privada concreta, por ejemplo la del gateway)
#            API:127.0.0.1:P -> [puente-modelos] -> IP:P (modelos-control)
# GATEWAY es la IP del gateway de la red docker "centinela". Ningún proxy escucha en 0.0.0.0.
#
# Riesgo conocido: si la API se recrea (cualquier despliegue del CD), el puente se queda en el espacio de
# red del contenedor viejo y sigue "running" sin servir a nadie. "verificar" lo detecta comparando el
# contenedor al que apunta el puente con el id actual de centinela-api, y "reparar" lo recrea.
#
# Subcomandos (los que cambian algo aceptan --seco: imprimen y no ejecutan):
#   gateway              imprime la IP del gateway de la red docker
#   instalar [--seco]    genera compose.z-modelos.yaml y las configuraciones. No toca contenedores
#   aplicar  [--seco]    recrea la API y levanta los puentes (la API en modo local). Unos segundos de 502
#   verificar            0 sano, 1 puente ausente o huérfano, 2 falta la instalación
#   reparar  [--seco]    si el puente quedó huérfano lo recrea (solo el puente, sin tocar sus dependencias)
#   desinstalar [--seco] quita los puentes y los archivos y devuelve la API a simulado (rollback)
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
ARCHIVO_STREAM_HOST="${DIR_PUENTE}/puente-host.conf"
CONTENEDOR_PUENTE="centinela-modelos-puente"
CONTENEDOR_PUENTE_HOST="centinela-modelos-puente-host"
CONTENEDOR_API="centinela-api"
SECO="no"

ESCUCHA_OLLAMA="127.0.0.1:11434"
ESCUCHA_VOZ="127.0.0.1:8766"
ESCUCHA_STT="127.0.0.1:8767"
IMAGEN_PUENTE="nginx:1.27.5-alpine"
RED_DOCKER="centinela"

HOST_MODELOS=""
PUERTO_OLLAMA=""
PUERTO_VOZ=""
PUERTO_STT=""
MODO=""
GATEWAY=""
DESTINO=""

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
      MODELOS_ESCUCHA_OLLAMA) ESCUCHA_OLLAMA="$valor" ;;
      MODELOS_ESCUCHA_VOZ) ESCUCHA_VOZ="$valor" ;;
      MODELOS_ESCUCHA_STT) ESCUCHA_STT="$valor" ;;
      MODELOS_PUENTE_IMAGEN) IMAGEN_PUENTE="$valor" ;;
      MODELOS_RED_DOCKER) RED_DOCKER="$valor" ;;
      *) ;;
    esac
  done < "$ARCHIVO_ENV"
}

# validar_direccion NOMBRE VALOR: exige IPv4:puerto con el puerto entre 1024 y 65535 y rechaza 0.0.0.0.
validar_direccion() {
  local nombre="$1" valor="$2" host puerto
  [[ "$valor" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}:[0-9]{4,5}$ ]] || error "${nombre} debe ser IPv4:puerto (recibido '${valor}')" 64
  host="${valor%:*}"
  puerto="${valor##*:}"
  [[ "$host" != "0.0.0.0" ]] || error "${nombre} no puede escuchar en 0.0.0.0" 64
  (( 10#$puerto >= 1024 && 10#$puerto <= 65535 )) || error "${nombre}: puerto fuera de rango" 64
}

validar_configuracion() {
  local host_ollama host_voz host_stt
  validar_direccion MODELOS_ESCUCHA_OLLAMA "$ESCUCHA_OLLAMA"
  validar_direccion MODELOS_ESCUCHA_VOZ "$ESCUCHA_VOZ"
  validar_direccion MODELOS_ESCUCHA_STT "$ESCUCHA_STT"
  host_ollama="${ESCUCHA_OLLAMA%:*}"
  host_voz="${ESCUCHA_VOZ%:*}"
  host_stt="${ESCUCHA_STT%:*}"
  [[ "$host_ollama" == "$host_voz" && "$host_voz" == "$host_stt" ]] \
    || error "las tres direcciones MODELOS_ESCUCHA_* deben usar la misma IP" 64
  HOST_MODELOS="$host_ollama"
  PUERTO_OLLAMA="${ESCUCHA_OLLAMA##*:}"
  PUERTO_VOZ="${ESCUCHA_VOZ##*:}"
  PUERTO_STT="${ESCUCHA_STT##*:}"
  [[ "$IMAGEN_PUENTE" =~ ^[A-Za-z0-9._/:@-]+$ ]] || error "MODELOS_PUENTE_IMAGEN tiene caracteres no permitidos" 64
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

# Decide el modo según dónde escucha modelos-control y fija GATEWAY y DESTINO.
decidir_modo() {
  if [[ "$HOST_MODELOS" == 127.* ]]; then
    MODO="salto"
    GATEWAY="$(cmd_gateway)"
    [[ "$GATEWAY" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || error "no se pudo leer el gateway de la red ${RED_DOCKER}" 69
    DESTINO="$GATEWAY"
  else
    MODO="directo"
    DESTINO="$HOST_MODELOS"
  fi
}

# Reemplaza los marcadores @CLAVE@ de un texto con los valores ya validados.
sustituir() {
  local contenido="$1"
  contenido="${contenido//@DESTINO@/$DESTINO}"
  contenido="${contenido//@GATEWAY@/$GATEWAY}"
  contenido="${contenido//@ESCUCHA_MODELOS@/$HOST_MODELOS}"
  contenido="${contenido//@PUERTO_OLLAMA@/$PUERTO_OLLAMA}"
  contenido="${contenido//@PUERTO_VOZ@/$PUERTO_VOZ}"
  contenido="${contenido//@PUERTO_STT@/$PUERTO_STT}"
  contenido="${contenido//@IMAGEN_PUENTE@/$IMAGEN_PUENTE}"
  contenido="${contenido//@RUTA_STREAM@/$ARCHIVO_STREAM}"
  contenido="${contenido//@RUTA_STREAM_HOST@/$ARCHIVO_STREAM_HOST}"
  contenido="${contenido//@ARCHIVO_TOKENS_API@/$ARCHIVO_TOKENS_API}"
  printf '%s' "$contenido"
}

renderizar_compose() {
  local plantilla fragmento=""
  plantilla="$(< "${DIRECTORIO_SCRIPT}/compose.z-modelos.yaml.plantilla")"
  if [[ "$MODO" == "salto" ]]; then
    fragmento="$(< "${DIRECTORIO_SCRIPT}/compose.salto-host.fragmento")"
  fi
  plantilla="${plantilla//@SERVICIO_SALTO@/$fragmento}"
  sustituir "$plantilla"
}

escribir_atomico() { # escribir_atomico DESTINO CONTENIDO
  local destino="$1" contenido="$2"
  printf '%s\n' "$contenido" > "${destino}.tmp"
  chmod 644 "${destino}.tmp"
  mv "${destino}.tmp" "$destino"
}

cmd_instalar() {
  local stream compose_yaml stream_host=""
  leer_configuracion
  validar_configuracion
  [[ -f "$ARCHIVO_TOKENS_API" ]] || error "falta ${ARCHIVO_TOKENS_API}: ejecuta generar-secretos.sh primero" 66
  decidir_modo
  stream="$(sustituir "$(< "${DIRECTORIO_SCRIPT}/puente-stream.conf.plantilla")")"
  compose_yaml="$(renderizar_compose)"
  if [[ "$MODO" == "salto" ]]; then
    stream_host="$(sustituir "$(< "${DIRECTORIO_SCRIPT}/puente-host.conf.plantilla")")"
  fi
  if [[ "$SECO" == "si" ]]; then
    info "modo ${MODO}, destino del puente ${DESTINO}"
    printf -- '--- %s\n%s\n--- %s\n%s\n' "$ARCHIVO_STREAM" "$stream" "$OVERRIDE" "$compose_yaml"
    [[ -z "$stream_host" ]] || printf -- '--- %s\n%s\n' "$ARCHIVO_STREAM_HOST" "$stream_host"
    return 0
  fi
  mkdir -p "$DIR_PUENTE"
  escribir_atomico "$ARCHIVO_STREAM" "$stream"
  if [[ -n "$stream_host" ]]; then
    escribir_atomico "$ARCHIVO_STREAM_HOST" "$stream_host"
  else
    rm -f "$ARCHIVO_STREAM_HOST"
  fi
  escribir_atomico "$OVERRIDE" "$compose_yaml"
  info "instalado en modo ${MODO}: ${OVERRIDE}. Ningún contenedor se tocó"
}

# Servicios de puente definidos en la instalación actual.
servicios_puente() {
  printf 'puente-modelos\n'
  if [[ -f "$ARCHIVO_STREAM_HOST" ]]; then
    printf 'puente-host-modelos\n'
  fi
}

cmd_aplicar() {
  local -a servicios=()
  [[ -f "$OVERRIDE" ]] || error "no hay ${OVERRIDE}: ejecuta instalar primero" 2
  mapfile -t servicios < <(servicios_puente)
  # --force-recreate en la API: toma el env_file y el environment nuevos y el puente se une a su espacio de red.
  compose up -d --force-recreate api "${servicios[@]}"
  info "API en VERA_PROVEEDOR=local con el puente a modelos-control"
}

contenedor_corriendo() {
  [[ "$("$DOCKER" inspect -f '{{.State.Running}}' "$1" 2>/dev/null)" == "true" ]]
}

# 0 sano, 1 puente ausente o huérfano, 2 sin instalación.
estado_puente() {
  local modo id_api
  [[ -f "$OVERRIDE" ]] || return 2
  modo="$("$DOCKER" inspect -f '{{.HostConfig.NetworkMode}}' "$CONTENEDOR_PUENTE" 2>/dev/null)" || return 1
  id_api="$("$DOCKER" inspect -f '{{.Id}}' "$CONTENEDOR_API" 2>/dev/null)" || return 1
  contenedor_corriendo "$CONTENEDOR_PUENTE" || return 1
  if [[ -f "$ARCHIVO_STREAM_HOST" ]]; then
    contenedor_corriendo "$CONTENEDOR_PUENTE_HOST" || return 1
  fi
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
  local rc=0 servicio
  local -a servicios=()
  estado_puente || rc=$?
  case "$rc" in
    0) info "puente sano, nada que reparar" ;;
    2) info "no hay instalación, nada que reparar" ;;
    *)
      info "puente huérfano o ausente: se recrea"
      mapfile -t servicios < <(servicios_puente)
      for servicio in "${servicios[@]}"; do
        # --no-deps: solo el puente. Sin esta bandera compose arrastraría api, migrate, postgres y redis.
        compose up -d --no-deps --force-recreate "$servicio"
      done
      ;;
  esac
}

cmd_desinstalar() {
  ejecutar "$DOCKER" rm -f "$CONTENEDOR_PUENTE" "$CONTENEDOR_PUENTE_HOST" >/dev/null 2>&1 || true
  ejecutar rm -f "$OVERRIDE" "$ARCHIVO_STREAM" "$ARCHIVO_STREAM_HOST"
  # Sin el override, la API vuelve a los valores de compose.api.yaml (simulado).
  compose up -d --force-recreate api
  info "puentes quitados, API devuelta a su configuración base"
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

#!/usr/bin/env bash
# infra/modelos-runpod/vm/generar-secretos.sh
# Crea en la VM, SIN que ningún secreto pase por el chat, por argv ni por el repositorio:
#   1. el usuario de sistema vera-modelos (sin shell, sin home)
#   2. /etc/vera-modelos/modelos-control.env  (root:root 0600, a partir de .env.example)
#   3. /opt/centinela/api/modelos.env         (root:root 0600, solo VERA_STT_TOKEN y VERA_VOZ_TOKEN)
#   4. la llave SSH del túnel en /etc/vera-modelos/ssh (privada: vera-modelos 0600)
# Los tres tokens salen de openssl rand -hex 32. La clave de RunPod se lee de la entrada estándar sin eco.
#
# Uso (como root en la VM, con una terminal real):
#   sudo bash generar-secretos.sh [--seco] [--rotar] [--clave-runpod] [--sin-usuario]
#                                 [--escucha IP] [--codigo-sha COMMIT]
#   --seco           imprime lo que haría y no cambia nada (no lee la clave)
#   --rotar          genera tokens nuevos (hay que reiniciar modelos-control y recrear la API después)
#   --clave-runpod   vuelve a pedir la clave de RunPod aunque el archivo ya exista
#   --sin-usuario    no crea el usuario ni cambia dueños. Solo para ensayos fuera de la VM
#   --escucha IP     fija VERA_MODELOS_ESCUCHA (la IP del gateway: puente-modelos.sh gateway)
#   --codigo-sha C   fija VERA_MODELOS_CODIGO_SHA (commit de Backend_Vera de 40 caracteres)
#
# Sin --rotar es idempotente: lo que ya existe no se regenera y nunca se imprime ningún secreto.
# Variables de ruta para ensayos: VERA_MODELOS_ETC, ARCHIVO_TOKENS_API, PLANTILLA_ENV.
set -euo pipefail

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ETC="${VERA_MODELOS_ETC:-/etc/vera-modelos}"
ARCHIVO_ENV="${ETC}/modelos-control.env"
DIR_SSH="${ETC}/ssh"
CLAVE_SSH="${DIR_SSH}/tunel_ed25519"
ARCHIVO_TOKENS_API="${ARCHIVO_TOKENS_API:-/opt/centinela/api/modelos.env}"
PLANTILLA_ENV="${PLANTILLA_ENV:-${DIRECTORIO_SCRIPT}/../.env.example}"
USUARIO="vera-modelos"
SECO="no"
ROTAR="no"
PEDIR_CLAVE="no"
SIN_USUARIO="no"
ESCUCHA=""
CODIGO_SHA=""

declare -A VALORES=()
CLAVES_SECRETAS=(RUNPOD_API_KEY VERA_MODELOS_TOKEN_CONTROL VERA_MODELOS_TOKEN_VOZ VERA_MODELOS_TOKEN_STT)

error() { printf 'generar-secretos: %s\n' "$1" >&2; exit "${2:-1}"; }
info() { printf 'generar-secretos: %s\n' "$1"; }

leer_argumentos() {
  while (( $# > 0 )); do
    case "$1" in
      --seco) SECO="si" ;;
      --rotar) ROTAR="si" ;;
      --clave-runpod) PEDIR_CLAVE="si" ;;
      --sin-usuario) SIN_USUARIO="si" ;;
      --escucha) shift; ESCUCHA="${1:-}" ;;
      --codigo-sha) shift; CODIGO_SHA="${1:-}" ;;
      *) error "argumento no permitido: $1" 64 ;;
    esac
    shift
  done
  [[ -z "$ESCUCHA" || "$ESCUCHA" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || error "--escucha debe ser una IPv4" 64
  [[ "$ESCUCHA" != "0.0.0.0" ]] || error "--escucha no puede ser 0.0.0.0" 64
  [[ -z "$CODIGO_SHA" || "$CODIGO_SHA" =~ ^[0-9a-f]{40}$ ]] || error "--codigo-sha debe ser un commit de 40 caracteres hexadecimales" 64
}

exigir_herramientas() {
  local c
  for c in openssl ssh-keygen install mv chmod; do
    command -v "$c" >/dev/null 2>&1 || error "falta el comando ${c} en la VM" 69
  done
  [[ "$SECO" == "si" || "$SIN_USUARIO" == "si" || "$(id -u)" == "0" ]] || error "ejecuta con sudo (hace falta root)" 77
  [[ -f "$PLANTILLA_ENV" ]] || error "no existe la plantilla ${PLANTILLA_ENV}" 66
}

# Lee una clave de un archivo KEY=valor sin ejecutarlo. No imprime nada.
leer_valor() { # leer_valor ARCHIVO CLAVE
  local linea
  [[ -f "$1" ]] || return 1
  while IFS= read -r linea || [[ -n "$linea" ]]; do
    if [[ "$linea" == "$2="* ]]; then
      printf '%s' "${linea#*=}"
      return 0
    fi
  done < "$1"
  return 1
}

token_nuevo() { openssl rand -hex 32; }

asegurar_usuario() {
  if [[ "$SIN_USUARIO" == "si" ]]; then
    return 0
  fi
  if getent passwd "$USUARIO" >/dev/null 2>&1; then
    info "el usuario ${USUARIO} ya existe"
    return 0
  fi
  if [[ "$SECO" == "si" ]]; then
    info "[seco] useradd --system --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin ${USUARIO}"
    return 0
  fi
  useradd --system --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin "$USUARIO"
  info "usuario ${USUARIO} creado (sin shell, sin home)"
}

asegurar_directorios() {
  if [[ "$SECO" == "si" ]]; then
    info "[seco] directorios ${ETC} (0755 root), ${DIR_SSH} (0700 ${USUARIO}) y $(dirname "$ARCHIVO_TOKENS_API") (0700 root)"
    return 0
  fi
  install -d -m 0755 "$ETC"
  install -d -m 0700 "$(dirname "$ARCHIVO_TOKENS_API")"
  if [[ "$SIN_USUARIO" == "si" ]]; then
    install -d -m 0700 "$DIR_SSH"
  else
    install -d -m 0700 -o "$USUARIO" -g "$USUARIO" "$DIR_SSH"
  fi
}

asegurar_llave_ssh() {
  if [[ -f "$CLAVE_SSH" ]]; then
    info "la llave SSH del túnel ya existe"
    return 0
  fi
  if [[ "$SECO" == "si" ]]; then
    info "[seco] ssh-keygen -t ed25519 -N '' -f ${CLAVE_SSH} (privada ${USUARIO} 0600, pública 0644)"
    return 0
  fi
  ssh-keygen -q -t ed25519 -N '' -C "vera-modelos@centinela-vm" -f "$CLAVE_SSH"
  chmod 0600 "$CLAVE_SSH"
  chmod 0644 "${CLAVE_SSH}.pub"
  if [[ "$SIN_USUARIO" != "si" ]]; then
    chown "${USUARIO}:${USUARIO}" "$CLAVE_SSH" "${CLAVE_SSH}.pub"
  fi
  info "llave SSH creada. Parte pública (no es secreta): $(< "${CLAVE_SSH}.pub")"
}

# Define el valor de cada secreto: conserva el del archivo existente salvo --rotar.
decidir_secretos() {
  local clave existente
  for clave in VERA_MODELOS_TOKEN_CONTROL VERA_MODELOS_TOKEN_VOZ VERA_MODELOS_TOKEN_STT; do
    existente="$(leer_valor "$ARCHIVO_ENV" "$clave" || true)"
    if [[ -n "$existente" && "$ROTAR" != "si" ]]; then
      VALORES[$clave]="$existente"
    elif [[ "$SECO" == "si" ]]; then
      VALORES[$clave]="(se generaría con openssl rand -hex 32)"
    else
      VALORES[$clave]="$(token_nuevo)"
    fi
  done
}

decidir_clave_runpod() {
  local existente
  existente="$(leer_valor "$ARCHIVO_ENV" RUNPOD_API_KEY || true)"
  if [[ -n "$existente" && "$existente" != rpa_REEMPLAZAR* && "$PEDIR_CLAVE" != "si" ]]; then
    VALORES[RUNPOD_API_KEY]="$existente"
    return 0
  fi
  if [[ "$SECO" == "si" ]]; then
    VALORES[RUNPOD_API_KEY]="(se pediría por la entrada estándar, sin eco)"
    return 0
  fi
  # Sin eco en una terminal y sin argv. También acepta un pipe: "... | sudo bash generar-secretos.sh".
  printf 'Pega la clave de la API de RunPod y pulsa Enter (no se muestra): ' >&2
  IFS= read -rs clave_leida || true
  printf '\n' >&2
  [[ "$clave_leida" =~ ^[A-Za-z0-9_-]{16,200}$ ]] || error "la clave de RunPod está vacía o tiene caracteres no permitidos" 65
  VALORES[RUNPOD_API_KEY]="$clave_leida"
  clave_leida=""
}

# Reescribe el archivo línea por línea: reemplaza solo las claves con valor decidido.
construir_env() {
  local base linea clave salida=""
  if [[ -f "$ARCHIVO_ENV" ]]; then base="$ARCHIVO_ENV"; else base="$PLANTILLA_ENV"; fi
  [[ -z "$ESCUCHA" ]] || VALORES[VERA_MODELOS_ESCUCHA]="$ESCUCHA"
  [[ -z "$CODIGO_SHA" ]] || VALORES[VERA_MODELOS_CODIGO_SHA]="$CODIGO_SHA"
  while IFS= read -r linea || [[ -n "$linea" ]]; do
    clave="${linea%%=*}"
    if [[ "$linea" == *=* && -n "${VALORES[$clave]+x}" ]]; then
      salida+="${clave}=${VALORES[$clave]}"$'\n'
    else
      salida+="${linea}"$'\n'
    fi
  done < "$base"
  printf '%s' "$salida"
}

escribir_secreto() { # escribir_secreto DESTINO CONTENIDO
  local destino="$1" contenido="$2"
  ( umask 077 && printf '%s' "$contenido" > "${destino}.tmp" )
  chmod 0600 "${destino}.tmp"
  mv "${destino}.tmp" "$destino"
}

escribir_archivos() {
  local contenido_env contenido_api
  contenido_env="$(construir_env)"
  contenido_api="VERA_STT_TOKEN=${VALORES[VERA_MODELOS_TOKEN_STT]}"$'\n'"VERA_VOZ_TOKEN=${VALORES[VERA_MODELOS_TOKEN_VOZ]}"$'\n'
  if [[ "$SECO" == "si" ]]; then
    info "[seco] escribiría ${ARCHIVO_ENV} (root 0600) con ${#CLAVES_SECRETAS[@]} secretos y el resto de la plantilla"
    info "[seco] escribiría ${ARCHIVO_TOKENS_API} (root 0600) con VERA_STT_TOKEN y VERA_VOZ_TOKEN"
    return 0
  fi
  escribir_secreto "$ARCHIVO_ENV" "$contenido_env"
  escribir_secreto "$ARCHIVO_TOKENS_API" "$contenido_api"
  info "escritos ${ARCHIVO_ENV} y ${ARCHIVO_TOKENS_API} (0600). Ningún secreto se imprimió"
}

principal() {
  leer_argumentos "$@"
  exigir_herramientas
  asegurar_usuario
  asegurar_directorios
  asegurar_llave_ssh
  decidir_secretos
  decidir_clave_runpod
  escribir_archivos
  if [[ "$ROTAR" == "si" ]]; then
    info "tokens rotados: reinicia vera-modelos-control y recrea la API (puente-modelos.sh aplicar)"
  fi
}

principal "$@"

#!/usr/bin/env bash
# infra/modelos-runpod/vm/generar-secretos.sh
# Crea en la VM, SIN que ningún secreto pase por el chat, por argv ni por el repositorio:
#   1. el usuario de sistema vera-modelos (sin shell, sin home)
#   2. /etc/vera-modelos/modelos-control.env  root:root 0600: la clave de RunPod y la configuración
#   3. /etc/vera-modelos/tokens.env           vera-modelos 0600: VERA_STT_TOKEN y VERA_VOZ_TOKEN (los lee modelos-control)
#   4. /etc/vera-modelos/control.token        vera-modelos 0600: token del puerto de control (los lee modelos-control)
#   5. /opt/centinela/api/modelos.env         root:root 0600: los mismos dos tokens, para el contenedor de la API
#   6. la llave SSH del túnel en /etc/vera-modelos/ssh (privada: vera-modelos 0600)
# Los tokens salen de openssl rand -hex 32. La clave de RunPod se lee de la entrada estándar sin eco.
# La clave de RunPod vive SOLO en el archivo de root: nunca entra al contenedor de la API.
#
# Uso (como root en la VM, con una terminal real):
#   sudo bash generar-secretos.sh [--seco] [--rotar] [--clave-runpod] [--sin-usuario] [--codigo-sha COMMIT]
#   --seco           imprime lo que haría y no cambia nada (no lee la clave)
#   --rotar          genera tokens nuevos (hay que reiniciar modelos-control y recrear la API después)
#   --clave-runpod   vuelve a pedir la clave de RunPod aunque el archivo ya exista
#   --sin-usuario    no crea el usuario ni cambia dueños. Solo para ensayos fuera de la VM
#   --codigo-sha C   fija MODELOS_REPO_SHA (commit de Backend_Vera de 40 caracteres)
#
# Sin --rotar es idempotente: lo que ya existe no se regenera y nunca se imprime ningún secreto.
# Variables de ruta para ensayos: VERA_MODELOS_ETC, ARCHIVO_TOKENS_API, PLANTILLA_ENV.
set -euo pipefail

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ETC="${VERA_MODELOS_ETC:-/etc/vera-modelos}"
ARCHIVO_ENV="${ETC}/modelos-control.env"
ARCHIVO_TOKENS="${ETC}/tokens.env"
ARCHIVO_CONTROL="${ETC}/control.token"
DIR_SSH="${ETC}/ssh"
CLAVE_SSH="${DIR_SSH}/tunel_ed25519"
ARCHIVO_TOKENS_API="${ARCHIVO_TOKENS_API:-/opt/centinela/api/modelos.env}"
PLANTILLA_ENV="${PLANTILLA_ENV:-${DIRECTORIO_SCRIPT}/../.env.example}"
USUARIO="vera-modelos"
SECO="no"
ROTAR="no"
PEDIR_CLAVE="no"
SIN_USUARIO="no"
CODIGO_SHA=""

TOKEN_CONTROL=""
TOKEN_VOZ=""
TOKEN_STT=""
CLAVE_RUNPOD=""

error() { printf 'generar-secretos: %s\n' "$1" >&2; exit "${2:-1}"; }
info() { printf 'generar-secretos: %s\n' "$1"; }

leer_argumentos() {
  while (( $# > 0 )); do
    case "$1" in
      --seco) SECO="si" ;;
      --rotar) ROTAR="si" ;;
      --clave-runpod) PEDIR_CLAVE="si" ;;
      --sin-usuario) SIN_USUARIO="si" ;;
      --codigo-sha) shift; CODIGO_SHA="${1:-}" ;;
      *) error "argumento no permitido: $1" 64 ;;
    esac
    shift
  done
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

# Un token: conserva el valor del archivo existente salvo --rotar.
decidir_token() { # decidir_token ARCHIVO CLAVE
  local existente
  existente="$(leer_valor "$1" "$2" || true)"
  if [[ -n "$existente" && "$ROTAR" != "si" ]]; then
    printf '%s' "$existente"
  elif [[ "$SECO" == "si" ]]; then
    printf '(se generaría con openssl rand -hex 32)'
  else
    token_nuevo
  fi
}

decidir_secretos() {
  local existente_control=""
  TOKEN_VOZ="$(decidir_token "$ARCHIVO_TOKENS" VERA_VOZ_TOKEN)"
  TOKEN_STT="$(decidir_token "$ARCHIVO_TOKENS" VERA_STT_TOKEN)"
  if [[ -f "$ARCHIVO_CONTROL" ]]; then existente_control="$(< "$ARCHIVO_CONTROL")"; fi
  if [[ -n "$existente_control" && "$ROTAR" != "si" ]]; then
    TOKEN_CONTROL="$existente_control"
  elif [[ "$SECO" == "si" ]]; then
    TOKEN_CONTROL="(se generaría con openssl rand -hex 32)"
  else
    TOKEN_CONTROL="$(token_nuevo)"
  fi
}

decidir_clave_runpod() {
  local existente
  existente="$(leer_valor "$ARCHIVO_ENV" RUNPOD_API_KEY || true)"
  if [[ -n "$existente" && "$existente" != rpa_REEMPLAZAR* && "$PEDIR_CLAVE" != "si" ]]; then
    CLAVE_RUNPOD="$existente"
    return 0
  fi
  if [[ "$SECO" == "si" ]]; then
    CLAVE_RUNPOD="(se pediría por la entrada estándar, sin eco)"
    return 0
  fi
  # Sin eco en una terminal y sin argv. También acepta un pipe: "... | sudo bash generar-secretos.sh".
  printf 'Pega la clave de la API de RunPod y pulsa Enter (no se muestra): ' >&2
  IFS= read -rs CLAVE_RUNPOD || true
  printf '\n' >&2
  [[ "$CLAVE_RUNPOD" =~ ^[A-Za-z0-9_-]{16,200}$ ]] || error "la clave de RunPod está vacía o tiene caracteres no permitidos" 65
}

# Reescribe el archivo de entorno línea por línea: reemplaza RUNPOD_API_KEY y MODELOS_REPO_SHA, deja lo demás.
construir_env() {
  local base linea clave salida=""
  if [[ -f "$ARCHIVO_ENV" ]]; then base="$ARCHIVO_ENV"; else base="$PLANTILLA_ENV"; fi
  while IFS= read -r linea || [[ -n "$linea" ]]; do
    clave="${linea%%=*}"
    if [[ "$clave" == "RUNPOD_API_KEY" ]]; then
      salida+="RUNPOD_API_KEY=${CLAVE_RUNPOD}"$'\n'
    elif [[ "$clave" == "MODELOS_REPO_SHA" && -n "$CODIGO_SHA" ]]; then
      salida+="MODELOS_REPO_SHA=${CODIGO_SHA}"$'\n'
    else
      salida+="${linea}"$'\n'
    fi
  done < "$base"
  printf '%s' "$salida"
}

# escribir_secreto DESTINO CONTENIDO [DUENO]: umask 077, mv atómico y modo 0600.
escribir_secreto() {
  local destino="$1" contenido="$2" dueno="${3:-}"
  ( umask 077 && printf '%s' "$contenido" > "${destino}.tmp" )
  chmod 0600 "${destino}.tmp"
  if [[ -n "$dueno" && "$SIN_USUARIO" != "si" ]]; then
    chown "$dueno" "${destino}.tmp"
  fi
  mv "${destino}.tmp" "$destino"
}

escribir_archivos() {
  local tokens
  tokens="VERA_STT_TOKEN=${TOKEN_STT}"$'\n'"VERA_VOZ_TOKEN=${TOKEN_VOZ}"$'\n'
  if [[ "$SECO" == "si" ]]; then
    info "[seco] escribiría ${ARCHIVO_ENV} (root 0600), ${ARCHIVO_TOKENS} y ${ARCHIVO_CONTROL} (${USUARIO} 0600) y ${ARCHIVO_TOKENS_API} (root 0600)"
    return 0
  fi
  escribir_secreto "$ARCHIVO_ENV" "$(construir_env)"$'\n'
  escribir_secreto "$ARCHIVO_TOKENS" "$tokens" "${USUARIO}:${USUARIO}"
  escribir_secreto "$ARCHIVO_CONTROL" "${TOKEN_CONTROL}"$'\n' "${USUARIO}:${USUARIO}"
  escribir_secreto "$ARCHIVO_TOKENS_API" "$tokens"
  info "escritos los cuatro archivos (todos 0600). Ningún secreto se imprimió"
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

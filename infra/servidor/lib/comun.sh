#!/usr/bin/env bash
# infra/servidor/lib/comun.sh
#
# Helpers de idempotencia para aprovisionar.sh (ver infra/servidor/README.md).
# Se carga con source, nunca se ejecuta como proceso aparte.

# Contador global de cambios aplicados durante la corrida actual.
_APROV_CAMBIOS=0

# Contador global de pasos que se saltaron por estar pendientes de implementar.
_APROV_PASOS_PENDIENTES=0

# Bandera interna: si "apt-get update" ya se ejecutó en esta corrida.
_APROV_APT_ACTUALIZADO="no"

paso() {
  printf '==> %s\n' "$1"
}

info() {
  printf '    %s\n' "$1"
}

aviso() {
  # A stdout, igual que paso() e info(); error_fatal() sí va a stderr.
  printf '[aviso] %s\n' "$1"
}

error_fatal() {
  printf '[error] %s\n' "$1" >&2
  exit 1
}

exigir_root() {
  if [[ "${EUID}" -ne 0 ]]; then
    error_fatal "Este script se debe ejecutar como root (o con sudo)."
  fi
}

exigir_variable() {
  local nombre="$1"
  if [[ -z "${!nombre:-}" ]]; then
    error_fatal "Falta definir la variable de entorno ${nombre}."
  fi
}

paquete_instalado() {
  local nombre="$1"
  dpkg -s "$nombre" &>/dev/null
}

instalar_paquetes() {
  local pendientes=()
  local paquete

  for paquete in "$@"; do
    if paquete_instalado "$paquete"; then
      info "El paquete ${paquete} ya está instalado."
    else
      pendientes+=("$paquete")
    fi
  done

  if [[ "${#pendientes[@]}" -eq 0 ]]; then
    return 0
  fi

  if [[ "${_APROV_APT_ACTUALIZADO}" != "si" ]]; then
    info "Actualizando el índice de paquetes (apt-get update)."
    DEBIAN_FRONTEND=noninteractive apt-get update -y
    _APROV_APT_ACTUALIZADO="si"
  fi

  info "Instalando: ${pendientes[*]}"
  DEBIAN_FRONTEND=noninteractive apt-get install -y "${pendientes[@]}"

  for paquete in "${pendientes[@]}"; do
    registrar_cambio
  done
}

asegurar_linea_en_archivo() {
  local archivo="$1"
  local linea="$2"

  if [[ ! -f "$archivo" ]]; then
    : > "$archivo"
  fi

  if grep -qxF "$linea" "$archivo"; then
    info "La línea ya está presente en ${archivo}."
    return 0
  fi

  printf '%s\n' "$linea" >> "$archivo"
  registrar_cambio
}

asegurar_directorio() {
  local ruta="$1"
  local modo="${2:-}"
  local propietario="${3:-}"
  local cambiado="no"

  if [[ ! -d "$ruta" ]]; then
    mkdir -p "$ruta"
    cambiado="si"
  fi

  if [[ -n "$modo" ]]; then
    local modo_actual
    modo_actual="$(stat -c '%a' "$ruta")"
    if [[ "$modo_actual" != "$modo" ]]; then
      chmod "$modo" "$ruta"
      cambiado="si"
    fi
  fi

  if [[ -n "$propietario" ]]; then
    local dueno_actual
    dueno_actual="$(stat -c '%U:%G' "$ruta")"
    if [[ "$dueno_actual" != "$propietario" ]]; then
      chown "$propietario" "$ruta"
      cambiado="si"
    fi
  fi

  if [[ "$cambiado" == "si" ]]; then
    registrar_cambio
  else
    info "El directorio ${ruta} ya estaba en el estado esperado."
  fi
}

asegurar_servicio_activo() {
  local servicio="$1"
  local habilitado="no"
  local activo="no"

  # No "command -v systemctl": el binario puede existir sin que systemd sea
  # el proceso 1; /run/systemd/system sí lo distingue.
  if [[ ! -d /run/systemd/system ]]; then
    aviso "systemd no es el proceso 1 en este sistema (no existe /run/systemd/system), se omite ${servicio}."
    return 0
  fi

  if systemctl is-enabled --quiet "$servicio" 2>/dev/null; then
    habilitado="si"
  fi

  if systemctl is-active --quiet "$servicio" 2>/dev/null; then
    activo="si"
  fi

  if [[ "$habilitado" == "si" && "$activo" == "si" ]]; then
    info "El servicio ${servicio} ya está habilitado y activo."
    return 0
  fi

  systemctl enable --now "$servicio"
  registrar_cambio
}

registrar_cambio() {
  _APROV_CAMBIOS=$((_APROV_CAMBIOS + 1))
}

resumen_cambios() {
  printf '%s\n' "${_APROV_CAMBIOS}"
}

# Para pasos sin implementar: marca el paso como pendiente sin fallar la corrida.
paso_pendiente() {
  local tarjeta="$1"
  aviso "Paso pendiente de implementar (tarjeta ${tarjeta}). No se aplicó ningún cambio."
  _APROV_PASOS_PENDIENTES=$((_APROV_PASOS_PENDIENTES + 1))
}

asegurar_archivo_con_contenido() {
  local ruta="$1"
  local contenido="$2"

  if [[ -f "$ruta" ]] && [[ "$(cat "$ruta")" == "$contenido" ]]; then
    info "El archivo ${ruta} ya tiene el contenido esperado."
    return 0
  fi

  asegurar_directorio "$(dirname "$ruta")"
  printf '%s\n' "$contenido" > "$ruta"
  registrar_cambio
}

asegurar_servicio_compose() {
  local archivo_compose="$1"
  local servicio="$2"

  # Mismo criterio que asegurar_servicio_activo: sin systemd como proceso 1
  # no hay daemon de Docker que alcanzar.
  if [[ ! -d /run/systemd/system ]]; then
    aviso "systemd no es el proceso 1 en este sistema (no existe /run/systemd/system), se omite el contenedor ${servicio}."
    return 0
  fi

  # Deriva el directorio de "$archivo_compose" y arma banderas -f con cada
  # compose*.yaml que encuentre ahí (ver infra/servidor/README.md).
  local directorio archivo
  local -a banderas_compose=()
  directorio="$(dirname "$archivo_compose")"
  for archivo in "${directorio}"/compose*.yaml; do
    [[ -f "$archivo" ]] || continue
    banderas_compose+=(-f "$archivo")
  done

  if [[ "${#banderas_compose[@]}" -eq 0 ]]; then
    error_fatal "No se encontró ningún archivo compose*.yaml en ${directorio}."
  fi

  # No comprueba antes si ya corre: "up -d" ya es idempotente, así que
  # compara ID y estado antes/después para medir si de verdad aplicó algo.
  local id_antes id_despues estado_antes
  id_antes="$(docker compose "${banderas_compose[@]}" ps -q "$servicio" 2>/dev/null || true)"
  estado_antes="inexistente"
  if [[ -n "$id_antes" ]]; then
    estado_antes="$(docker inspect -f '{{.State.Status}}' "$id_antes" 2>/dev/null || echo "inexistente")"
  fi

  docker compose "${banderas_compose[@]}" up -d "$servicio"

  id_despues="$(docker compose "${banderas_compose[@]}" ps -q "$servicio" 2>/dev/null || true)"

  if [[ -z "$id_despues" ]]; then
    error_fatal "docker compose up -d ${servicio} no dejó ningún contenedor corriendo."
  fi

  if [[ "$id_despues" != "$id_antes" || "$estado_antes" != "running" ]]; then
    info "El contenedor ${servicio} se creó o se recreó."
    registrar_cambio
  else
    info "El contenedor ${servicio} ya estaba corriendo con la configuración actual."
  fi
}

# Genera 32 caracteres alfanuméricos aptos para un archivo de entorno o una
# URL de conexión (ver infra/servidor/README.md).
generar_secreto() {
  local crudo

  crudo="$(head -c 128 /dev/urandom | base64 | tr -dc 'A-Za-z0-9')"

  if [[ "${#crudo}" -lt 32 ]]; then
    error_fatal "No se pudieron generar 32 caracteres alfanuméricos a partir de /dev/urandom."
  fi

  printf '%s' "${crudo:0:32}"
}

# Crea "$ruta" con "$nombre_variable=<secreto de 32 caracteres>" solo si no
# existe; no la reescribe si ya está (ver infra/servidor/README.md).
asegurar_archivo_con_secreto() {
  local ruta="$1"
  local nombre_variable="$2"
  local secreto

  if [[ -f "$ruta" ]]; then
    info "El archivo ${ruta} ya existe: no se regenera ${nombre_variable}."
    return 0
  fi

  # No se interpola "$(generar_secreto)" en el printf: un error ahí no
  # dispara "set -e". Se asigna a variable aparte; el chequeo de abajo es la
  # segunda red.
  secreto="$(generar_secreto)"
  if [[ "${#secreto}" -ne 32 ]]; then
    error_fatal "generar_secreto no devolvió una cadena de 32 caracteres para ${ruta}."
  fi

  asegurar_directorio "$(dirname "$ruta")" "700"
  printf '%s=%s\n' "$nombre_variable" "$secreto" > "$ruta"
  chmod 600 "$ruta"
  registrar_cambio
}

#!/usr/bin/env bash
# infra/servidor/lib/comun.sh
#
# Helpers de idempotencia para el orquestador de aprovisionamiento. Toda función
# que empieza con "asegurar_" o "instalar_" sigue la misma regla: solo llama a
# registrar_cambio cuando de verdad modificó el estado del sistema. Si el estado
# ya era el esperado, lo informa con info y no cuenta cambio. Esa es la prueba
# observable de idempotencia: ejecutar el script dos veces seguidas tiene que
# reportar 0 cambios en la segunda corrida.
#
# Este archivo se carga con source desde aprovisionar.sh, nunca se ejecuta como
# proceso aparte.

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
  # A stdout, igual que paso() e info(): así el log queda en orden cronológico
  # cuando la salida se redirige a un archivo. error_fatal() sí se queda en
  # stderr porque marca el corte definitivo de la corrida.
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

  # No se usa "command -v systemctl" para decidir si hay servicio que tocar:
  # ese binario puede quedar instalado como dependencia de otro paquete (por
  # ejemplo docker.io, que trae systemd y systemd-sysv) sin que systemd sea
  # de verdad el proceso 1, como pasa en el contenedor de
  # infra/servidor/probar-en-docker.sh. Ahí "systemctl enable --now" se
  # niega con "--now cannot be used when systemd is not running", lo que
  # aborta toda la corrida. /run/systemd/system solo existe cuando systemd
  # es el proceso 1: es la misma comprobación que usa sd_booted() de
  # libsystemd.
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

# Helper de apoyo para los pasos que todavía no están implementados (esqueleto).
# Deja constancia clara de que el paso no aplicó ningún cambio y lo cuenta como
# pendiente en el resumen final, sin hacer fallar la corrida.
paso_pendiente() {
  local tarjeta="$1"
  aviso "Paso pendiente de implementar (tarjeta ${tarjeta}). No se aplicó ningún cambio."
  _APROV_PASOS_PENDIENTES=$((_APROV_PASOS_PENDIENTES + 1))
}

#!/usr/bin/env bash
# Helpers genéricos para infra/azure/: presentación, verificación previa contra az,
# idempotencia (recurso_existe/ejecutar_cambio) y conteo de cambios. Se carga con source.

# Cambios aplicados (o que se aplicarían, bajo --simular) en la corrida actual.
_AZURE_CAMBIOS=0

# Acompañan al contador para que el resumen nombre los recursos, no solo el conteo.
RECURSOS_CREADOS=()
RECURSOS_EXISTENTES=()

paso() {
  printf '==> %s\n' "$1"
}

info() {
  printf '    %s\n' "$1"
}

aviso() {
  printf '[aviso] %s\n' "$1"
}

error_fatal() {
  printf '[error] %s\n' "$1" >&2
  exit 1
}

registrar_cambio() {
  _AZURE_CAMBIOS=$((_AZURE_CAMBIOS + 1))
}

resumen_cambios() {
  printf '%s\n' "${_AZURE_CAMBIOS}"
}

# Registra un recurso ya existente (llamado desde cada asegurar_* antes de su return 0 temprano).
registrar_existente() {
  info "$1 ya existe."
  RECURSOS_EXISTENTES+=("$1")
}

verificar_cli_az() {
  command -v az >/dev/null 2>&1 \
    || error_fatal "No se encontró el CLI de az en el PATH. Instálalo y ejecuta 'az login' antes de continuar."
}

# Exige confirmación interactiva antes de crear recursos: la suscripción equivocada es el
# error caro que un rerun idempotente no corrige. Requiere SIMULAR definida por el caller.
confirmar_suscripcion() {
  local salida respuesta
  local -a campos
  local nombre id

  salida="$(az account show --query "[name,id]" -o tsv --only-show-errors)" \
    || error_fatal "No se pudo consultar la suscripción activa de Azure. Verifica que 'az login' esté vigente."
  # tsv de az emite una línea por campo (no tabulado): mapfile llena cada elemento del arreglo.
  mapfile -t campos <<<"$salida"
  nombre="${campos[0]}"
  id="${campos[1]}"

  info "Suscripción activa: ${nombre} (${id})"

  if [[ "${SIMULAR:-no}" == "si" ]]; then
    info "Modo --simular: no se va a crear nada, se omite la confirmación interactiva."
    return 0
  fi

  if [[ "${CONFIRMAR_SUSCRIPCION:-}" == "si" ]]; then
    info "CONFIRMAR_SUSCRIPCION=si: se omite la confirmación interactiva."
    return 0
  fi

  [[ -t 0 ]] \
    || error_fatal "No hay terminal interactiva para confirmar la suscripción. Define CONFIRMAR_SUSCRIPCION=si si ya la verificaste por otro medio."

  read -r -p "¿Es esta la suscripción correcta para continuar? Escribe 's' o 'si' para continuar: " respuesta
  case "${respuesta,,}" in
    s | si | sí) ;;
    *) error_fatal "Confirmación no recibida: se detiene antes de crear ningún recurso." ;;
  esac
}

# Ejecuta "az $@" en modo lectura sin volcar salida; traduce el código a existe/no existe.
recurso_existe() {
  az "$@" -o none --only-show-errors >/dev/null 2>&1
}

# En --simular imprime el comando sin ejecutarlo y aun así cuenta el cambio (mismo número
# que una corrida real); fuera de --simular, corta con error_fatal nombrando el recurso.
ejecutar_cambio() {
  local descripcion="$1"
  shift

  if [[ "${SIMULAR:-no}" == "si" ]]; then
    info "[simular] Se ejecutaría: $*"
    registrar_cambio
    RECURSOS_CREADOS+=("$descripcion")
    return 0
  fi

  if ! "$@" -o none --only-show-errors; then
    error_fatal "No se pudo crear ${descripcion}."
  fi
  registrar_cambio
  RECURSOS_CREADOS+=("$descripcion")
}

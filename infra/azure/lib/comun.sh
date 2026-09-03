#!/usr/bin/env bash
# infra/azure/lib/comun.sh
#
# Helpers genéricos para los scripts de infra/azure/ (lado del operador, no
# del servidor): presentación (paso/info/aviso/error_fatal), verificación
# previa contra el CLI de az (verificar_cli_az/confirmar_suscripcion), el
# envoltorio de idempotencia para lecturas y escrituras contra Azure
# (recurso_existe/ejecutar_cambio) y el contador de cambios de la corrida
# actual (registrar_cambio/registrar_existente/resumen_cambios).
#
# Es el mismo patrón que infra/servidor/lib/comun.sh, pero para el otro lado
# de la frontera de despliegue: ese archivo asume que corre como root dentro
# del servidor real (exigir_root, dpkg, apt-get), y este asume que corre
# como el usuario normal del operador contra la API de Azure. No comparten
# archivo a propósito (ver la cabecera de infra/azure/crear-servidor.sh), y
# ahora tampoco comparten los nombres de sus variables globales: donde
# infra/servidor/lib/comun.sh usa _APROV_CAMBIOS, este usa _AZURE_CAMBIOS,
# para que quede claro de un vistazo a cuál de las dos corridas pertenece un
# número si algún día ambos logs terminan mezclados.
#
# Este archivo se carga con source desde cada script de infra/azure/ que lo
# necesite (crear-servidor.sh, automatizar-horario.sh), nunca se ejecuta como
# proceso aparte.

# Contador global de cambios aplicados (o que se aplicarían, bajo --simular)
# durante la corrida actual.
_AZURE_CAMBIOS=0

# Los dos arreglos de abajo acompañan al contador para que el resumen final
# pueda nombrar los recursos, no solo decir cuántos: "3 cambios" no le dice a
# un humano si el tercero fue la VM o una regla del NSG.
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

# Registra un recurso que ya existía (se llama desde cada asegurar_* antes
# de su "return 0" temprano), para que el resumen final liste tanto lo
# creado como lo que ya estaba, no solo el conteo de cambios.
registrar_existente() {
  info "$1 ya existe."
  RECURSOS_EXISTENTES+=("$1")
}

verificar_cli_az() {
  command -v az >/dev/null 2>&1 \
    || error_fatal "No se encontró el CLI de az en el PATH. Instálalo y ejecuta 'az login' antes de continuar."
}

# Imprime la suscripción activa y exige confirmación interactiva antes de
# seguir: crear recursos en la suscripción equivocada es el error caro de
# estos scripts, y a diferencia de un recurso mal nombrado no se corrige con
# un rerun idempotente. Espera que el script que llama haya definido SIMULAR
# ("si"/"no") antes de invocar esta función.
confirmar_suscripcion() {
  local salida respuesta
  local -a campos
  local nombre id

  salida="$(az account show --query "[name,id]" -o tsv --only-show-errors)" \
    || error_fatal "No se pudo consultar la suscripción activa de Azure. Verifica que 'az login' esté vigente."
  # El formato tsv de az emite una línea por campo, no campos separados por
  # tabulador en una sola línea, así que mapfile lee cada línea en un elemento
  # del arreglo en vez de dejar vacíos los campos posteriores al primero.
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

# Ejecuta "az $@" en modo lectura y traduce el código de salida a existe/no
# existe, sin volcar nada a stdout ni stderr: cada asegurar_* solo tiene que
# ramificar por el resultado.
recurso_existe() {
  az "$@" -o none --only-show-errors >/dev/null 2>&1
}

# Envoltorio único de escritura: en --simular imprime el comando en vez de
# ejecutarlo, y de cualquier forma cuenta el cambio, para que el resumen
# final reporte "cambios que se aplicarían" con el mismo número que
# aplicaría una corrida real. Fuera de --simular, revisa el código de salida
# y corta con error_fatal nombrando el recurso, no el volcado crudo del CLI.
# Espera que el script que llama haya definido SIMULAR ("si"/"no").
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

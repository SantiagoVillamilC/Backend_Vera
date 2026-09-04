#!/usr/bin/env bash
# infra/servidor/aprovisionar.sh
#
# Orquestador del aprovisionamiento: recorre infra/servidor/pasos/ en orden
# numérico y hace source de cada archivo (ver infra/servidor/README.md).
set -euo pipefail

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIRECTORIO_PASOS="${DIRECTORIO_SCRIPT}/pasos"

# shellcheck source=lib/comun.sh
source "${DIRECTORIO_SCRIPT}/lib/comun.sh"

# shellcheck source=imagen-fijada.sh
source "${DIRECTORIO_SCRIPT}/imagen-fijada.sh"

# shellcheck source=red-fijada.sh
source "${DIRECTORIO_SCRIPT}/red-fijada.sh"

# shellcheck source=dominio-fijado.sh
source "${DIRECTORIO_SCRIPT}/dominio-fijado.sh"

# Configuración local opcional, no se versiona (ver infra/servidor/.gitignore).
ARCHIVO_ENV="${DIRECTORIO_SCRIPT}/aprovisionar.env"
if [[ -f "${ARCHIVO_ENV}" ]]; then
  # shellcheck source=/dev/null
  source "${ARCHIVO_ENV}"
fi

_APROV_TOTAL_PASOS=0
# Paso 00, el gate de entrada.
ARCHIVO_GATE="00-verificar-sistema.sh"

mostrar_ayuda() {
  cat <<'EOF'
Uso: aprovisionar.sh [opción]

Opciones:
  --lista           Muestra los pasos disponibles con su tarjeta, sin ejecutar nada.
  --solo <archivo>  Ejecuta el gate (00-verificar-sistema.sh) y luego un único
                    paso, por ejemplo: --solo 20-docker.sh. Si <archivo> ya es
                    el gate, se ejecuta una sola vez.
  --ayuda           Muestra esta ayuda.

Sin opciones, ejecuta todos los pasos de pasos/ en orden numérico.
EOF
}

# Lee NOMBRE_PASO y TARJETA sin ejecutar el cuerpo del paso, para que
# --lista sea siempre de solo lectura.
mostrar_lista() {
  paso "Pasos disponibles"

  local archivo nombre tarjeta
  for archivo in "${DIRECTORIO_PASOS}"/[0-9][0-9]-*.sh; do
    [[ -f "$archivo" ]] || continue
    nombre="$(grep -m1 '^NOMBRE_PASO=' "$archivo" | cut -d'"' -f2)"
    tarjeta="$(grep -m1 '^TARJETA=' "$archivo" | cut -d'"' -f2)"
    printf '    %-18s %-48s %s\n' "$(basename "$archivo")" "$nombre" "$tarjeta"
  done
}

# Limpia NOMBRE_PASO y TARJETA antes del source: comparten el mismo shell, y
# un paso que las olvide heredaría las del paso anterior.
ejecutar_paso() {
  local archivo="$1"

  unset NOMBRE_PASO TARJETA
  # shellcheck source=/dev/null
  source "$archivo"

  if [[ -z "${NOMBRE_PASO:-}" || -z "${TARJETA:-}" ]]; then
    aviso "El paso $(basename "$archivo") no declaró NOMBRE_PASO o TARJETA."
  fi

  _APROV_TOTAL_PASOS=$((_APROV_TOTAL_PASOS + 1))
}

imprimir_resumen() {
  local duracion="$1"
  local cambios
  local pasos_aplicados
  cambios="$(resumen_cambios)"
  # Pasos aplicados = total ejecutados menos los pendientes por implementar.
  pasos_aplicados=$((_APROV_TOTAL_PASOS - _APROV_PASOS_PENDIENTES))

  paso "Resumen de aprovisionamiento"
  info "Pasos aplicados: ${pasos_aplicados}"
  info "Pasos pendientes: ${_APROV_PASOS_PENDIENTES}"
  info "Pasos totales: ${_APROV_TOTAL_PASOS}"
  info "Cambios aplicados: ${cambios}"
  info "Duración: ${duracion}s"
  # Línea adicional pensada para que probar-en-docker.sh la lea sin ambigüedad.
  printf 'CAMBIOS_APLICADOS=%s\n' "${cambios}"
}

# Recorre todos los pasos (o uno solo si modo es "unico") e imprime el
# resumen; solo falla si un paso implementado revienta de verdad.
ejecutar_todo() {
  local modo="$1"
  local archivo_unico="$2"
  local inicio fin duracion

  inicio="$(date +%s)"

  if [[ "$modo" == "unico" ]]; then
    local ruta="${DIRECTORIO_PASOS}/${archivo_unico}"
    [[ -f "$ruta" ]] || error_fatal "No existe el paso ${archivo_unico} en ${DIRECTORIO_PASOS}."
    # El gate corre primero, salvo que el paso pedido sea el propio gate: así
    # --solo nunca se lo salta, y tampoco se ejecuta dos veces.
    if [[ "$archivo_unico" != "$ARCHIVO_GATE" ]]; then
      ejecutar_paso "${DIRECTORIO_PASOS}/${ARCHIVO_GATE}"
    fi
    ejecutar_paso "$ruta"
  else
    local archivo
    for archivo in "${DIRECTORIO_PASOS}"/[0-9][0-9]-*.sh; do
      [[ -f "$archivo" ]] || continue
      ejecutar_paso "$archivo"
    done
  fi

  fin="$(date +%s)"
  duracion=$((fin - inicio))
  imprimir_resumen "$duracion"
}

principal() {
  local opcion="${1:-}"

  if [[ "$opcion" == "--ayuda" || "$opcion" == "-h" ]]; then
    mostrar_ayuda
    return 0
  fi

  exigir_root

  case "$opcion" in
    --lista)
      mostrar_lista
      ;;
    --solo)
      local archivo_unico="${2:-}"
      [[ -n "$archivo_unico" ]] || error_fatal "Falta indicar el archivo del paso. Uso: --solo <archivo>"
      ejecutar_todo "unico" "$archivo_unico"
      ;;
    "")
      ejecutar_todo "todos" ""
      ;;
    *)
      error_fatal "Opción desconocida: ${opcion}. Usa --ayuda para ver las opciones disponibles."
      ;;
  esac
}

principal "$@"

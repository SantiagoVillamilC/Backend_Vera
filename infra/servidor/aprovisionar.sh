#!/usr/bin/env bash
# infra/servidor/aprovisionar.sh
#
# Orquestador del aprovisionamiento del servidor. Recorre infra/servidor/pasos/
# en orden numérico y hace source de cada archivo, para que todos compartan el
# contador de cambios y los helpers de infra/servidor/lib/comun.sh.
set -euo pipefail

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIRECTORIO_PASOS="${DIRECTORIO_SCRIPT}/pasos"

# shellcheck source=lib/comun.sh
source "${DIRECTORIO_SCRIPT}/lib/comun.sh"

# Única fuente de verdad del sistema operativo y la imagen fijados. Se carga
# aquí, no dentro del gate, para que todos los pasos (no solo
# 00-verificar-sistema.sh) puedan usar sus variables.
# shellcheck source=imagen-fijada.sh
source "${DIRECTORIO_SCRIPT}/imagen-fijada.sh"

# Única fuente de verdad del puerto SSH fijado. Se carga aquí, justo después
# de imagen-fijada.sh y por la misma razón: no dentro de 10-ssh.sh, para que
# cualquier paso futuro (no solo el de SSH) pueda usar PUERTO_SSH sin tener
# que repetir el source.
# shellcheck source=red-fijada.sh
source "${DIRECTORIO_SCRIPT}/red-fijada.sh"

# Única fuente de verdad del dominio del servidor. Se carga aquí, junto con
# imagen-fijada.sh y red-fijada.sh, por la misma razón que esos dos: no
# dentro de 30-nginx.sh, para que cualquier paso futuro (no solo el de
# nginx, también 50-certbot.sh a futuro) pueda usar DOMINIO_SERVIDOR sin
# tener que repetir el source.
# shellcheck source=dominio-fijado.sh
source "${DIRECTORIO_SCRIPT}/dominio-fijado.sh"

# Configuración local opcional, no se versiona (ver infra/servidor/.gitignore).
ARCHIVO_ENV="${DIRECTORIO_SCRIPT}/aprovisionar.env"
if [[ -f "${ARCHIVO_ENV}" ]]; then
  # shellcheck source=/dev/null
  source "${ARCHIVO_ENV}"
fi

_APROV_TOTAL_PASOS=0
# Archivo del gate de entrada (infra/servidor/pasos/00-verificar-sistema.sh).
# --solo lo ejecuta primero cuando el paso pedido es otro, para que ninguna
# corrida dirigida se salte la verificación de sistema por accidente.
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

# Lee NOMBRE_PASO y TARJETA de cada archivo de paso sin ejecutar su cuerpo, para
# que --lista sea siempre de solo lectura aunque un paso futuro ya esté
# implementado de verdad.
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

# Hace source de un archivo de paso. Se limpian NOMBRE_PASO y TARJETA antes,
# porque al compartir el mismo shell un paso que olvide declararlas heredaría
# por error los valores del paso anterior.
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
  # _APROV_TOTAL_PASOS cuenta todo paso que se ejecutó, esté implementado o
  # pendiente. Los pasos aplicados son los que sí están implementados, es
  # decir el total menos los que se saltaron por estar pendientes.
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

# Recorre todos los pasos, o uno solo si modo es "unico", y al final imprime el
# resumen. Devuelve 0 aunque queden pasos pendientes: solo falla si un paso
# implementado revienta de verdad.
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

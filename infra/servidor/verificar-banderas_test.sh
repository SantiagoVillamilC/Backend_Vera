#!/usr/bin/env bash
# infra/servidor/verificar-banderas_test.sh
#
# Prueba en seco de verificar-banderas.sh: inyecta el entorno del contenedor con
# ENTORNO_CONTENEDOR y simula el estado con ESTADO_CONTENEDOR, así que no
# necesita docker. Cubre: coincide, falta una, sobra una y error.
set -euo pipefail

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# shellcheck source=lib/comun.sh
source "${DIRECTORIO_SCRIPT}/lib/comun.sh"

SCRIPT="${DIRECTORIO_SCRIPT}/verificar-banderas.sh"
ARCHIVO_BANDERAS="${DIRECTORIO_SCRIPT}/banderas.tsv"

esperadas="$(banderas_del_destino "${ARCHIVO_BANDERAS}" local)"
con_sistema="${esperadas}
PATH=/usr/bin
HOME=/root
HOSTNAME=centinela-api"

probar() {
  local etiqueta="$1"
  local codigo_esperado="$2"
  local patron="$3"
  local entorno="$4"
  local estado="${5:-running}"
  local salida codigo

  if salida="$(ENTORNO_CONTENEDOR="${entorno}" ESTADO_CONTENEDOR="${estado}" bash "${SCRIPT}" local 2>&1)"; then
    codigo=0
  else
    codigo=$?
  fi

  if [[ "${codigo}" -ne "${codigo_esperado}" ]]; then
    printf '[fallo] %s: código %s, esperaba %s\n%s\n' \
      "${etiqueta}" "${codigo}" "${codigo_esperado}" "${salida}" >&2
    exit 1
  fi

  if [[ -n "${patron}" ]] && ! printf '%s\n' "${salida}" | grep -q "${patron}"; then
    printf '[fallo] %s: la salida no nombra "%s"\n%s\n' \
      "${etiqueta}" "${patron}" "${salida}" >&2
    exit 1
  fi

  printf 'ok %s (código %s)\n' "${etiqueta}" "${codigo}"
}

# Coincide: todas las banderas del destino y las variables del sistema de más.
probar "coincide" 0 "banderas coinciden" "${con_sistema}"

# Falta una: la matriz pide CLASIFICADOR y el contenedor no lo trae.
sin_clasificador="$(printf '%s\n' "${con_sistema}" | grep -v '^CLASIFICADOR=')"
probar "falta una" 2 "DIFF CLASIFICADOR: falta" "${sin_clasificador}"

# Sobra una: una bandera vieja que la matriz no declara, con la misma familia.
con_sobrante="${con_sistema}
AVISOS_CUPO_VIEJO=9"
probar "sobra una" 2 "DIFF AVISOS_CUPO_VIEJO: sobra" "${con_sobrante}"

# Error: el contenedor no está corriendo y no se puede verificar.
probar "contenedor detenido" 3 "no hay API en marcha" "${con_sistema}" "exited"

# Error: destino inválido, sin llegar a comparar.
if salida="$(bash "${SCRIPT}" roto 2>&1)"; then
  codigo=0
else
  codigo=$?
fi
if [[ "${codigo}" -ne 3 ]]; then
  printf '[fallo] destino inválido: código %s, esperaba 3\n%s\n' "${codigo}" "${salida}" >&2
  exit 1
fi
printf 'ok destino inválido (código 3)\n'

printf 'verificación de banderas en seco: todos los casos pasan\n'

#!/usr/bin/env bash
# infra/servidor/verificar-banderas.sh
#
# Compara las variables del contenedor centinela-api contra la columna del
# destino en infra/servidor/banderas.tsv (CF-A0-02). La matriz es la fuente de
# verdad: 60-api.sh emite esas banderas en el compose y este script comprueba
# que el contenedor arrancado las tenga de verdad. Ver
# docs/configuracion-entornos.md.
#
# Uso: verificar-banderas.sh [local|demo|produccion]
# Sin argumento toma DESTINO del entorno o de aprovisionar.env.
#
# Códigos de salida: 0 coincide, 2 difiere, 3 no se pudo verificar.
set -euo pipefail

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# shellcheck source=lib/comun.sh
source "${DIRECTORIO_SCRIPT}/lib/comun.sh"

ARCHIVO_BANDERAS="${DIRECTORIO_SCRIPT}/banderas.tsv"
ARCHIVO_ENV_APROVISIONAR="${DIRECTORIO_SCRIPT}/aprovisionar.env"
CONTENEDOR="${CONTENEDOR_API:-centinela-api}"

imposible() {
  printf '[error] %s\n' "$1" >&2
  exit 3
}

mostrar_ayuda() {
  cat <<'EOF'
Uso: verificar-banderas.sh [local|demo|produccion]

Compara las banderas del contenedor centinela-api contra la columna del destino
en infra/servidor/banderas.tsv.

Sin argumento toma DESTINO de una de estas fuentes, en orden: la variable de
entorno DESTINO, el archivo aprovisionar.env junto a este script.

Devuelve 0 si el contenedor coincide, 2 si alguna bandera difiere o falta, y 3
si no se pudo verificar (sin destino, sin docker, contenedor ausente o
contenedor que no está en estado "running").
EOF
}

case "${1:-}" in
  -h | --ayuda | --help)
    mostrar_ayuda
    exit 0
    ;;
esac

if [[ "$#" -gt 1 ]]; then
  mostrar_ayuda >&2
  exit 3
fi

destino="${1:-${DESTINO:-}}"
if [[ -z "${destino}" ]]; then
  destino="$(destino_de_aprovisionar "${ARCHIVO_ENV_APROVISIONAR}")"
fi
if [[ -z "${destino}" ]]; then
  imposible "Falta el destino: pásalo como argumento, exporta DESTINO o declara DESTINO en ${ARCHIVO_ENV_APROVISIONAR}."
fi

case "${destino}" in
  local | demo | produccion) ;;
  *) imposible "DESTINO debe ser local, demo o produccion, no \"${destino}\"." ;;
esac

if [[ ! -f "${ARCHIVO_BANDERAS}" ]]; then
  imposible "No existe la matriz de banderas ${ARCHIVO_BANDERAS}."
fi

if ! command -v docker >/dev/null 2>&1; then
  imposible "No hay docker en este sistema: la comparación se hace contra el contenedor ${CONTENEDOR}."
fi

# Un contenedor detenido o en crash loop conserva .Config.Env intacto, así que
# inspeccionarlo sin mirar .State.Status devolvería 0 sin que haya API
# corriendo. El contrato del script es 3 cuando no se puede verificar.
estado_contenedor="$(docker inspect -f '{{.State.Status}}' "${CONTENEDOR}" 2>&1)" ||
  imposible "No se pudo inspeccionar el contenedor ${CONTENEDOR}: ${estado_contenedor}"
if [[ "${estado_contenedor}" != "running" ]]; then
  imposible "El contenedor ${CONTENEDOR} está en estado \"${estado_contenedor}\", no \"running\": no hay API en marcha a la que comparar las banderas. Arráncalo con infra/servidor/pasos/60-api.sh y vuelve a correr esta verificación."
fi

# {{println .}} separa cada variable en una línea "CLAVE=valor". Un env_file o
# el propio compose pueden haber dejado más variables; solo se miran las de la
# matriz.
entorno_contenedor="$(docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "${CONTENEDOR}" 2>&1)" ||
  imposible "No se pudo inspeccionar el contenedor ${CONTENEDOR}: ${entorno_contenedor}"

paso "Banderas del destino ${destino} contra ${CONTENEDOR}"

diferencias=0
revisadas=0
while IFS= read -r linea; do
  [[ -n "${linea}" ]] || continue
  variable="${linea%%=*}"
  esperado="${linea#*=}"
  revisadas=$((revisadas + 1))

  if printf '%s\n' "${entorno_contenedor}" | grep -qxF "${variable}=${esperado}"; then
    info "${variable}=${esperado}"
    continue
  fi

  if printf '%s\n' "${entorno_contenedor}" | grep -q "^${variable}="; then
    actual="$(printf '%s\n' "${entorno_contenedor}" | grep -m1 "^${variable}=")"
    printf '    DIFF %s: el contenedor tiene "%s", la matriz pide "%s"\n' \
      "${variable}" "${actual#*=}" "${esperado}"
  else
    printf '    DIFF %s: falta en el contenedor, la matriz pide "%s"\n' "${variable}" "${esperado}"
  fi
  diferencias=$((diferencias + 1))
done <<< "$(banderas_del_destino "${ARCHIVO_BANDERAS}" "${destino}")"

if [[ "${revisadas}" -eq 0 ]]; then
  imposible "${ARCHIVO_BANDERAS} no tiene banderas para DESTINO=${destino}."
fi

if [[ "${diferencias}" -ne 0 ]]; then
  printf '[error] %s de %s banderas no coinciden con DESTINO=%s. Vuelve a correr infra/servidor/pasos/60-api.sh para recrear el contenedor.\n' \
    "${diferencias}" "${revisadas}" "${destino}" >&2
  exit 2
fi

info "${revisadas} banderas coinciden con DESTINO=${destino}."
exit 0

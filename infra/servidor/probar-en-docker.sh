#!/usr/bin/env bash
# infra/servidor/probar-en-docker.sh
# Prueba de idempotencia en un contenedor Docker efímero. Ver infra/servidor/README.md.
set -euo pipefail

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NOMBRE_CONTENEDOR="centinela-prueba-aprovisionamiento-$$"

limpiar() {
  docker rm -f "${NOMBRE_CONTENEDOR}" &>/dev/null || true
}
trap limpiar EXIT

echo "==> Levantando contenedor debian:13 (${NOMBRE_CONTENEDOR})"
docker run -d --name "${NOMBRE_CONTENEDOR}" \
  -v "${DIRECTORIO_SCRIPT}:/infra-servidor:ro" \
  debian:13 sleep infinity >/dev/null

# Se copia a una ruta propia: el montaje original es de solo lectura.
echo "==> Primera corrida"
docker exec "${NOMBRE_CONTENEDOR}" bash -c '
  set -euo pipefail
  cp -r /infra-servidor /root/infra-servidor
  bash /root/infra-servidor/aprovisionar.sh
'

echo "==> Segunda corrida"
salida_segunda="$(docker exec "${NOMBRE_CONTENEDOR}" bash /root/infra-servidor/aprovisionar.sh)"

echo "${salida_segunda}"

cambios_segunda="$(printf '%s\n' "${salida_segunda}" | grep '^CAMBIOS_APLICADOS=' | cut -d'=' -f2)"

if [[ -z "${cambios_segunda}" ]]; then
  echo "[error] No se pudo leer CAMBIOS_APLICADOS en la salida de la segunda corrida." >&2
  exit 1
fi

if [[ "${cambios_segunda}" -ne 0 ]]; then
  echo "[error] La segunda corrida reportó ${cambios_segunda} cambios, se esperaban 0." >&2
  exit 1
fi

echo "==> Prueba de idempotencia superada: la segunda corrida reportó 0 cambios."

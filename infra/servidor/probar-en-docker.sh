#!/usr/bin/env bash
# infra/servidor/probar-en-docker.sh
#
# Prueba de idempotencia sin gastar crédito de Azure y sin depender de Vagrant
# (Vagrant no está instalado en esta máquina). Levanta un único contenedor
# debian:13 (Debian 13 trixie, la misma versión de la imagen de Azure y de la
# caja de Vagrant; ver infra/azure/variables.sh), monta infra/servidor/ dentro
# y ejecuta aprovisionar.sh dos veces seguidas en ese mismo contenedor. La
# segunda corrida tiene que reportar 0 cambios aplicados.
#
# El contenedor no tiene systemd, así que asegurar_servicio_activo se limita a
# avisar y seguir. Los pasos que de verdad dependan de systemd (nginx, postgres,
# certbot, cuando se implementen) se deben verificar aparte en la VM real
# (Azure o Vagrant), no en esta prueba.
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

# Se copia a una ruta propia del contenedor porque el montaje es de solo
# lectura: así ningún paso futuro puede modificar por accidente los archivos
# del repositorio en el equipo anfitrión.
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

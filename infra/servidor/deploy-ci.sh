#!/usr/bin/env bash
# infra/servidor/deploy-ci.sh
# Comando forzado de la llave de deploy de CI (SCRUM-100, SCRUM-114).
#
# cd.yml manda el nombre de la rama como comando SSH y sshd lo deja en
# SSH_ORIGINAL_COMMAND. Solo se aceptan las tres ramas que publican imagen:
# cualquier otro valor aborta antes de tocar nada, y como el comando es
# forzado, la llave no sirve para ejecutar nada más en la máquina.
#
# staging y main comparten esta misma VM: la rama desplegada es únicamente la
# etiqueta de la imagen, que este script reescribe en los dos .env.
set -euo pipefail

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REGISTRO="ghcr.io/santiagovillamilc/backend_vera"
DIRECTORIO_SIMULADORES="/opt/centinela/simuladores"

rama="${SSH_ORIGINAL_COMMAND:-}"
case "${rama}" in
  dev | staging | main) ;;
  *)
    printf 'deploy-ci: rama no permitida ("%s"); se acepta dev, staging o main.\n' "${rama}" >&2
    exit 64
    ;;
esac

# Reescribe VAR=valor sin tocar el resto del archivo. Los .env los generan
# 60-api.sh y 65-simuladores.sh; si no existen, el paso correspondiente no ha
# corrido y no hay nada que desplegar.
fijar_variable() {
  local archivo="$1" variable="$2" valor="$3"

  if [[ ! -f "${archivo}" ]]; then
    return 0
  fi

  if grep -q "^${variable}=" "${archivo}"; then
    if [[ "$(grep -m1 "^${variable}=" "${archivo}")" != "${variable}=${valor}" ]]; then
      sed -i "s|^${variable}=.*|${variable}=${valor}|" "${archivo}"
    fi
  else
    printf '%s=%s\n' "${variable}" "${valor}" >>"${archivo}"
  fi
}

fijar_variable "${DIRECTORIO_SCRIPT}/.env" IMAGEN_API "${REGISTRO}:${rama}"
fijar_variable "${DIRECTORIO_SIMULADORES}/.env" TAG "${rama}"

declare -a banderas_compose=()
for archivo in "${DIRECTORIO_SCRIPT}"/compose*.yaml; do
  [[ -f "${archivo}" ]] || continue
  banderas_compose+=(-f "${archivo}")
done

if [[ "${#banderas_compose[@]}" -eq 0 ]]; then
  printf 'deploy-ci: no hay compose*.yaml en %s.\n' "${DIRECTORIO_SCRIPT}" >&2
  exit 1
fi

cd "${DIRECTORIO_SCRIPT}"

esperar_sano() {
  local contenedor="$1" intentos=0

  while [[ "${intentos}" -lt 45 ]]; do
    if [[ "$(sudo docker inspect -f '{{.State.Health.Status}}' "${contenedor}" 2>/dev/null)" == "healthy" ]]; then
      printf 'deploy-ci: %s healthy.\n' "${contenedor}"
      return 0
    fi
    intentos=$((intentos + 1))
    sleep 2
  done

  printf 'deploy-ci: %s no quedó healthy en 90 segundos.\n' "${contenedor}" >&2
  return 1
}

printf 'deploy-ci: desplegando la rama %s (%s).\n' "${rama}" "${REGISTRO}:${rama}"

sudo docker compose "${banderas_compose[@]}" pull migrate api

# Las migraciones corren como servicio de un solo uso antes de que la API
# nueva arranque: "up -d api" reusa el contenedor migrate ya terminado y no
# volvería a aplicarlas. Si fallan, el "set -e" corta aquí y la API vigente
# sigue sirviendo.
sudo docker compose "${banderas_compose[@]}" run --rm migrate

sudo docker compose "${banderas_compose[@]}" up -d api
esperar_sano centinela-api

if [[ -d "${DIRECTORIO_SIMULADORES}" ]]; then
  # El directorio es 700 porque su .env lleva CUSTODIO_TOKEN: centinela no
  # puede entrar, así que todo el bloque corre bajo sudo.
  sudo bash -c "cd ${DIRECTORIO_SIMULADORES} && docker compose pull && docker compose up -d"
  for contenedor in vera-simulador-riel vera-simulador-custodia vera-simulador-pse; do
    esperar_sano "${contenedor}"
  done
fi

printf 'deploy-ci: rama %s desplegada y healthy.\n' "${rama}"

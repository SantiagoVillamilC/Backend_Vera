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
#
# R-05: también acepta un digest de imagen y un tag de modelo. Los valida antes
# de tocar nada, guarda una copia fechada del entorno anterior y tiene vuelta
# atrás. Un digest inexistente aborta sin cambiar el entorno vigente.
#
# Uso:
#   deploy-ci.sh [dev|staging|main] [--digest sha256:...] [--modelo TAG] [--rollback] [--simular]
# Variables: IMAGEN_DIGEST, MODELO_TAG, DEPLOY_SIMULAR=si, DEPLOY_DIRECTORIO,
#            DEPLOY_LOCK_MODELOS, DEPLOY_SMOKE_URL, DOCKER.
set -euo pipefail

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REGISTRO="ghcr.io/santiagovillamilc/backend_vera"
DEPLOY_DIRECTORIO="${DEPLOY_DIRECTORIO:-${DIRECTORIO_SCRIPT}}"
DIRECTORIO_SIMULADORES="${DEPLOY_DIRECTORIO}/simuladores"
RESPALDOS="${DEPLOY_DIRECTORIO}/respaldos"
ARCHIVO_LOCK_MODELOS="${DEPLOY_LOCK_MODELOS:-${DIRECTORIO_SCRIPT}/../../modelos/lock.json}"
DEPLOY_SIMULAR="${DEPLOY_SIMULAR:-no}"
DEPLOY_SMOKE_URL="${DEPLOY_SMOKE_URL:-http://127.0.0.1:8080/readyz}"
DOCKER="${DOCKER:-docker}"
rama=""
digest="${IMAGEN_DIGEST:-}"
modelo="${MODELO_TAG:-}"
rollback="no"

error_deploy() {
  printf 'deploy-ci: %s\n' "$1" >&2
  exit "${2:-1}"
}

while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --digest)
      shift
      [[ "$#" -gt 0 ]] || error_deploy "falta el valor de --digest" 64
      digest="$1"
      ;;
    --modelo)
      shift
      [[ "$#" -gt 0 ]] || error_deploy "falta el valor de --modelo" 64
      modelo="$1"
      ;;
    --rollback)
      rollback="si"
      ;;
    --simular)
      DEPLOY_SIMULAR="si"
      ;;
    dev | staging | main)
      [[ -z "${rama}" ]] || error_deploy "rama repetida" 64
      rama="$1"
      ;;
    *)
      error_deploy "argumento no permitido (\"$1\"); usa dev, staging o main, --digest, --modelo, --rollback o --simular" 64
      ;;
  esac
  shift
done

if [[ "${rollback}" != "si" && -z "${rama}" ]]; then
  rama="${SSH_ORIGINAL_COMMAND:-}"
fi
if [[ "${rollback}" != "si" ]]; then
  case "${rama}" in
    dev | staging | main) ;;
    *) error_deploy "rama no permitida (\"${rama}\"); se acepta dev, staging o main" 64 ;;
  esac
fi

if [[ "${digest}" != "" ]]; then
  if [[ ! "${digest}" =~ ^sha256:[0-9a-f]{64}$ ]]; then
    error_deploy "digest inválido: \"${digest}\"; se espera sha256:<64 hex>" 65
  fi
  if [[ "${DEPLOY_SIMULAR}" != "si" ]]; then
    if ! "${DOCKER}" manifest inspect "${REGISTRO}@${digest}" >/dev/null 2>&1; then
      error_deploy "el digest ${digest} no existe en ${REGISTRO}; no se toca el entorno vigente" 66
    fi
  fi
fi

if [[ "${modelo}" != "" ]]; then
  if [[ ! -f "${ARCHIVO_LOCK_MODELOS}" ]]; then
    error_deploy "no encuentro el lockfile de modelos ${ARCHIVO_LOCK_MODELOS}" 67
  fi
  if ! python3 - "${ARCHIVO_LOCK_MODELOS}" "${modelo}" <<'PY'
import json
import sys

datos = json.load(open(sys.argv[1], encoding="utf-8"))
nombres = {artefacto.get("nombre") for artefacto in datos.get("artefactos", [])}
sys.exit(0 if sys.argv[2] in nombres else 1)
PY
  then
    error_deploy "el tag de modelo \"${modelo}\" no está en ${ARCHIVO_LOCK_MODELOS}" 68
  fi
fi

if [[ "${EUID}" -ne 0 && "${DEPLOY_SIMULAR}" != "si" ]]; then
  # SSH_ORIGINAL_COMMAND no sobrevive a sudo (env_reset), así que la rama ya
  # validada viaja como argumento y el script vuelve a validarla arriba.
  argumentos=("${BASH_SOURCE[0]}")
  [[ -n "${rama}" ]] && argumentos+=("${rama}")
  [[ -n "${digest}" ]] && argumentos+=(--digest "${digest}")
  [[ -n "${modelo}" ]] && argumentos+=(--modelo "${modelo}")
  [[ "${rollback}" == "si" ]] && argumentos+=(--rollback)
  exec sudo -- "${argumentos[@]}"
fi

# Reescribe VAR=valor sin tocar el resto del archivo.
fijar_variable() {
  local archivo="$1" variable="$2" valor="$3"

  if [[ ! -f "${archivo}" ]]; then
    printf 'deploy-ci: no existe %s; el paso que lo genera no ha corrido.\n' "${archivo}" >&2
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

# Guarda una copia fechada de los dos .env antes de reescribirlos y devuelve la ruta.
respaldar_entorno() {
  local marca destino
  marca="$(date -u +%Y-%m-%dT%H-%M-%SZ)"
  destino="${RESPALDOS}/${marca}"
  mkdir -p "${destino}"
  if [[ -f "${DEPLOY_DIRECTORIO}/.env" ]]; then
    cp "${DEPLOY_DIRECTORIO}/.env" "${destino}/api.env"
  fi
  if [[ -f "${DIRECTORIO_SIMULADORES}/.env" ]]; then
    cp "${DIRECTORIO_SIMULADORES}/.env" "${destino}/simuladores.env"
  fi
  printf '%s\n' "${destino}"
}

restaurar_entorno() {
  local respaldo="$1"
  [[ -f "${respaldo}/api.env" ]] && cp "${respaldo}/api.env" "${DEPLOY_DIRECTORIO}/.env"
  if [[ -f "${respaldo}/simuladores.env" ]]; then
    mkdir -p "${DIRECTORIO_SIMULADORES}"
    cp "${respaldo}/simuladores.env" "${DIRECTORIO_SIMULADORES}/.env"
  fi
}

ultimo_respaldo() {
  local candidato
  candidato="$(ls -1d "${RESPALDOS}"/*/ 2>/dev/null | sort | tail -n 1)"
  [[ -n "${candidato}" ]] || return 1
  printf '%s\n' "${candidato%/}"
}

banderas_compose=()
for archivo in "${DEPLOY_DIRECTORIO}"/compose*.yaml; do
  [[ -f "${archivo}" ]] || continue
  banderas_compose+=(-f "${archivo}")
done

esperar_sano() {
  local contenedor="$1" intentos=0

  while [[ "${intentos}" -lt 45 ]]; do
    if [[ "$("${DOCKER}" inspect -f '{{.State.Health.Status}}' "${contenedor}" 2>/dev/null)" == "healthy" ]]; then
      printf 'deploy-ci: %s healthy.\n' "${contenedor}"
      return 0
    fi
    intentos=$((intentos + 1))
    sleep 2
  done

  printf 'deploy-ci: %s no quedó healthy en 90 segundos.\n' "${contenedor}" >&2
  return 1
}

humo() {
  if [[ "${DEPLOY_SIMULAR}" == "si" ]]; then
    printf 'deploy-ci: [simulación] GET %s\n' "${DEPLOY_SMOKE_URL}"
    return 0
  fi
  if ! curl -fsS --max-time 10 "${DEPLOY_SMOKE_URL}" >/dev/null; then
    printf 'deploy-ci: el humo contra %s no respondió.\n' "${DEPLOY_SMOKE_URL}" >&2
    return 1
  fi
  printf 'deploy-ci: humo OK (%s).\n' "${DEPLOY_SMOKE_URL}"
}

subir_api() {
  if [[ "${DEPLOY_SIMULAR}" == "si" ]]; then
    printf 'deploy-ci: [simulación] docker compose pull migrate api; run --rm migrate; up -d api\n'
    return 0
  fi
  if [[ "${#banderas_compose[@]}" -eq 0 ]]; then
    error_deploy "no hay compose*.yaml en ${DEPLOY_DIRECTORIO}"
  fi
  cd "${DEPLOY_DIRECTORIO}"
  "${DOCKER}" compose "${banderas_compose[@]}" pull migrate api
  # Las migraciones corren como servicio de un solo uso antes de que la API
  # nueva arranque: "up -d api" reusa el contenedor migrate ya terminado y no
  # volvería a aplicarlas. Si fallan, el "set -e" corta aquí y la API vigente
  # sigue sirviendo.
  "${DOCKER}" compose "${banderas_compose[@]}" run --rm migrate
  "${DOCKER}" compose "${banderas_compose[@]}" up -d api
  esperar_sano centinela-api
}

subir_simuladores() {
  if [[ "${DEPLOY_SIMULAR}" == "si" ]]; then
    printf 'deploy-ci: [simulación] simuladores: pull y up -d\n'
    return 0
  fi
  if [[ -d "${DIRECTORIO_SIMULADORES}" ]]; then
    cd "${DIRECTORIO_SIMULADORES}"
    "${DOCKER}" compose pull
    "${DOCKER}" compose up -d
    for contenedor in vera-simulador-riel vera-simulador-custodia vera-simulador-pse; do
      esperar_sano "${contenedor}"
    done
  fi
}

if [[ "${rollback}" == "si" ]]; then
  respaldo="$(ultimo_respaldo)" || error_deploy "no hay respaldos en ${RESPALDOS}: no hay entorno anterior al que volver" 69
  printf 'deploy-ci: rollback al entorno %s.\n' "${respaldo}"
  restaurar_entorno "${respaldo}"
  subir_api
  subir_simuladores
  humo
  printf 'deploy-ci: entorno anterior restaurado y sirviendo.\n'
  exit 0
fi

printf 'deploy-ci: desplegando la rama %s (%s).\n' "${rama}" "${REGISTRO}:${rama}"

respaldo="$(respaldar_entorno)"
printf 'deploy-ci: entorno anterior respaldado en %s.\n' "${respaldo}"

if [[ "${#banderas_compose[@]}" -eq 0 && "${DEPLOY_SIMULAR}" != "si" ]]; then
  error_deploy "no hay compose*.yaml en ${DEPLOY_DIRECTORIO}"
fi

if [[ -n "${digest}" ]]; then
  fijar_variable "${DEPLOY_DIRECTORIO}/.env" IMAGEN_API "${REGISTRO}@${digest}"
  printf 'deploy-ci: imagen fijada por digest %s.\n' "${digest}"
else
  fijar_variable "${DEPLOY_DIRECTORIO}/.env" IMAGEN_API "${REGISTRO}:${rama}"
fi
if [[ -n "${modelo}" ]]; then
  fijar_variable "${DEPLOY_DIRECTORIO}/.env" MODELO_TAG "${modelo}"
  printf 'deploy-ci: modelo fijado en %s.\n' "${modelo}"
fi
fijar_variable "${DIRECTORIO_SIMULADORES}/.env" TAG "${rama}"

subir_api
subir_simuladores
humo

printf 'deploy-ci: rama %s desplegada y healthy.\n' "${rama}"

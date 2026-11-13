#!/usr/bin/env bash
# infra/servidor/deploy-ci_test.sh
# Prueba en seco de deploy-ci.sh con docker simulado. Cubre: digest válido, digest inválido,
# modelo inválido, rollback con respaldo, rollback sin respaldo y rama inválida.
set -euo pipefail

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="${DIRECTORIO_SCRIPT}/deploy-ci.sh"
HEX="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
BASE="$(mktemp -d)"
trap 'rm -rf "${BASE}"' EXIT

preparar() {
  local casa="$1"
  mkdir -p "${casa}/simuladores"
  printf 'IMAGEN_API=ghcr.io/santiagovillamilc/backend_vera:main\nMODELO_TAG=viejo\n' >"${casa}/.env"
  printf 'TAG=main\n' >"${casa}/simuladores/.env"
  printf '{"artefactos":[{"nombre":"gemma-4-e2b-it-q4_k_m"}]}' >"${casa}/lock.json"
}

correr() {
  local casa="$1"
  shift
  DEPLOY_SIMULAR=si DEPLOY_DIRECTORIO="${casa}" DEPLOY_LOCK_MODELOS="${casa}/lock.json" \
    bash "${SCRIPT}" "$@"
}

probar_codigo() {
  local etiqueta="$1" esperado="$2" casa="$3"
  shift 3
  local codigo
  if correr "${casa}" "$@" >"${casa}/salida.txt" 2>&1; then
    codigo=0
  else
    codigo=$?
  fi
  if [[ "${codigo}" -ne "${esperado}" ]]; then
    printf '[fallo] %s: código %s, esperaba %s\n%s\n' "${etiqueta}" "${codigo}" "${esperado}" "${casa}/salida.txt" >&2
    cat "${casa}/salida.txt" >&2
    exit 1
  fi
  printf 'ok %s (código %s)\n' "${etiqueta}" "${codigo}"
}

# 1. Digest válido y modelo válido: pina ambos y respalda.
CASA="${BASE}/valido"
preparar "${CASA}"
probar_codigo "digest y modelo válidos" 0 "${CASA}" main --digest "sha256:${HEX}" --modelo gemma-4-e2b-it-q4_k_m
grep -q "IMAGEN_API=ghcr.io/santiagovillamilc/backend_vera@sha256:${HEX}" "${CASA}/.env" || {
  printf '[fallo] el digest no quedó en el entorno\n' >&2
  exit 1
}
grep -q 'MODELO_TAG=gemma-4-e2b-it-q4_k_m' "${CASA}/.env" || {
  printf '[fallo] el modelo no quedó en el entorno\n' >&2
  exit 1
}
[[ -n "$(ls -1d "${CASA}"/respaldos/*/ 2>/dev/null)" ]] || {
  printf '[fallo] no se guardó respaldo\n' >&2
  exit 1
}
printf 'ok digest y modelo pinados con respaldo\n'

# 2. Digest inválido: aborta sin tocar el entorno.
CASA="${BASE}/digest-malo"
preparar "${CASA}"
antes="$(cat "${CASA}/.env")"
probar_codigo "digest inválido" 65 "${CASA}" main --digest sha256:zzz
if [[ "${antes}" != "$(cat "${CASA}/.env")" ]]; then
  printf '[fallo] el entorno cambió con un digest inválido\n' >&2
  exit 1
fi
printf 'ok digest inválido no toca el entorno\n'

# 3. Modelo que no está en el lockfile.
CASA="${BASE}/modelo-malo"
preparar "${CASA}"
probar_codigo "modelo inválido" 68 "${CASA}" main --modelo no-existe

# 4. Rollback con respaldo restaura el entorno anterior.
CASA="${BASE}/rollback"
preparar "${CASA}"
probar_codigo "despliegue previo" 0 "${CASA}" main --digest "sha256:${HEX}" --modelo gemma-4-e2b-it-q4_k_m
probar_codigo "rollback con respaldo" 0 "${CASA}" --rollback
grep -q 'IMAGEN_API=ghcr.io/santiagovillamilc/backend_vera:main' "${CASA}/.env" || {
  printf '[fallo] el rollback no restauró la imagen\n' >&2
  exit 1
}
grep -q 'MODELO_TAG=viejo' "${CASA}/.env" || {
  printf '[fallo] el rollback no restauró el modelo\n' >&2
  exit 1
}
printf 'ok rollback restaura imagen y modelo\n'

# 5. Rollback sin respaldo.
CASA="${BASE}/sin-respaldo"
mkdir -p "${CASA}"
printf 'IMAGEN_API=x\n' >"${CASA}/.env"
printf '{"artefactos":[]}' >"${CASA}/lock.json"
probar_codigo "rollback sin respaldo" 69 "${CASA}" --rollback

# 6. Rama inválida.
CASA="${BASE}/rama-mala"
preparar "${CASA}"
probar_codigo "rama inválida" 64 "${CASA}" produccionxx

printf 'deploy-ci en seco: todos los casos pasan\n'

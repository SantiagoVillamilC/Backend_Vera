#!/usr/bin/env bash
# infra/modelos/deploy-modelos_test.sh
# Prueba de deploy-modelos.sh sin docker real, sin red y sin tocar Ollama ni el puerto 11434.
# Usa un docker simulado que registra sus llamadas y puede fallar el pull, devolver un digest
# distinto o reportar unhealthy para un digest elegido. Cubre el camino sin simular, incluido el
# rollback automático, y el modo --simular.
set -euo pipefail

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="${DIRECTORIO_SCRIPT}/deploy-modelos.sh"
BASE="$(mktemp -d)"
trap 'rm -rf "${BASE}"' EXIT

A="sha256:$(printf 'a%.0s' {1..64})"
B="sha256:$(printf 'b%.0s' {1..64})"
C="sha256:$(printf 'c%.0s' {1..64})"
D="sha256:$(printf 'd%.0s' {1..64})"
REGISTRO="ghcr.io/santiagovillamilc/backend_vera"

# docker simulado. Archivos de control en FAKE_DIR:
#   inexistentes     digests que `manifest inspect` no encuentra
#   pull-falla       si existe, `compose pull` sale con 1
#   pull-distinto    si existe, `image inspect` no devuelve el digest pedido
#   digests-malos    digests cuyo contenedor queda unhealthy tras `up`
#   senal            si existe, el siguiente `up` envía esa señal (INT, TERM o HUP) al script y borra
#                    el archivo, así que el `up` de la restauración ya no la repite
cat >"${BASE}/docker" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${FAKE_DIR}/llamadas.log"
case "$1" in
  manifest)
    digest="${3#*@}"
    if [[ -s "${FAKE_DIR}/inexistentes" ]] && grep -qF "${digest}" "${FAKE_DIR}/inexistentes"; then
      exit 1
    fi
    ;;
  image)
    if [[ -f "${FAKE_DIR}/pull-distinto" ]]; then
      printf 'otro/repo@sha256:0\n'
    else
      printf '%s\n' "${@: -1}"
    fi
    ;;
  inspect)
    if [[ -s "${FAKE_DIR}/digests-malos" && -f "${FAKE_DIR}/env-actual" ]] &&
      grep -qFf "${FAKE_DIR}/digests-malos" "${FAKE_DIR}/env-actual"; then
      printf 'unhealthy\n'
    else
      printf 'healthy\n'
    fi
    ;;
  compose)
    shift
    env_file=""
    while [[ "$#" -gt 0 ]]; do
      case "$1" in
        --env-file) env_file="$2"; shift ;;
        pull)
          [[ ! -f "${FAKE_DIR}/pull-falla" ]] || exit 1
          exit 0
          ;;
        up)
          if [[ -s "${FAKE_DIR}/senal" ]]; then
            senal="$(cat "${FAKE_DIR}/senal")"
            rm -f "${FAKE_DIR}/senal"
            kill -s "${senal}" "${PPID}"
          fi
          cp "${env_file}" "${FAKE_DIR}/env-actual"
          exit 0
          ;;
        ps)
          printf 'id-%s\n' "${@: -1}"
          exit 0
          ;;
      esac
      shift
    done
    ;;
esac
FAKE
chmod +x "${BASE}/docker"

preparar() {
  local casa="$1"
  mkdir -p "${casa}/fake"
  printf '{"artefactos":[{"nombre":"gemma-4-e2b-it-q4_k_m"},{"nombre":"gemma-4-e4b-it-q4_k_m"},{"nombre":"whisper-small"},{"nombre":"kokoro-v1.0-int8-onnx"}]}' >"${casa}/lock.json"
}

declarar() {
  local casa="$1"
  python3 - "${casa}/imagenes.json" "${REGISTRO}" "$2" "$3" "$4" <<'PY'
import json
import sys

def digest(valor):
    return None if valor == "null" else valor

datos = {
    "version": 1,
    "registro": sys.argv[2],
    "imagenes": {
        "vera-llm": {"digest": digest(sys.argv[3])},
        "vera-voz": {"digest": digest(sys.argv[4])},
        "vera-kokoro": {"digest": digest(sys.argv[5])},
    },
}
json.dump(datos, open(sys.argv[1], "w", encoding="utf-8"))
PY
}

# correr CASA [args]. Cualquier variable DEPLOY_SIMULAR heredada se ignora a propósito.
correr() {
  local casa="$1"
  shift
  FAKE_DIR="${casa}/fake" DOCKER="${BASE}/docker" \
    DEPLOY_SIMULAR="${SIMULAR:-no}" VERA_GPU="${VERA_GPU:-no}" \
    DEPLOY_MODELOS_ESTADO="${casa}/estado" DEPLOY_MODELOS_DECLARADO="${casa}/imagenes.json" \
    DEPLOY_LOCK_MODELOS="${casa}/lock.json" DEPLOY_MODELOS_ESPERA=2 DEPLOY_MODELOS_INTERVALO=0 \
    WHISPER_TOKEN="${WHISPER_TOKEN-token-whisper}" KOKORO_TOKEN="${KOKORO_TOKEN-token-kokoro}" \
    bash "${SCRIPT}" "$@"
}

fallar() {
  printf '[fallo] %s\n' "$1" >&2
  exit 1
}

probar() {
  local etiqueta="$1" esperado="$2" casa="$3" codigo
  shift 3
  if correr "${casa}" "$@" >"${casa}/salida.txt" 2>&1; then
    codigo=0
  else
    codigo=$?
  fi
  if [[ "${codigo}" -ne "${esperado}" ]]; then
    cat "${casa}/salida.txt" >&2
    fallar "${etiqueta}: código ${codigo}, esperaba ${esperado}"
  fi
  printf 'ok %s (código %s)\n' "${etiqueta}" "${codigo}"
}

tiene() {
  grep -qF -- "$2" "$1" || fallar "$3"
}

no_tiene() {
  if grep -qF -- "$2" "$1"; then fallar "$3"; fi
}

ref() {
  printf '%s/%s@%s' "${REGISTRO}" "$1" "$2"
}

# 1. Primer despliegue sin simular: pull, up, healthy, entorno escrito sin respaldo previo.
CASA="${BASE}/primero"
preparar "${CASA}"
declarar "${CASA}" "${A}" "${A}" "${A}"
probar "primer despliegue" 0 "${CASA}" --modelo gemma-4-e2b-it-q4_k_m
tiene "${CASA}/estado/modelos.env" "IMAGEN_VERA_LLM=$(ref vera-llm "${A}")" "el digest de vera-llm no quedó en el entorno"
tiene "${CASA}/estado/modelos.env" "OLLAMA_MODELO=gemma-4-e2b-it-q4_k_m" "el modelo no quedó en el entorno"
tiene "${CASA}/fake/llamadas.log" "pull" "no hubo pull"
tiene "${CASA}/fake/llamadas.log" "up -d --no-build" "no hubo up"
tiene "${CASA}/fake/llamadas.log" "manifest inspect $(ref vera-kokoro "${A}")" "no validó el digest en el registro"
[[ ! -d "${CASA}/estado/respaldos-modelos" ]] || fallar "el primer despliegue no debería crear respaldo"
tiene "${CASA}/estado/historial.log" "despliegue-ok" "el historial no registró el éxito"

# 2. Segundo despliegue sano: respalda el anterior y lo registra.
declarar "${CASA}" "${B}" "${B}" "${B}"
probar "segundo despliegue sano" 0 "${CASA}"
tiene "${CASA}/estado/modelos.env" "IMAGEN_VERA_VOZ=$(ref vera-voz "${B}")" "el digest nuevo no quedó"
tiene "${CASA}/estado/modelos.env" "OLLAMA_MODELO=gemma-4-e2b-it-q4_k_m" "el modelo no se conservó"
[[ -n "$(ls -1d "${CASA}"/estado/respaldos-modelos/[0-9]*/ 2>/dev/null)" ]] || fallar "no se guardó respaldo"
tiene "${CASA}/estado/historial.log" "anterior: llm=$(ref vera-llm "${A}")" "el historial no registró el digest anterior"

# 3. Rollback manual vuelve al digest anterior y un segundo rollback ya no tiene a dónde ir.
probar "rollback manual" 0 "${CASA}" --rollback
tiene "${CASA}/estado/modelos.env" "IMAGEN_VERA_LLM=$(ref vera-llm "${A}")" "el rollback no restauró el digest anterior"
probar "rollback sin más respaldos" 69 "${CASA}" --rollback

# 4. Healthcheck falla con el digest nuevo: rollback automático, código 72, entorno anterior sano.
CASA="${BASE}/health-falla"
preparar "${CASA}"
declarar "${CASA}" "${A}" "${A}" "${A}"
probar "base sana" 0 "${CASA}"
declarar "${CASA}" "${C}" "${C}" "${C}"
printf '%s\n' "${C}" >"${CASA}/fake/digests-malos"
probar "healthcheck falla" 72 "${CASA}"
tiene "${CASA}/estado/modelos.env" "IMAGEN_VERA_LLM=$(ref vera-llm "${A}")" "el rollback automático no restauró el entorno"
no_tiene "${CASA}/estado/modelos.env" "${C}" "el digest malo sigue en el entorno"
tiene "${CASA}/estado/historial.log" "rollback-automatico-ok" "el historial no registró el rollback automático"
tiene "${CASA}/fake/env-actual" "$(ref vera-llm "${A}")" "tras el rollback no se volvió a levantar la pila anterior"
[[ -d "${CASA}/estado/respaldos-modelos/aplicados" ]] || fallar "el respaldo usado no se movió a aplicados"

# 5. Healthcheck falla y el entorno anterior tampoco queda sano: código 73.
CASA="${BASE}/rollback-falla"
preparar "${CASA}"
declarar "${CASA}" "${A}" "${A}" "${A}"
probar "base" 0 "${CASA}"
declarar "${CASA}" "${C}" "${C}" "${C}"
printf '%s\n%s\n' "${A}" "${C}" >"${CASA}/fake/digests-malos"
probar "rollback tampoco sano" 73 "${CASA}"

# 6. Primer despliegue que falla: no hay a dónde volver, código 74.
CASA="${BASE}/primero-falla"
preparar "${CASA}"
declarar "${CASA}" "${D}" "${D}" "${D}"
printf '%s\n' "${D}" >"${CASA}/fake/digests-malos"
probar "primer despliegue sin vuelta atrás" 74 "${CASA}"

# 7. Lo descargado no coincide con el digest declarado: código 75 y entorno anterior restaurado.
CASA="${BASE}/pull-distinto"
preparar "${CASA}"
declarar "${CASA}" "${A}" "${A}" "${A}"
probar "base" 0 "${CASA}"
declarar "${CASA}" "${B}" "${B}" "${B}"
touch "${CASA}/fake/pull-distinto"
probar "digest descargado distinto" 75 "${CASA}"
tiene "${CASA}/estado/modelos.env" "$(ref vera-llm "${A}")" "no se restauró el entorno tras el digest distinto"

# 8. El pull falla: código 77 y entorno anterior restaurado.
CASA="${BASE}/pull-falla"
preparar "${CASA}"
declarar "${CASA}" "${A}" "${A}" "${A}"
probar "base" 0 "${CASA}"
declarar "${CASA}" "${B}" "${B}" "${B}"
touch "${CASA}/fake/pull-falla"
probar "pull falla" 77 "${CASA}"
tiene "${CASA}/estado/modelos.env" "$(ref vera-llm "${A}")" "no se restauró el entorno tras el pull fallido"

# 9. Digest declarado que no existe bajo el nombre de su imagen: código 66 sin tocar el entorno.
CASA="${BASE}/no-existe"
preparar "${CASA}"
declarar "${CASA}" "${A}" "${A}" "${A}"
probar "base" 0 "${CASA}"
antes="$(cat "${CASA}/estado/modelos.env")"
declarar "${CASA}" "${A}" "${B}" "${A}"
printf '%s\n' "${B}" >"${CASA}/fake/inexistentes"
probar "digest inexistente en el registro" 66 "${CASA}"
[[ "${antes}" == "$(cat "${CASA}/estado/modelos.env")" ]] || fallar "el entorno cambió con un digest inexistente"

# 10. Digest sin declarar (null) o con formato inválido: no se toca nada.
CASA="${BASE}/sin-declarar"
preparar "${CASA}"
declarar "${CASA}" null "${A}" "${A}"
probar "digest sin declarar" 70 "${CASA}"
[[ ! -e "${CASA}/estado/modelos.env" ]] || fallar "se escribió entorno con un digest sin declarar"
declarar "${CASA}" "sha256:zzz" "${A}" "${A}"
probar "digest con formato inválido" 65 "${CASA}"

# 11. Modelo que no está en el lockfile.
CASA="${BASE}/modelo-malo"
preparar "${CASA}"
declarar "${CASA}" "${A}" "${A}" "${A}"
probar "modelo inválido" 68 "${CASA}" --modelo no-existe
# Un artefacto del lockfile que no es de Ollama (voz) tampoco se acepta como modelo del razonador.
probar "whisper-small no es modelo del razonador" 68 "${CASA}" --modelo whisper-small
probar "kokoro no es modelo del razonador" 68 "${CASA}" --modelo kokoro-v1.0-int8-onnx
[[ ! -e "${CASA}/estado/modelos.env" ]] || fallar "se escribió entorno con un modelo rechazado"

# 12. Sin tokens, rollback sin respaldos y argumento inválido.
CASA="${BASE}/varios"
preparar "${CASA}"
declarar "${CASA}" "${A}" "${A}" "${A}"
KOKORO_TOKEN="" probar "falta KOKORO_TOKEN" 71 "${CASA}"
probar "rollback sin respaldos" 69 "${CASA}" --rollback
probar "argumento inválido" 64 "${CASA}" main

# 13. Modo --simular: no llama a docker y deja entorno e historial.
CASA="${BASE}/simulado"
preparar "${CASA}"
declarar "${CASA}" "${A}" "${A}" "${A}"
SIMULAR=si probar "despliegue simulado" 0 "${CASA}" --modelo gemma-4-e4b-it-q4_k_m
[[ ! -e "${CASA}/fake/llamadas.log" ]] || fallar "el modo simulado llamó a docker"
tiene "${CASA}/estado/modelos.env" "OLLAMA_MODELO=gemma-4-e4b-it-q4_k_m" "el modo simulado no escribió el modelo"

# 14. Señal durante el despliegue, con modelos.env ya reemplazado: restaura el respaldo y sale con 128+n.
for par in "INT 130" "TERM 143" "HUP 129"; do
  senal="${par% *}"
  esperado="${par#* }"
  CASA="${BASE}/senal-${senal}"
  preparar "${CASA}"
  declarar "${CASA}" "${A}" "${A}" "${A}"
  probar "base para señal ${senal}" 0 "${CASA}"
  declarar "${CASA}" "${B}" "${B}" "${B}"
  printf '%s\n' "${senal}" >"${CASA}/fake/senal"
  probar "señal ${senal} durante el despliegue" "${esperado}" "${CASA}"
  tiene "${CASA}/estado/modelos.env" "IMAGEN_VERA_LLM=$(ref vera-llm "${A}")" "la señal ${senal} no restauró el respaldo"
  no_tiene "${CASA}/estado/modelos.env" "${B}" "el digest nuevo sigue en el entorno tras ${senal}"
  tiene "${CASA}/fake/env-actual" "$(ref vera-llm "${A}")" "tras ${senal} no se volvió a levantar la pila anterior"
  tiene "${CASA}/estado/historial.log" "despliegue-interrumpido" "el historial no registró la interrupción ${senal}"
  tiene "${CASA}/estado/historial.log" "rollback-por-senal-ok" "el historial no registró el rollback por ${senal}"
  [[ -d "${CASA}/estado/respaldos-modelos/aplicados" ]] || fallar "tras ${senal} el respaldo usado no pasó a aplicados"
done

# 15. Señal en el primer despliegue: no hay respaldo, se avisa y no se restaura nada.
CASA="${BASE}/senal-primero"
preparar "${CASA}"
declarar "${CASA}" "${A}" "${A}" "${A}"
printf 'TERM\n' >"${CASA}/fake/senal"
probar "señal en el primer despliegue" 143 "${CASA}"
tiene "${CASA}/estado/historial.log" "despliegue-interrumpido" "el historial no registró la interrupción del primer despliegue"
tiene "${CASA}/salida.txt" "no había entorno anterior" "no avisó que no había a dónde volver"

# 16. GPU: sin VERA_GPU no se usa el override, con VERA_GPU=si sí, y un valor raro aborta con 64.
CASA="${BASE}/gpu"
preparar "${CASA}"
declarar "${CASA}" "${A}" "${A}" "${A}"
probar "sin VERA_GPU" 0 "${CASA}"
no_tiene "${CASA}/fake/llamadas.log" "compose.registro-gpu.yaml" "usó el override de GPU sin pedirlo"
tiene "${CASA}/salida.txt" "corre en CPU" "no avisó que vera-llm corre en CPU"
VERA_GPU=si probar "VERA_GPU=si" 0 "${CASA}"
tiene "${CASA}/fake/llamadas.log" "compose.registro-gpu.yaml" "VERA_GPU=si no usó el override de GPU"
VERA_GPU=quizas probar "VERA_GPU inválido" 64 "${CASA}"

printf 'deploy-modelos: todos los casos pasan\n'

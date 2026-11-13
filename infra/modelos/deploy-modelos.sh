#!/usr/bin/env bash
# infra/modelos/deploy-modelos.sh
# Despliegue de la pila de modelos (vera-llm, vera-voz, vera-kokoro) por digest, con respaldo del
# entorno anterior y vuelta atrás automática si algún healthcheck falla.
#
# Hermano de infra/servidor/deploy-ci.sh, que solo valida el digest de la imagen de la API. Se
# mantiene aparte porque la pila de modelos corre en el pod, no en la VM de CI, y porque la llave SSH
# de CI solo recibe un nombre de rama: no puede transportar digests. Lo ejecuta el operador.
#
# Qué hace, en orden y sin tocar nada hasta pasar las validaciones:
#   1. Lee los digests declarados en infra/modelos/imagenes.json. Un digest null o con formato
#      inválido aborta.
#   2. Comprueba que cada digest existe en el registro bajo el nombre de SU imagen. Un digest de otra
#      imagen (por ejemplo el de la API pegado en vera-llm) no existe bajo ese nombre y aborta.
#   3. Valida --modelo contra modelos/lock.json.
#   4. Guarda el entorno anterior (digests y modelo) y lo registra en historial.log.
#   5. pull, comprueba que lo descargado coincide con el digest declarado, up y espera healthy.
#   6. Si algo falla, restaura el entorno anterior y lo vuelve a levantar.
#
# Uso:
#   deploy-modelos.sh [--modelo TAG] [--rollback] [--simular]
# Variables: MODELO_TAG, DEPLOY_SIMULAR=si, VERA_GPU=si, DEPLOY_MODELOS_ESTADO, DEPLOY_MODELOS_DECLARADO,
#            DEPLOY_MODELOS_COMPOSE_DIR, DEPLOY_LOCK_MODELOS, DEPLOY_MODELOS_ESPERA,
#            DEPLOY_MODELOS_INTERVALO, DOCKER.
# Los tokens (WHISPER_TOKEN y KOKORO_TOKEN) llegan por entorno, igual que con el compose base. Nunca se
# guardan en el estado.
#
# --modelo solo acepta modelos de Ollama del razonador: nombre presente en modelos/lock.json y con
# prefijo "gemma-4-" (gemma-4-e2b-*, gemma-4-e4b-*). Whisper y Kokoro quedan fuera.
#
# GPU: sin VERA_GPU=si, vera-llm corre en CPU. Con VERA_GPU=si se suma compose.registro-gpu.yaml, que
# solo le da `gpus: all` a vera-llm. Esa ruta no se probó en un pod real.
#
# Señales: INT, TERM y HUP durante el despliegue, con modelos.env ya reemplazado y la verificación sin
# terminar, restauran el respaldo igual que el rollback automático. Bash ejecuta el trap cuando termina
# el comando en curso (por ejemplo un `compose up`), no a mitad de él. SIGKILL no se puede atrapar.
#
# No mezclar con pod-demo.sh en el mismo pod: ambos usan el proyecto compose `vera-modelos` (name: en
# compose.yaml), así que cada uno vería y pisaría los contenedores del otro. pod-demo.sh construye con
# compose.cuda.yaml y este script despliega por digest con compose.registro.yaml. Elegir un camino por
# pod y, para cambiar de uno a otro, bajar primero la pila con el del camino anterior.
set -euo pipefail

DIRECTORIO_SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_DIR="${DEPLOY_MODELOS_COMPOSE_DIR:-${DIRECTORIO_SCRIPT}}"
ARCHIVO_DECLARADO="${DEPLOY_MODELOS_DECLARADO:-${DIRECTORIO_SCRIPT}/imagenes.json}"
ARCHIVO_LOCK_MODELOS="${DEPLOY_LOCK_MODELOS:-${DIRECTORIO_SCRIPT}/../../modelos/lock.json}"
ESTADO="${DEPLOY_MODELOS_ESTADO:-${DIRECTORIO_SCRIPT}/estado}"
ARCHIVO_ENV="${ESTADO}/modelos.env"
# Carpeta propia: no se mezcla con el respaldos/ de deploy-ci.sh, cuyo rollback tomaría el más reciente
# y no encontraría api.env.
RESPALDOS="${ESTADO}/respaldos-modelos"
APLICADOS="${RESPALDOS}/aplicados"
HISTORIAL="${ESTADO}/historial.log"
DEPLOY_SIMULAR="${DEPLOY_SIMULAR:-no}"
VERA_GPU="${VERA_GPU:-no}"
DOCKER="${DOCKER:-docker}"
# 360 s cubre el healthcheck más largo de compose.yaml (vera-voz: 120 s de arranque y 18 reintentos de 10 s).
ESPERA="${DEPLOY_MODELOS_ESPERA:-360}"
INTERVALO="${DEPLOY_MODELOS_INTERVALO:-2}"
SERVICIOS=(vera-llm vera-voz vera-kokoro)
modelo="${MODELO_TAG:-}"
rollback="no"
FALLO_CODIGO=1

error_deploy() {
  printf 'deploy-modelos: %s\n' "$1" >&2
  exit "${2:-1}"
}

aviso() {
  printf 'deploy-modelos: %s\n' "$1"
}

while [[ "$#" -gt 0 ]]; do
  case "$1" in
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
    *)
      error_deploy "argumento no permitido (\"$1\"), usa --modelo, --rollback o --simular" 64
      ;;
  esac
  shift
done

# Imagen de un servicio: registro/servicio@digest.
referencia() {
  printf '%s/%s@%s' "${registro}" "$1" "$2"
}

# Lee registro y los tres digests declarados. Deja `registro` y `digest_<servicio>` listos.
leer_declarado() {
  local salida
  [[ -f "${ARCHIVO_DECLARADO}" ]] || error_deploy "no encuentro ${ARCHIVO_DECLARADO}" 70
  if ! salida="$(python3 - "${ARCHIVO_DECLARADO}" <<'PY'
import json
import sys

datos = json.load(open(sys.argv[1], encoding="utf-8"))
print(datos["registro"])
for nombre in ("vera-llm", "vera-voz", "vera-kokoro"):
    print(datos["imagenes"][nombre].get("digest") or "")
PY
  )"; then
    error_deploy "${ARCHIVO_DECLARADO} no tiene la forma esperada" 70
  fi
  mapfile -t lineas <<<"${salida}"
  registro="${lineas[0]}"
  digest_llm="${lineas[1]:-}"
  digest_voz="${lineas[2]:-}"
  digest_kokoro="${lineas[3]:-}"
  [[ -n "${registro}" ]] || error_deploy "imagenes.json no declara el registro" 70
}

validar_digest() {
  local servicio="$1" digest="$2"
  if [[ -z "${digest}" ]]; then
    error_deploy "imagenes.json no declara digest para ${servicio}: no se despliega" 70
  fi
  if [[ ! "${digest}" =~ ^sha256:[0-9a-f]{64}$ ]]; then
    error_deploy "digest inválido para ${servicio}: \"${digest}\", se espera sha256:<64 hex>" 65
  fi
}

existe_en_registro() {
  local servicio="$1" digest="$2"
  if ! "${DOCKER}" manifest inspect "$(referencia "${servicio}" "${digest}")" >/dev/null 2>&1; then
    error_deploy "el digest ${digest} no existe como ${registro}/${servicio}, no se toca el entorno vigente" 66
  fi
}

# --modelo fija OLLAMA_MODELO, así que solo vale un modelo de Ollama del razonador. Criterio: el nombre
# está en modelos/lock.json Y empieza por "gemma-4-" (gemma-4-e2b-* o gemma-4-e4b-*). modelos/lock.json
# no trae un campo de tipo y también lista artefactos de voz (whisper-small, kokoro-*), que no son
# modelos de Ollama y dejarían a vera-llm sin poder cargar nada. Si se añade otra familia al razonador,
# hay que ampliar el prefijo aquí.
PREFIJO_MODELO_RAZONADOR="gemma-4-"

validar_modelo() {
  [[ -n "${modelo}" ]] || return 0
  [[ -f "${ARCHIVO_LOCK_MODELOS}" ]] || error_deploy "no encuentro el lockfile de modelos ${ARCHIVO_LOCK_MODELOS}" 67
  if ! python3 - "${ARCHIVO_LOCK_MODELOS}" "${modelo}" "${PREFIJO_MODELO_RAZONADOR}" <<'PY'
import json
import sys

datos = json.load(open(sys.argv[1], encoding="utf-8"))
nombres = {artefacto.get("nombre") for artefacto in datos.get("artefactos", [])}
sys.exit(0 if sys.argv[2] in nombres and sys.argv[2].startswith(sys.argv[3]) else 1)
PY
  then
    error_deploy "el modelo \"${modelo}\" no es un modelo de Ollama del razonador: debe estar en ${ARCHIVO_LOCK_MODELOS} y empezar por \"${PREFIJO_MODELO_RAZONADOR}\"" 68
  fi
}

validar_tokens() {
  [[ "${DEPLOY_SIMULAR}" != "si" ]] || return 0
  [[ -n "${WHISPER_TOKEN:-}" ]] || error_deploy "falta WHISPER_TOKEN en el entorno" 71
  [[ -n "${KOKORO_TOKEN:-}" ]] || error_deploy "falta KOKORO_TOKEN en el entorno" 71
}

valor_env() {
  local archivo="$1" variable="$2"
  [[ -f "${archivo}" ]] || return 0
  { grep -m1 "^${variable}=" "${archivo}" || true; } | cut -d= -f2-
}

# Escribe el .env de estado de forma atómica: nunca queda un archivo a medias.
escribir_env() {
  local llm="$1" voz="$2" kokoro="$3" tag="$4" temporal
  mkdir -p "${ESTADO}"
  temporal="$(mktemp "${ESTADO}/modelos.env.XXXXXX")"
  {
    printf 'IMAGEN_VERA_LLM=%s\n' "${llm}"
    printf 'IMAGEN_VERA_VOZ=%s\n' "${voz}"
    printf 'IMAGEN_VERA_KOKORO=%s\n' "${kokoro}"
    [[ -z "${tag}" ]] || printf 'OLLAMA_MODELO=%s\n' "${tag}"
  } >"${temporal}"
  chmod 600 "${temporal}"
  mv "${temporal}" "${ARCHIVO_ENV}"
}

registrar() {
  mkdir -p "${ESTADO}"
  printf '%s\t%s\t%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$1" "$2" >>"${HISTORIAL}"
}

resumen_env() {
  printf 'llm=%s voz=%s kokoro=%s modelo=%s' \
    "$(valor_env "$1" IMAGEN_VERA_LLM)" "$(valor_env "$1" IMAGEN_VERA_VOZ)" \
    "$(valor_env "$1" IMAGEN_VERA_KOKORO)" "$(valor_env "$1" OLLAMA_MODELO)"
}

# Copia el entorno vigente a un respaldo fechado y devuelve la ruta. Sin entorno vigente no hay nada
# que respaldar y devuelve vacío.
respaldar_entorno() {
  local destino
  [[ -f "${ARCHIVO_ENV}" ]] || return 0
  destino="${RESPALDOS}/$(date -u +%Y-%m-%dT%H-%M-%S-%NZ)"
  mkdir -p "${destino}"
  cp "${ARCHIVO_ENV}" "${destino}/modelos.env"
  printf '%s\n' "${destino}"
}

# El respaldo más reciente que aún no se aplicó. Los ya usados se mueven a aplicados/ para que
# rollbacks consecutivos retrocedan un paso cada vez en vez de repetir el mismo.
ultimo_respaldo() {
  local candidatos=("${RESPALDOS}"/[0-9]*/)
  # Sin respaldos el glob queda sin expandir y la comprobación de directorio lo descarta. Los nombres
  # son fechas ISO, así que el orden alfabético del glob es el cronológico.
  [[ -d "${candidatos[-1]}" ]] || return 1
  printf '%s\n' "${candidatos[-1]%/}"
}

marcar_aplicado() {
  mkdir -p "${APLICADOS}"
  mv "$1" "${APLICADOS}/"
}

compose() {
  local archivos=(-f "${COMPOSE_DIR}/compose.yaml" -f "${COMPOSE_DIR}/compose.registro.yaml")
  # Con VERA_GPU=si se añade el override que le da la GPU a vera-llm. Sin él corre en CPU.
  [[ "${VERA_GPU}" != "si" ]] || archivos+=(-f "${COMPOSE_DIR}/compose.registro-gpu.yaml")
  "${DOCKER}" compose "${archivos[@]}" --env-file "${ARCHIVO_ENV}" "$@"
}

validar_gpu() {
  case "${VERA_GPU}" in
    no) aviso "VERA_GPU no está en si: vera-llm corre en CPU." ;;
    si)
      [[ -f "${COMPOSE_DIR}/compose.registro-gpu.yaml" ]] ||
        error_deploy "VERA_GPU=si pero no encuentro ${COMPOSE_DIR}/compose.registro-gpu.yaml" 70
      aviso "VERA_GPU=si: vera-llm usa la GPU (no probado en un pod real)."
      ;;
    *) error_deploy "VERA_GPU debe ser si o no (vale \"${VERA_GPU}\")" 64 ;;
  esac
}

# Espera a que el contenedor del servicio quede healthy. Sale antes si queda unhealthy.
esperar_sano() {
  local servicio="$1" identificador estado intentos=0 maximo paso
  paso="${INTERVALO}"
  [[ "${paso}" -gt 0 ]] || paso=1
  maximo=$((ESPERA / paso))
  [[ "${maximo}" -gt 0 ]] || maximo=1

  while [[ "${intentos}" -lt "${maximo}" ]]; do
    identificador="$(compose ps -q "${servicio}" 2>/dev/null || true)"
    if [[ -n "${identificador}" ]]; then
      estado="$("${DOCKER}" inspect -f '{{.State.Health.Status}}' "${identificador}" 2>/dev/null || true)"
      if [[ "${estado}" == "healthy" ]]; then
        aviso "${servicio} healthy."
        return 0
      fi
      if [[ "${estado}" == "unhealthy" ]]; then
        printf 'deploy-modelos: %s quedó unhealthy.\n' "${servicio}" >&2
        return 1
      fi
    fi
    intentos=$((intentos + 1))
    sleep "${INTERVALO}"
  done

  printf 'deploy-modelos: %s no quedó healthy en %s segundos.\n' "${servicio}" "${ESPERA}" >&2
  return 1
}

# Levanta y espera los tres servicios. Se usa con el entorno ya escrito.
subir_y_esperar() {
  local servicio
  if [[ "${DEPLOY_SIMULAR}" == "si" ]]; then
    aviso "[simulación] compose up -d --no-build y espera de ${SERVICIOS[*]}"
    return 0
  fi
  compose up -d --no-build || {
    FALLO_CODIGO=72
    return 1
  }
  for servicio in "${SERVICIOS[@]}"; do
    esperar_sano "${servicio}" || {
      FALLO_CODIGO=72
      return 1
    }
  done
}

# Tras el pull, lo que hay en local bajo cada referencia debe ser exactamente el digest declarado.
verificar_pull() {
  local servicio digest referencia_completa repo_digests
  for servicio in "${SERVICIOS[@]}"; do
    case "${servicio}" in
      vera-llm) digest="${digest_llm}" ;;
      vera-voz) digest="${digest_voz}" ;;
      vera-kokoro) digest="${digest_kokoro}" ;;
    esac
    referencia_completa="$(referencia "${servicio}" "${digest}")"
    repo_digests="$("${DOCKER}" image inspect --format '{{join .RepoDigests " "}}' "${referencia_completa}" 2>/dev/null || true)"
    if [[ " ${repo_digests} " != *" ${referencia_completa} "* ]]; then
      printf 'deploy-modelos: lo descargado de %s no coincide con %s\n' "${servicio}" "${digest}" >&2
      FALLO_CODIGO=75
      return 1
    fi
  done
}

aplicar() {
  if [[ "${DEPLOY_SIMULAR}" == "si" ]]; then
    aviso "[simulación] compose pull, verificación del digest descargado, up -d --no-build y espera"
    return 0
  fi
  compose pull || {
    FALLO_CODIGO=77
    return 1
  }
  verificar_pull || return 1
  subir_y_esperar
}

# Restaura el respaldo, lo marca aplicado y vuelve a levantar. Devuelve 0 si la pila anterior quedó sana.
restaurar_y_subir() {
  local respaldo="$1"
  cp "${respaldo}/modelos.env" "${ARCHIVO_ENV}"
  marcar_aplicado "${respaldo}"
  subir_y_esperar
}

# Verdadero desde que modelos.env se reemplaza hasta que el despliegue termina bien o empieza el rollback
# automático. Mientras lo sea, una señal restaura el respaldo.
despliegue_en_curso="no"

# Handler de INT, TERM y HUP. Recibe el nombre de la señal y su número para devolver 128+n.
al_interrumpir() {
  local senal="$1" numero="$2"
  trap '' INT TERM HUP
  if [[ "${despliegue_en_curso}" == "si" ]]; then
    despliegue_en_curso="no"
    registrar "despliegue-interrumpido" "senal=${senal}"
    printf 'deploy-modelos: recibí %s con el despliegue sin terminar.\n' "${senal}" >&2
    if [[ -z "${respaldo}" ]]; then
      printf 'deploy-modelos: no había entorno anterior al que volver, revisa los contenedores a mano.\n' >&2
    elif restaurar_y_subir "${respaldo}"; then
      registrar "rollback-por-senal-ok" "$(resumen_env "${ARCHIVO_ENV}")"
      printf 'deploy-modelos: entorno anterior restaurado y sano.\n' >&2
    else
      registrar "rollback-por-senal-fallo" "$(resumen_env "${ARCHIVO_ENV}")"
      printf 'deploy-modelos: el entorno restaurado no quedó sano, revisa los contenedores a mano.\n' >&2
    fi
  fi
  exit $((128 + numero))
}

respaldo=""
trap 'al_interrumpir INT 2' INT
trap 'al_interrumpir TERM 15' TERM
trap 'al_interrumpir HUP 1' HUP

validar_tokens
validar_gpu

if [[ "${rollback}" == "si" ]]; then
  respaldo="$(ultimo_respaldo)" || error_deploy "no hay respaldos en ${RESPALDOS}, no hay entorno anterior al que volver" 69
  aviso "rollback al entorno $(resumen_env "${respaldo}/modelos.env")"
  registrar "rollback-manual" "desde: $(resumen_env "${ARCHIVO_ENV}") hacia: $(resumen_env "${respaldo}/modelos.env")"
  if restaurar_y_subir "${respaldo}"; then
    registrar "rollback-manual-ok" "$(resumen_env "${ARCHIVO_ENV}")"
    aviso "entorno anterior restaurado y sano."
    exit 0
  fi
  registrar "rollback-manual-fallo" "$(resumen_env "${ARCHIVO_ENV}")"
  error_deploy "el entorno restaurado no quedó sano, revisa los contenedores a mano" 73
fi

leer_declarado
validar_digest vera-llm "${digest_llm}"
validar_digest vera-voz "${digest_voz}"
validar_digest vera-kokoro "${digest_kokoro}"
validar_modelo
if [[ "${DEPLOY_SIMULAR}" != "si" ]]; then
  existe_en_registro vera-llm "${digest_llm}"
  existe_en_registro vera-voz "${digest_voz}"
  existe_en_registro vera-kokoro "${digest_kokoro}"
fi

# Si no se pide otro modelo, se conserva el del entorno vigente.
tag_nuevo="${modelo:-$(valor_env "${ARCHIVO_ENV}" OLLAMA_MODELO)}"
anterior="$(resumen_env "${ARCHIVO_ENV}")"
respaldo="$(respaldar_entorno)"
if [[ -n "${respaldo}" ]]; then
  aviso "entorno anterior respaldado en ${respaldo}."
else
  aviso "no hay entorno anterior: es el primer despliegue y no habrá a dónde volver."
fi

escribir_env "$(referencia vera-llm "${digest_llm}")" "$(referencia vera-voz "${digest_voz}")" \
  "$(referencia vera-kokoro "${digest_kokoro}")" "${tag_nuevo}"
despliegue_en_curso="si"
registrar "despliegue" "anterior: ${anterior} nuevo: $(resumen_env "${ARCHIVO_ENV}")"

if aplicar; then
  despliegue_en_curso="no"
  registrar "despliegue-ok" "$(resumen_env "${ARCHIVO_ENV}")"
  aviso "pila de modelos desplegada y healthy."
  exit 0
fi

# Falló el pull, la verificación del digest o un healthcheck: vuelta atrás automática.
despliegue_en_curso="no"
codigo_original="${FALLO_CODIGO}"
registrar "despliegue-fallo" "codigo=${codigo_original}"
if [[ -z "${respaldo}" ]]; then
  error_deploy "el despliegue falló y no había entorno anterior al que volver" 74
fi
printf 'deploy-modelos: el despliegue falló (código %s), vuelvo al entorno anterior.\n' "${codigo_original}" >&2
if restaurar_y_subir "${respaldo}"; then
  registrar "rollback-automatico-ok" "$(resumen_env "${ARCHIVO_ENV}")"
  error_deploy "despliegue revertido: el entorno anterior quedó sano, la corrida se marca como fallida" "${codigo_original}"
fi
registrar "rollback-automatico-fallo" "$(resumen_env "${ARCHIVO_ENV}")"
error_deploy "el despliegue falló y el entorno anterior tampoco quedó sano, revisa los contenedores a mano" 73

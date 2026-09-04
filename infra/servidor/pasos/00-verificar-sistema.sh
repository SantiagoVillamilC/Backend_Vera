#!/usr/bin/env bash
# infra/servidor/pasos/00-verificar-sistema.sh
# Gate de entrada (SCRUM-27): corre primero por el prefijo "00-". Se carga
# con source: nunca "return" para salir temprano, corta también
# ejecutar_paso en aprovisionar.sh.

NOMBRE_PASO="Verificar que el sistema es el fijado"
TARJETA="SCRUM-27"

paso "${NOMBRE_PASO} (${TARJETA})"

# IMAGEN_OS_ID, IMAGEN_OS_VERSION_ID e IMAGEN_VERSION_ESPERADA vienen de
# imagen-fijada.sh, cargado antes por aprovisionar.sh.

if [[ "${OMITIR_VERIFICACION_SISTEMA:-no}" == "si" ]]; then
  aviso "OMITIR_VERIFICACION_SISTEMA=si: se saltó A PROPÓSITO la verificación de sistema operativo y de imagen fijada. No uses esta salida sin saber por qué la necesitas."
else
  # 1. Sistema operativo fijado, sin excepción.
  id_sistema=""
  version_id_sistema=""
  if [[ -r /etc/os-release ]]; then
    # /etc/os-release no está en el repo, shellcheck no puede seguirlo.
    # shellcheck disable=SC1091
    id_sistema="$(. /etc/os-release && printf '%s' "${ID:-}")"
    # shellcheck disable=SC1091
    version_id_sistema="$(. /etc/os-release && printf '%s' "${VERSION_ID:-}")"
  fi

  if [[ "$id_sistema" != "$IMAGEN_OS_ID" || "$version_id_sistema" != "$IMAGEN_OS_VERSION_ID" ]]; then
    error_fatal "Sistema no soportado: se encontró ID=${id_sistema:-<vacío>} VERSION_ID=${version_id_sistema:-<vacío>}, se esperaba ID=${IMAGEN_OS_ID} VERSION_ID=${IMAGEN_OS_VERSION_ID}."
  fi
  info "Sistema operativo: ID=${IMAGEN_OS_ID} VERSION_ID=${IMAGEN_OS_VERSION_ID}, como se esperaba."

  # 2. Imagen de Azure, solo si curl está disponible y responde el metadata.
  if ! command -v curl >/dev/null 2>&1; then
    aviso "curl no está instalado: no se puede consultar el servicio de metadata de Azure, se omite la verificación de imagen."
  else
    # --max-time 2 obligatorio: sin límite, este paso cuelga fuera de Azure.
    respuesta_metadata="$(curl -H "Metadata:true" --max-time 2 -s \
      "http://169.254.169.254/metadata/instance/compute/storageProfile/imageReference?api-version=2021-02-01" 2>/dev/null || true)"

    if [[ -z "$respuesta_metadata" ]]; then
      info "El servicio de metadata de instancia de Azure no respondió: no se está ejecutando en Azure, se omite la verificación de imagen."
    else
      version_imagen="$(printf '%s' "$respuesta_metadata" | grep -o '"version":"[^"]*"' | head -n1 | cut -d'"' -f4 || true)"

      if [[ -z "$version_imagen" ]]; then
        aviso "El servicio de metadata respondió pero no se pudo leer el campo \"version\" de la imagen; se omite la comparación."
      elif [[ "$version_imagen" != "$IMAGEN_VERSION_ESPERADA" ]]; then
        error_fatal "La imagen de Azure no es la fijada: se encontró versión ${version_imagen}, se esperaba ${IMAGEN_VERSION_ESPERADA}."
      else
        info "Imagen de Azure: versión ${version_imagen}, como se esperaba."
      fi
    fi
  fi
fi

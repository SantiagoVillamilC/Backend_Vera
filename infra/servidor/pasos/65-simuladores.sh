#!/usr/bin/env bash
# infra/servidor/pasos/65-simuladores.sh
# Instala los tres servicios externos simulados como contenedores aparte.

NOMBRE_PASO="Instalar los servicios simulados (riel, custodio y PSE)"
TARJETA="SCRUM-114"

paso "${NOMBRE_PASO} (${TARJETA})"

DIRECTORIO_SIMULADORES="/opt/centinela/simuladores"
ARCHIVO_COMPOSE="${DIRECTORIO_SIMULADORES}/compose.yaml"
ARCHIVO_ENV="${DIRECTORIO_SIMULADORES}/.env"
ARCHIVO_PLANTILLA="${DIRECTORIO_SCRIPT}/simuladores/simuladores.yaml"
ARCHIVO_TOKEN_API="/opt/centinela/api/custodio-token.env"

# Las imágenes las publica .github/workflows/cd-simuladores.yml en cada push a main.
REGISTRO_SIMULADORES="ghcr.io/santiagovillamilc/backend_vera"
TAG_SIMULADORES="main"
PUERTO_PSE="8091"

asegurar_directorio "${DIRECTORIO_SIMULADORES}" "700"
asegurar_archivo_con_contenido "${ARCHIVO_COMPOSE}" "$(cat "${ARCHIVO_PLANTILLA}")"

# El token es el mismo que usa la API para autenticar al custodio. No se genera otro.
# shellcheck source=/dev/null
. "${ARCHIVO_TOKEN_API}"
if [[ -z "${CUSTODIO_TOKEN:-}" || "${#CUSTODIO_TOKEN}" -lt 32 ]]; then
  error_fatal "CUSTODIO_TOKEN no existe o tiene menos de 32 bytes en ${ARCHIVO_TOKEN_API}."
fi

contenido_env="$(cat <<EOF
REGISTRO=${REGISTRO_SIMULADORES}
TAG=${TAG_SIMULADORES}
PSE_HOST_PORT=${PUERTO_PSE}
CUSTODIO_TOKEN=${CUSTODIO_TOKEN}
NUCLEO_CONFIRMACION_URL=http://api:8080/v1/depositos/confirmaciones
SIMULADOR_CUSTODIA_URL=http://simulador-custodia:8099/notificaciones/depositos
EOF
)"
asegurar_archivo_con_contenido "${ARCHIVO_ENV}" "${contenido_env}"
chmod 600 "${ARCHIVO_ENV}"

if [[ ! -d /run/systemd/system ]]; then
  aviso "systemd no es el proceso 1 en este sistema (no existe /run/systemd/system): se omite por completo el arranque de los simuladores. Verifícalo en la VM real (Azure o Vagrant)."
else
  (
    cd "${DIRECTORIO_SIMULADORES}" || exit 1
    docker compose pull
  )

  for servicio in simulador-riel simulador-custodia simulador-pse; do
    asegurar_servicio_compose "${ARCHIVO_COMPOSE}" "${servicio}"
  done

  for contenedor in vera-simulador-riel vera-simulador-custodia vera-simulador-pse; do
    intentos=0
    sano="no"
    while [[ "${intentos}" -lt 30 ]]; do
      if [[ "$(docker inspect -f '{{.State.Health.Status}}' "${contenedor}" 2>/dev/null)" == "healthy" ]]; then
        sano="si"
        break
      fi
      intentos=$((intentos + 1))
      sleep 2
    done
    if [[ "${sano}" != "si" ]]; then
      error_fatal "El contenedor ${contenedor} no quedó healthy tras 60 segundos. Revisa 'docker logs ${contenedor}'."
    fi
    info "Confirmado con el healthcheck: ${contenedor} está listo."
  done
fi

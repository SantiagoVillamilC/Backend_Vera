#!/usr/bin/env bash
# infra/servidor/pasos/60-api.sh
# Instala y configura el contenedor de la API (tarjeta SCRUM-100).
#
# nginx ya esperaba "api" en la red "centinela" (30-nginx.sh), pero ningún
# paso lo creaba: "up -d api" del CD fallaba con "no such service: api".
# También fija la contraseña de rol_api (la migración 0004 lo crea sin una)
# y arma APP_DATABASE_URL con ella.

NOMBRE_PASO="Instalar y configurar el contenedor de la API"
TARJETA="SCRUM-100"

paso "${NOMBRE_PASO} (${TARJETA})"

# Tag flotante: staging y main comparten esta misma VM y el mismo comando SSH
# forzado (no hay segundo ambiente), un push a staging también redespliega
# esta imagen. Ver bitácora técnica 2026-09-24.
#
# El nombre de la imagen sigue a github.repository (cd.yml: env.IMAGE), que
# GitHub actualiza solo tras un rename de repo. Este valor NO se actualiza
# solo: el repo pasó de Backend_Centinela a Backend_Vera el 24 de sep y este
# hardcode se quedó apuntando al nombre viejo, "manifest unknown" en el
# primer deploy real aunque la imagen sí existía (bitácora técnica del mismo día).
IMAGEN_API="ghcr.io/santiagovillamilc/backend_vera:main"

DIRECTORIO_API="/opt/centinela/api"
ARCHIVO_COMPOSE_API="${DIRECTORIO_SCRIPT}/compose.api.yaml"
ARCHIVO_JWT_SECRET="${DIRECTORIO_API}/jwt-secret.env"
ARCHIVO_CONTADORES_PEPPER="${DIRECTORIO_API}/contadores-pepper.env"
ARCHIVO_OTP_PEPPER="${DIRECTORIO_API}/otp-pepper.env"
ARCHIVO_ROL_API_PASSWORD="${DIRECTORIO_API}/rol-api-password.env"
ARCHIVO_APP_DATABASE_URL="${DIRECTORIO_API}/app-database-url.env"
# El núcleo financiero exige CUSTODIO_TOKEN de al menos 32 bytes y distinto de JWT_SECRET
# (cmd/api/config.go), lo firma el custodio simulado como bearer estático.
ARCHIVO_CUSTODIO_TOKEN="${DIRECTORIO_API}/custodio-token.env"

asegurar_archivo_con_secreto "${ARCHIVO_JWT_SECRET}" "JWT_SECRET"
asegurar_archivo_con_secreto_hex "${ARCHIVO_CONTADORES_PEPPER}" "CONTADORES_PEPPER"
asegurar_archivo_con_secreto_hex "${ARCHIVO_OTP_PEPPER}" "OTP_PEPPER"
asegurar_archivo_con_secreto "${ARCHIVO_ROL_API_PASSWORD}" "ROL_API_PASSWORD"
asegurar_archivo_con_secreto "${ARCHIVO_CUSTODIO_TOKEN}" "CUSTODIO_TOKEN"

# APP_DATABASE_URL se deriva de ROL_API_PASSWORD (no es un secreto aparte).
# shellcheck source=/dev/null
. "${ARCHIVO_ROL_API_PASSWORD}"
asegurar_directorio "${DIRECTORIO_API}" "700"
printf 'APP_DATABASE_URL=postgres://rol_api:%s@postgres:5432/centinela?sslmode=disable\n' \
  "${ROL_API_PASSWORD}" > "${ARCHIVO_APP_DATABASE_URL}"
chmod 600 "${ARCHIVO_APP_DATABASE_URL}"

# Delimitador sin comillas: interpola IMAGEN_API. Los cinco archivos
# generados arriba entran por env_file (secretos, 600); todo lo que sigue en
# "environment:" es config fija, no sensible.
contenido_compose_api="$(cat <<EOF
# infra/servidor/compose.api.yaml, generado por 60-api.sh, no
# versionado como archivo estático aparte.
services:
  api:
    image: ${IMAGEN_API}
    container_name: centinela-api
    restart: unless-stopped
    env_file:
      - ${ARCHIVO_JWT_SECRET}
      - ${ARCHIVO_CONTADORES_PEPPER}
      - ${ARCHIVO_OTP_PEPPER}
      - ${ARCHIVO_APP_DATABASE_URL}
      - ${ARCHIVO_CUSTODIO_TOKEN}
    environment:
      PORT: "8080"
      PSE_URL: https://pse.veraapp.tech
      LOG_LEVEL: info
      LOG_FORMAT: json
      REDIS_URL: redis://redis:6379
      JWT_ISSUER: centinela-auth
      JWT_AUDIENCE: centinela-api
      AUTH_RESET_TOKEN_TTL: 1h
      AUTH_ENFRIAMIENTO_CAMBIO_NUMERO: 24h
      # Placeholder: el equipo tiene que reemplazarlo por un contacto real de soporte.
      AUTH_CONTACTO_SOPORTE: soporte@veraapp.tech
      AUTH_VENTANA_PENDIENTE: 24h
      AUTH_LIMPIEZA_INTERVAL: 15m
      AUTH_OUTBOX_INTERVAL: 10s
      AUTH_OUTBOX_BACKOFF_BASE: 5s
      AUTH_OUTBOX_BACKOFF_FACTOR: "2"
      AUTH_OUTBOX_BACKOFF_MAX: 5m
      AUTH_ARGON2_CONCURRENCIA: "4"
      # simulador: no exige credenciales reales de WhatsApp para que la API arranque.
      OTP_CANAL: simulador
      OTP_SIMULADOR_CODIGO_FIJO: "123456"
      KYC_PROVEEDOR: simulador
      KYC_SIMULADOR_RESULTADO: verificado
    networks:
      - centinela
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
    healthcheck:
      test: ["CMD", "wget", "-q", "-O", "/dev/null", "http://localhost:8080/healthz"]
      interval: 10s
      timeout: 5s
      retries: 5
EOF
)"
asegurar_archivo_con_contenido "${ARCHIVO_COMPOSE_API}" "${contenido_compose_api}"

if [[ ! -d /run/systemd/system ]]; then
  # Mismo criterio que el resto de los pasos de contenedor (ver README).
  aviso "systemd no es el proceso 1 en este sistema (no existe /run/systemd/system): se omite por completo el contenedor de la API. Verifícalo en la VM real (Azure o Vagrant)."
else
  docker exec centinela-postgres psql -U centinela -d centinela -v ON_ERROR_STOP=1 \
    -c "ALTER ROLE rol_api WITH PASSWORD '${ROL_API_PASSWORD}';" >/dev/null

  asegurar_servicio_compose "${ARCHIVO_COMPOSE_API}" api

  intentos_listo=0
  api_lista="no"
  while [[ "${intentos_listo}" -lt 30 ]]; do
    if [[ "$(docker inspect -f '{{.State.Health.Status}}' centinela-api 2>/dev/null)" == "healthy" ]]; then
      api_lista="si"
      break
    fi
    intentos_listo=$((intentos_listo + 1))
    sleep 2
  done

  if [[ "${api_lista}" != "si" ]]; then
    error_fatal "El contenedor centinela-api está corriendo según docker compose, pero su healthcheck (/healthz) no pasó a \"healthy\" tras 30 intentos (60 segundos). Revisa \"docker logs centinela-api\" antes de continuar."
  fi
  info "Confirmado con el healthcheck del contenedor: la API está lista (/healthz)."
fi

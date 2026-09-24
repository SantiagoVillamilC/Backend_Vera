#!/usr/bin/env bash
# infra/servidor/pasos/45-redis.sh
# Instala y configura Redis en contenedor Docker (tarjeta SCRUM-100).
#
# Dependencia dura de cmd/api: REDIS_URL sin default en config_auth.go.
# Mismo patrón que 40-postgres.sh: red interna, sin puerto publicado, sin
# password (mismo modelo de confianza que Postgres).

NOMBRE_PASO="Instalar y configurar Redis en contenedor"
TARJETA="SCRUM-100"

paso "${NOMBRE_PASO} (${TARJETA})"

# Tag de parche exacto, no minor flotante: mismo criterio que IMAGEN_POSTGRES en 40-postgres.sh.
IMAGEN_REDIS="redis:7.4.1-alpine3.20"

ARCHIVO_COMPOSE_REDIS="${DIRECTORIO_SCRIPT}/compose.redis.yaml"

# Delimitador sin comillas: interpola IMAGEN_REDIS. Solo "services: redis"
# (name/networks ya estan en compose.yaml); sin "ports:".
contenido_compose_redis="$(cat <<EOF
# infra/servidor/compose.redis.yaml, generado por 45-redis.sh, no
# versionado como archivo estático aparte.
services:
  redis:
    image: ${IMAGEN_REDIS}
    container_name: centinela-redis
    restart: unless-stopped
    networks:
      - centinela
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5
EOF
)"
asegurar_archivo_con_contenido "${ARCHIVO_COMPOSE_REDIS}" "${contenido_compose_redis}"

if [[ ! -d /run/systemd/system ]]; then
  # Mismo criterio que 20-docker.sh, 30-nginx.sh y 40-postgres.sh (ver README).
  aviso "systemd no es el proceso 1 en este sistema (no existe /run/systemd/system): se omite por completo el contenedor Redis. Verifícalo en la VM real (Azure o Vagrant)."
else
  asegurar_servicio_compose "${ARCHIVO_COMPOSE_REDIS}" redis

  intentos_listo=0
  redis_listo="no"
  while [[ "${intentos_listo}" -lt 30 ]]; do
    if docker exec centinela-redis redis-cli ping 2>/dev/null | grep -q PONG; then
      redis_listo="si"
      break
    fi
    intentos_listo=$((intentos_listo + 1))
    sleep 2
  done

  if [[ "${redis_listo}" != "si" ]]; then
    error_fatal "El contenedor centinela-redis está corriendo según docker compose, pero redis-cli ping no confirmó PONG tras 30 intentos (60 segundos). Revisa el estado del contenedor antes de continuar."
  fi
  info "Confirmado con redis-cli ping: Redis está listo dentro del contenedor."

  if command -v ss &>/dev/null; then
    if ss -tln 2>/dev/null | grep -q ':6379 '; then
      error_fatal "ss -tln muestra el puerto 6379 escuchando en el host: Redis no debería estar publicado. Revisa compose.redis.yaml."
    fi
    info "Confirmado con ss -tln: el puerto 6379 no está expuesto en el host."
  else
    aviso "ss no está disponible: no se pudo confirmar con un socket real que el puerto 6379 no esté expuesto en el host. Verifícalo a mano."
  fi
fi

#!/usr/bin/env bash
# infra/servidor/pasos/40-postgres.sh
# Instala y configura Postgres en contenedor Docker (tarjeta SCRUM-33).

NOMBRE_PASO="Instalar y configurar Postgres en contenedor"
TARJETA="SCRUM-33"

paso "${NOMBRE_PASO} (${TARJETA})"

# Tag de parche exacto, no minor flotante: no se mueve solo al publicarse 16.16.
IMAGEN_POSTGRES="postgres:16.15-alpine3.24"

ARCHIVO_COMPOSE_POSTGRES="${DIRECTORIO_SCRIPT}/compose.postgres.yaml"
DIRECTORIO_DATOS_POSTGRES="/opt/centinela/postgres/datos"
ARCHIVO_SECRETO_POSTGRES="/opt/centinela/postgres/postgres.env"

# 700 es el permiso; el chown al usuario postgres lo hace el entrypoint de la imagen.
# Ruta fuera de DIRECTORIO_SCRIPT porque /tmp se limpia en cada reinicio de Debian.
asegurar_directorio "${DIRECTORIO_DATOS_POSTGRES}" "700"

# Editarlo tras el primer arranque no cambia la contraseña ya inicializada; rotarla exige ALTER ROLE.
asegurar_archivo_con_secreto "${ARCHIVO_SECRETO_POSTGRES}" "POSTGRES_PASSWORD"

# Delimitador sin comillas: interpola las variables de arriba. Solo
# "services: postgres" (name/networks ya están en compose.yaml); sin "ports:".
contenido_compose_postgres="$(cat <<EOF
# infra/servidor/compose.postgres.yaml, generado por 40-postgres.sh, no
# versionado como archivo estático aparte.
services:
  postgres:
    image: ${IMAGEN_POSTGRES}
    container_name: centinela-postgres
    restart: unless-stopped
    environment:
      POSTGRES_USER: centinela
      POSTGRES_DB: centinela
    env_file:
      - ${ARCHIVO_SECRETO_POSTGRES}
    volumes:
      - ${DIRECTORIO_DATOS_POSTGRES}:/var/lib/postgresql/data
    networks:
      - centinela
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U centinela -d centinela"]
      interval: 10s
      timeout: 5s
      retries: 5
EOF
)"
asegurar_archivo_con_contenido "${ARCHIVO_COMPOSE_POSTGRES}" "${contenido_compose_postgres}"

if [[ ! -d /run/systemd/system ]]; then
  # Mismo criterio que 20-docker.sh y 30-nginx.sh (ver README).
  aviso "systemd no es el proceso 1 en este sistema (no existe /run/systemd/system): se omite por completo el contenedor Postgres. Verifícalo en la VM real (Azure o Vagrant)."
else
  asegurar_servicio_compose "${ARCHIVO_COMPOSE_POSTGRES}" postgres

  intentos_listo=0
  postgres_listo="no"
  while [[ "${intentos_listo}" -lt 30 ]]; do
    if docker exec centinela-postgres pg_isready -U centinela -d centinela -h localhost &>/dev/null; then
      postgres_listo="si"
      break
    fi
    intentos_listo=$((intentos_listo + 1))
    sleep 2
  done

  if [[ "${postgres_listo}" != "si" ]]; then
    error_fatal "El contenedor centinela-postgres está corriendo según docker compose, pero pg_isready no confirmó que Postgres esté listo tras 30 intentos (60 segundos). Revisa el estado del contenedor antes de continuar."
  fi
  info "Confirmado con pg_isready: Postgres está listo dentro del contenedor."

  if command -v ss &>/dev/null; then
    if ss -tln 2>/dev/null | grep -q ':5432 '; then
      error_fatal "ss -tln muestra el puerto 5432 escuchando en el host: Postgres no debería estar publicado. Revisa compose.postgres.yaml."
    fi
    info "Confirmado con ss -tln: el puerto 5432 no está expuesto en el host."
  else
    aviso "ss no está disponible: no se pudo confirmar con un socket real que el puerto 5432 no esté expuesto en el host. Verifícalo a mano."
  fi
fi

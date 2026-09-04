#!/usr/bin/env bash
# infra/servidor/pasos/20-docker.sh
# Instala Docker Engine y docker compose (SCRUM-31). Se carga con source:
# nunca "exit" para salir temprano (mataría aprovisionar.sh).

NOMBRE_PASO="Instalar Docker y Docker Compose"
TARJETA="SCRUM-31"

paso "${NOMBRE_PASO} (${TARJETA})"

# docker-cli va explícito: docker.io lo declara "Recommends", no "Depends".
instalar_paquetes docker.io docker-cli docker-compose

# centinela no entra al grupo docker: decisión de equipo pendiente.
asegurar_servicio_activo docker

if ! docker --version &>/dev/null; then
  error_fatal "Los paquetes se instalaron pero el comando 'docker' no responde a --version."
fi
info "Confirmado: $(docker --version)"

if ! docker compose version &>/dev/null; then
  error_fatal "Los paquetes se instalaron pero 'docker compose version' (el plugin, con espacio) no responde."
fi
info "Confirmado: $(docker compose version)"

if [[ ! -d /run/systemd/system ]]; then
  aviso "systemd no es el proceso 1 en este sistema: se omite la verificación del daemon de Docker. Verifícalo en la VM real (Azure o Vagrant) con: docker info"
else
  if ! docker info &>/dev/null; then
    error_fatal "El servicio docker está activo según systemd, pero 'docker info' no logra hablar con el daemon a través del socket."
  fi
  info "Confirmado con 'docker info': el daemon de Docker responde por el socket."
fi

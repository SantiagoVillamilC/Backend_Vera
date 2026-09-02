#!/usr/bin/env bash
# infra/servidor/pasos/20-docker.sh
#
# Este archivo se carga con source desde aprovisionar.sh, no se ejecuta como
# proceso aparte. Todavía no está implementado: lo implementa la tarjeta
# SCRUM-31. Este esqueleto solo declara la metadata del paso y marca que
# está pendiente.

NOMBRE_PASO="Instalar Docker y Docker Compose"
TARJETA="SCRUM-31"

paso "${NOMBRE_PASO} (${TARJETA})"
paso_pendiente "${TARJETA}"

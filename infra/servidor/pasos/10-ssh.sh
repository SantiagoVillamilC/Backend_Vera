#!/usr/bin/env bash
# infra/servidor/pasos/10-ssh.sh
#
# Este archivo se carga con source desde aprovisionar.sh, no se ejecuta como
# proceso aparte. Todavía no está implementado: lo implementa la tarjeta
# SCRUM-28. Este esqueleto solo declara la metadata del paso y marca que
# está pendiente.

NOMBRE_PASO="Endurecer el acceso por SSH"
TARJETA="SCRUM-28"

paso "${NOMBRE_PASO} (${TARJETA})"
paso_pendiente "${TARJETA}"

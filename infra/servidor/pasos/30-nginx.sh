#!/usr/bin/env bash
# infra/servidor/pasos/30-nginx.sh
#
# Este archivo se carga con source desde aprovisionar.sh, no se ejecuta como
# proceso aparte. Todavía no está implementado: lo implementa la tarjeta
# SCRUM-32. Este esqueleto solo declara la metadata del paso y marca que
# está pendiente.

NOMBRE_PASO="Instalar y configurar nginx como proxy inverso"
TARJETA="SCRUM-32"

paso "${NOMBRE_PASO} (${TARJETA})"
paso_pendiente "${TARJETA}"

#!/usr/bin/env bash
# infra/servidor/pasos/50-certbot.sh
#
# Este archivo se carga con source desde aprovisionar.sh, no se ejecuta como
# proceso aparte. Todavía no está implementado: lo implementa la tarjeta
# SCRUM-30. Este esqueleto solo declara la metadata del paso y marca que
# está pendiente.

NOMBRE_PASO="Emitir el certificado TLS con certbot y su temporizador"
TARJETA="SCRUM-30"

paso "${NOMBRE_PASO} (${TARJETA})"
paso_pendiente "${TARJETA}"

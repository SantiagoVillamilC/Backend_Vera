#!/usr/bin/env bash
# infra/servidor/dominio-fijado.sh
# Dominio del servidor. Ver infra/servidor/README.md.

: "${DOMINIO_SERVIDOR:=veraapp.tech}"

# Único nombre público de los servicios simulados: el navegador del usuario
# abre aquí la página de pago de PSE. El riel y el custodio se quedan en
# 127.0.0.1 porque sus /control no tienen autenticación.
: "${DOMINIO_PSE:=pse.${DOMINIO_SERVIDOR}}"

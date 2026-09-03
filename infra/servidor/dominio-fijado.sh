#!/usr/bin/env bash
# infra/servidor/dominio-fijado.sh
#
# Única fuente de verdad del dominio del servidor. Lo usan
# infra/servidor/pasos/30-nginx.sh (server_name) y, a futuro,
# infra/servidor/pasos/50-certbot.sh (a qué dominio emitirle el
# certificado). Mismo patrón que imagen-fijada.sh y red-fijada.sh: solo
# declaraciones "${VAR:=valor}", sin lógica.
#
# SCRUM-24 (obtener el dominio .dev) sigue en curso. Mientras no haya un
# dominio real, DOMINIO_SERVIDOR queda en "_", el comodín de nginx para
# "acepta cualquier Host" (el servidor se sirve solo por IP mientras
# tanto). Cuando SCRUM-24 cierre, este es el único archivo que hay que
# tocar.
#
# Para sobrescribir el valor sin editar este archivo:
#   DOMINIO_SERVIDOR=centinela.dev bash infra/servidor/aprovisionar.sh

: "${DOMINIO_SERVIDOR:=_}"

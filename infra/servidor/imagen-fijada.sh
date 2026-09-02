#!/usr/bin/env bash
# infra/servidor/imagen-fijada.sh
#
# Única fuente de verdad del sistema operativo y la imagen que deben correr
# los tres destinos (VM de Azure, VM local de Vagrant, contenedor de prueba):
# solo declaraciones con valores por defecto sobrescribibles por entorno, sin
# lógica. Vive dentro de infra/servidor/ a propósito: es lo único que viaja
# en el despliegue al servidor real (ver infra/servidor/README.md), así que
# aprovisionar.sh y sus pasos pueden depender de este archivo sin romper el
# despliegue. infra/azure/variables.sh, que se queda del lado de la máquina local,
# hace source de este archivo en vez de duplicar los valores.
#
# Para sobrescribir un valor sin editar este archivo:
#   IMAGEN_VERSION_ESPERADA=0.20260907.2590 bash infra/servidor/aprovisionar.sh

# URN exacta de la imagen de Azure. Verificada contra la suscripción con:
#   az vm image list --publisher Debian --offer debian-13 --sku 13-gen2 --all --location "$REGION"
# Nunca usar ":latest": Azure Marketplace poda versiones viejas de la imagen,
# y una caída silenciosa a otra versión rompería la premisa de que los tres
# destinos arrancan del mismo sistema.
: "${IMAGEN_URN:=Debian:debian-13:13-gen2:0.20260831.2587}"

# Versión exacta esperada dentro de la URN de arriba. La usa
# infra/servidor/pasos/00-verificar-sistema.sh para comparar contra lo que
# reporta el servicio de metadata de instancia de Azure.
: "${IMAGEN_VERSION_ESPERADA:=0.20260831.2587}"

# Serial de compilación de Debian detrás de esa versión de imagen. Es el
# mismo serial que publica la caja de Vagrant cloud-image/debian-13 (ver el
# Vagrantfile de la raíz del repositorio): confirma que la VM local y la VM
# de Azure parten de la misma compilación de Debian, aunque no sean el mismo
# artefacto byte a byte (la imagen de Azure trae su propia capa de
# integración con esa nube).
: "${SERIAL_IMAGEN_DEBIAN:=20260831-2587}"

# ID y VERSION_ID de /etc/os-release que debe reportar el sistema operativo
# fijado. Los usa infra/servidor/pasos/00-verificar-sistema.sh en vez de
# comparar contra los literales "debian" y "13" directamente.
: "${IMAGEN_OS_ID:=debian}"
: "${IMAGEN_OS_VERSION_ID:=13}"

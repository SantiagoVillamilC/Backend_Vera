#!/usr/bin/env bash
# infra/azure/variables.sh
#
# Variables del despliegue de Azure para el servidor de Centinela. Solo
# declaraciones con valores por defecto sobrescribibles por entorno: ningún
# script de infra/azure/ debe agregar aquí lógica ni llamadas a az. Se carga
# con source desde los scripts que lo necesiten.
#
# Para sobrescribir un valor sin editar este archivo:
#   REGION=centralus bash infra/azure/algun-script.sh

# La imagen y el sistema operativo fijados (IMAGEN_URN, IMAGEN_VERSION_ESPERADA,
# SERIAL_IMAGEN_DEBIAN, IMAGEN_OS_ID, IMAGEN_OS_VERSION_ID) NO se declaran acá:
# viven en infra/servidor/imagen-fijada.sh, que es lo único que necesita
# ambos lados de la frontera de despliegue (infra/azure/ nunca llega al
# servidor real; infra/servidor/ sí, vía rsync, ver infra/servidor/README.md).
# Se resuelve con BASH_SOURCE, no con una ruta relativa al directorio de
# trabajo, para que este archivo funcione sin importar desde dónde se invoque.
DIRECTORIO_AZURE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUTA_IMAGEN_FIJADA="${DIRECTORIO_AZURE}/../servidor/imagen-fijada.sh"

# Falla con un mensaje legible en vez de un error crudo de bash si algún día
# infra/azure/ se copia o se mueve sin infra/servidor/: quien llame a este
# archivo puede o no tener "set -e" activo, así que no basta con dejar que el
# source falle solo.
if [[ ! -r "$RUTA_IMAGEN_FIJADA" ]]; then
  printf '[error] No se encontró %s\n' "$RUTA_IMAGEN_FIJADA" >&2
  # Falso positivo de SC2317: shellcheck asume que "return" siempre corta el
  # flujo, así que da "exit 1" por inalcanzable. Pero si este archivo se
  # ejecuta directo (no con source), "return" fuera de una función falla en
  # tiempo de ejecución en vez de cortar el flujo, y "|| exit 1" sí corre.
  # shellcheck disable=SC2317
  return 1 2>/dev/null || exit 1
fi

# shellcheck source=../servidor/imagen-fijada.sh
source "${RUTA_IMAGEN_FIJADA}"

# --- Región y tamaño ---
#
# northcentralus está permitida por la política "Allowed resource deployment
# regions" de la suscripción (verificado con az policy assignment list). La
# lista completa de regiones permitidas es: chilecentral, northcentralus,
# southcentralus, brazilsouth y centralus.
: "${REGION:=northcentralus}"

# Standard_B1ms: 1 vCPU, 2 GB de RAM. Refleja el tamaño real del servidor.
# La cuota de la suscripción en esta región es 6 vCPU regionales y 4 de la
# familia Standard BS (verificado con az vm list-usage); un B1ms consume 1
# vCPU de cada cupo.
: "${SKU_VM:=Standard_B1ms}"

: "${TAMANO_DISCO_GB:=16}"

# --- Nombres de recursos ---
#
# Todos derivados de un mismo prefijo configurable, y todos en un solo grupo
# de recursos: mover recursos entre suscripciones exige que sus dependencias
# se muevan juntas, así que repartirlos en varios grupos solo complicaría un
# eventual traslado sin ganar nada a cambio.
: "${PREFIJO_RECURSOS:=centinela}"

: "${GRUPO_RECURSOS:=${PREFIJO_RECURSOS}-rg}"
: "${NOMBRE_VM:=${PREFIJO_RECURSOS}-vm}"
: "${NOMBRE_RED_VIRTUAL:=${PREFIJO_RECURSOS}-vnet}"
: "${NOMBRE_SUBRED:=${PREFIJO_RECURSOS}-subnet}"
: "${NOMBRE_GRUPO_SEGURIDAD:=${PREFIJO_RECURSOS}-nsg}"
: "${NOMBRE_IP_PUBLICA:=${PREFIJO_RECURSOS}-ip}"

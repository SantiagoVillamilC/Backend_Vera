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

# Standard_B2als_v2: 2 vCPU, 4 GB de RAM, arquitectura x64/AMD. No es una
# elección de diseño: Standard_B1ms existe en la región pero está
# "NotAvailableForSubscription" para esta suscripción (verificado con
# az vm list-skus -l northcentralus --size Standard_B1 --all), igual que
# B1s y B1ls. La suscripción solo ofrece la generación Bv2, y su tamaño
# más chico ya es de 2 vCPU.
#
# Se descartaron los SKU con "p" (B2pls_v2, B2ps_v2): son ARM64. Además de
# romper la paridad amd64 con la VM local (ver el Vagrantfile de la raíz) y
# con la imagen fijada de infra/servidor/imagen-fijada.sh, cuestan 14 % más
# a especificaciones iguales (0.0428 contra 0.0376 USD/h).
#
# Se descartó B2ats_v2 (2 vCPU, 1 GB): Postgres más el demonio de Docker no
# caben en 1 GB de RAM.
#
# Se descartaron los B4: la cuota de la familia Standard BS es de 4 vCPU, y
# un B4 la consumiría entera, cerrando la puerta a la VM de medición que
# anticipa la propuesta del tablero de plataforma (fuera de este
# repositorio, sección "La trampa que sí afecta la tesis"): esa VM también
# sale de la misma familia y necesita cupo libre.
#
# La cuota de la suscripción en esta región es 6 vCPU regionales y 4 de la
# familia Standard BS (verificado con az vm list-usage); un B2als_v2 consume
# 2 de cada cupo, contra 1 que consumía el B1ms anterior.
: "${SKU_VM:=Standard_B2als_v2}"

# 30 GiB es el disco propio de la imagen de Debian, medido creando un disco
# desde la referencia de la imagen (az disk create --image-reference) y
# leyendo diskSizeGB. --os-disk-size-gb solo agranda, nunca achica, así que
# 30 es un piso que impone el editor de la imagen, no una elección propia.
# Debian instalado ocupa unos 700 MB: lo que mide 30 GiB es el disco, no el
# contenido.
: "${TAMANO_DISCO_GB:=30}"

# Sin especificarlo, el CLI crea el disco como Premium_LRS, que en el
# escalón P4 cuesta 5.28 USD/mes contra 2.40 del Standard SSD (E4). Se
# descartó Standard HDD (S4, 1.54 USD/mes) porque agregarle latencia de
# disco giratorio a Postgres sería un segundo factor confundido en las
# mediciones de la tesis, además del hecho ya conocido de que la instancia
# es burstable.
: "${SKU_DISCO:=StandardSSD_LRS}"

# --- Acceso administrativo ---
#
# Azure rechaza nombres de usuario administrador reservados: admin,
# administrator, root y guest.
: "${USUARIO_ADMIN:=centinela}"

# Lista separada por espacios de archivos de llave pública SSH. Solo la del
# operador que ejecuta crear-servidor.sh por defecto: las llaves del resto
# del equipo se agregan en SCRUM-28, y agregar una llave después no exige
# recrear la VM, así que no bloquea la creación de hoy. Nota: "${HOME}" se
# expande al momento del source; si alguien sobrescribe esta variable con
# "~" en vez de una ruta absoluta, bash no la expande dentro de comillas.
: "${LLAVES_SSH_PUBLICAS:=${HOME}/.ssh/id_ed25519.pub}"

# ORIGEN_SSH acota de dónde se acepta SSH (puerto 22) en la regla del NSG.
# No tiene valor por defecto a propósito: crear-servidor.sh se niega a
# ejecutar si está vacía, porque un default abierto es un default inseguro
# que la prisa termina aceptando en producción. Para definirla, obtén tu IP
# pública con "curl -fsS https://ifconfig.me" y ejecuta:
#   ORIGEN_SSH=$(curl -fsS https://ifconfig.me) bash infra/azure/crear-servidor.sh
# Acepta una IP individual, un rango CIDR, o varios valores separados por
# espacio. Salida de escape deliberada para casos muy concretos:
#   PERMITIR_SSH_ABIERTO=si bash infra/azure/crear-servidor.sh
# Este patrón es el mismo que OMITIR_VERIFICACION_SISTEMA en
# infra/servidor/pasos/00-verificar-sistema.sh. SCRUM-28 endurece el acceso
# de forma definitiva.
: "${ORIGEN_SSH:=}"

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
: "${NOMBRE_NIC:=${PREFIJO_RECURSOS}-nic}"

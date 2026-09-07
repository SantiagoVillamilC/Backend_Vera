#!/usr/bin/env bash
# Variables del despliegue de Azure: solo declaraciones, sin llamadas a az.
# Sobrescribir sin editar: REGION=centralus bash infra/azure/algun-script.sh

# IMAGEN_* y PUERTO_SSH viven en infra/servidor/ (ver README), cargados vía BASH_SOURCE.
DIRECTORIO_AZURE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUTA_IMAGEN_FIJADA="${DIRECTORIO_AZURE}/../servidor/imagen-fijada.sh"

# Falla legible si infra/azure/ se separa de infra/servidor/ (el caller puede no tener set -e).
if [[ ! -r "$RUTA_IMAGEN_FIJADA" ]]; then
  printf '[error] No se encontró %s\n' "$RUTA_IMAGEN_FIJADA" >&2
  # "return" fuera de función no corta el flujo si el script corre sin source.
  # shellcheck disable=SC2317
  return 1 2>/dev/null || exit 1
fi

# shellcheck source=../servidor/imagen-fijada.sh
source "${RUTA_IMAGEN_FIJADA}"

RUTA_RED_FIJADA="${DIRECTORIO_AZURE}/../servidor/red-fijada.sh"

if [[ ! -r "$RUTA_RED_FIJADA" ]]; then
  printf '[error] No se encontró %s\n' "$RUTA_RED_FIJADA" >&2
  # shellcheck disable=SC2317
  return 1 2>/dev/null || exit 1
fi

# shellcheck source=../servidor/red-fijada.sh
source "${RUTA_RED_FIJADA}"

# --- Región y tamaño ---
: "${REGION:=northcentralus}"

# x64/AMD, no ARM: debe coincidir con la imagen fijada y la VM local de Vagrant.
: "${SKU_VM:=Standard_B2als_v2}"

: "${TAMANO_DISCO_GB:=30}"
: "${SKU_DISCO:=StandardSSD_LRS}"

# --- Acceso administrativo ---
# Azure rechaza como usuario admin: admin, administrator, root, guest.
: "${USUARIO_ADMIN:=centinela}"

# "${HOME}" se expande al hacer source; con "~" bash no lo expande entre comillas.
: "${LLAVES_SSH_PUBLICAS:=${HOME}/.ssh/id_ed25519.pub}"

# Origen SSH del NSG (CIDR). Por defecto "*" (abierto a todo internet) a
# propósito, ya es una decisión tomada: el equipo se conecta desde IPs
# residenciales que rotan, sin una IP fija que pinear y todavía sin VPN ni
# Tailscale montados. La protección real contra acceso no autorizado no es
# el origen de esta regla, es la autenticación exclusiva por llave SSH que
# fija infra/servidor/pasos/10-ssh.sh (SCRUM-28: PasswordAuthentication no).
# Sigue siendo sobreescribible por corrida para quien sí tenga una IP fija:
# ORIGEN_SSH=$(curl -fsS https://ifconfig.me) bash infra/azure/crear-servidor.sh
: "${ORIGEN_SSH:=*}"

# --- Nombres de recursos ---
# Todos derivados de un mismo prefijo, en un solo grupo de recursos.
: "${PREFIJO_RECURSOS:=centinela}"

: "${GRUPO_RECURSOS:=${PREFIJO_RECURSOS}-rg}"
: "${NOMBRE_VM:=${PREFIJO_RECURSOS}-vm}"
: "${NOMBRE_RED_VIRTUAL:=${PREFIJO_RECURSOS}-vnet}"
: "${NOMBRE_SUBRED:=${PREFIJO_RECURSOS}-subnet}"
: "${NOMBRE_GRUPO_SEGURIDAD:=${PREFIJO_RECURSOS}-nsg}"
: "${NOMBRE_IP_PUBLICA:=${PREFIJO_RECURSOS}-ip}"
: "${NOMBRE_NIC:=${PREFIJO_RECURSOS}-nic}"
: "${NOMBRE_LOGIC_REINICIAR:=${PREFIJO_RECURSOS}-reiniciar}"

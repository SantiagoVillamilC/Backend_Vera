#!/usr/bin/env bash
# infra/local/preparar-entorno.sh
#
# Diagnostica (o, con --instalar, instala) las herramientas que necesita un
# integrante del equipo para trabajar en Backend_Centinela: Go (el backend),
# Docker con el plugin compose (entorno local y Postgres en contenedor), el
# linter shellcheck (revisar los scripts de infra/ antes del PR), Vagrant con
# su proveedor de virtualización (VM local para probar el aprovisionamiento) y
# Azure CLI (los scripts de infra/azure/).
#
# Distribuciones soportadas: Arch Linux y Debian/Ubuntu. Windows queda fuera de
# este script a propósito: un .sh no le sirve. En Windows la vía es abrir una
# distribución WSL2 (donde este mismo script corre como si fuera Debian o
# Ubuntu) o instalar cada herramienta a mano; ver infra/servidor/README.md.
#
# Este script solo instala el proveedor libvirt/KVM: es lo que corresponde a
# un anfitrión Linux (está en el árbol del kernel, no necesita módulos DKMS
# ni repositorios de terceros). VirtualBox solo hace falta en anfitriones
# Windows, que no pasan por este script: ahí se instala con el instalador de
# Windows y se usa directo desde el Vagrantfile de la raíz.
#
# Por defecto solo diagnostica: revisa qué hay, qué falta, e imprime el comando
# exacto de instalación para la distribución detectada. No instala nada sin que
# se pida explícitamente con --instalar. Nunca ejecuta sudo en silencio: el
# comando que necesita sudo se muestra completo antes de ejecutarse.
set -euo pipefail

MODO_INSTALAR="no"
HUBO_FALLO=0
# El proveedor de Vagrant es libvirt/KVM en las dos distribuciones que cubre
# este script (ver el comentario de cabecera): no hay rama por distro para
# esta etiqueta.
ETIQUETA_PROVEEDOR="Proveedor Vagrant (libvirt/KVM)"

paso() { printf '==> %s\n' "$1"; }
info() { printf '    %s\n' "$1"; }
aviso() { printf '[aviso] %s\n' "$1"; }
error_fatal() { printf '[error] %s\n' "$1" >&2; exit 1; }

comando_existe() {
  command -v "$1" &>/dev/null
}

# Ejecuta el comando pasado como "$@" y traduce su código de salida a "si" o
# "no" sin que un resultado negativo dispare set -e: esta función siempre
# termina con un printf que sale con estado 0.
estado_si_no() {
  if "$@"; then
    printf 'si'
  else
    printf 'no'
  fi
}

mostrar_ayuda() {
  cat <<'EOF'
Uso: preparar-entorno.sh [opción]

Opciones:
  --instalar   Instala las herramientas que falten, además de diagnosticar.
  --ayuda      Muestra esta ayuda.

Sin opciones, solo diagnostica: revisa qué herramientas hay y cuáles faltan, e
imprime el comando exacto de instalación para la distribución detectada. No
instala nada.

Distribuciones soportadas: Arch Linux y Debian/Ubuntu. En Windows, usa WSL2 y
ejecuta este script dentro de la distribución Linux de WSL, o instala cada
herramienta a mano (ver infra/servidor/README.md).
EOF
}

# Lee /etc/os-release en un subshell para no contaminar las variables de este
# script con el ID/ID_LIKE del sistema. Devuelve "arch", "debian" o
# "desconocida" por stdout.
detectar_distribucion() {
  if [[ ! -r /etc/os-release ]]; then
    printf 'desconocida'
    return 0
  fi

  local id id_like
  # /etc/os-release no está en este repositorio: shellcheck nunca puede
  # seguirlo, así que se desactiva SC1091 en vez de dejarlo sin directiva.
  # shellcheck disable=SC1091
  id="$(. /etc/os-release && printf '%s' "${ID:-}")"
  # shellcheck disable=SC1091
  id_like="$(. /etc/os-release && printf '%s' "${ID_LIKE:-}")"

  case "$id" in
    arch) printf 'arch' ;;
    debian | ubuntu) printf 'debian' ;;
    *)
      if [[ "$id_like" == *debian* ]]; then
        printf 'debian'
      else
        printf 'desconocida'
      fi
      ;;
  esac
}

# En Arch, Vagrant y Azure CLI se resuelven distinto: Azure CLI está en el
# repositorio oficial "extra" (verificado con pacman -Si azure-cli), pero
# Vagrant solo está en AUR (pacman -Si vagrant falla). Por eso solo Vagrant
# necesita un ayudante de AUR.
ayudante_aur() {
  if comando_existe yay; then
    printf 'yay'
  elif comando_existe paru; then
    printf 'paru'
  else
    printf ''
  fi
}

verificar_go() { comando_existe go; }
verificar_docker() { comando_existe docker; }
verificar_docker_compose() { comando_existe docker && docker compose version &>/dev/null; }
verificar_shellcheck() { comando_existe shellcheck; }
verificar_vagrant() { comando_existe vagrant; }
verificar_az() { comando_existe az; }
verificar_kvm() { [[ -e /dev/kvm ]]; }

verificar_proveedor() {
  local distro="$1"
  if [[ "$distro" == "arch" ]]; then
    pacman -Qi libvirt &>/dev/null && pacman -Qi qemu-desktop &>/dev/null && pacman -Qi dnsmasq &>/dev/null
  else
    dpkg -s libvirt-daemon-system &>/dev/null && dpkg -s qemu-system-x86 &>/dev/null && dpkg -s dnsmasq &>/dev/null
  fi
}

comando_instalar_go() {
  if [[ "$1" == "arch" ]]; then
    printf 'sudo pacman -S --needed --noconfirm go'
  else
    printf 'sudo apt-get install -y golang-go'
  fi
}

# En Debian 12 no existe un paquete "docker-compose-v2": apt-cache policy
# confirma que el repositorio principal solo trae docker-compose (v1,
# obsoleto). Por eso en Debian/Ubuntu se usa el script de conveniencia oficial
# de Docker (get.docker.com), que instala docker-ce, containerd y el plugin
# docker-compose-plugin (v2) juntos, y ya habilita el servicio por su cuenta.
# Verificado de punta a punta en un contenedor debian:12: "docker compose
# version" respondió "Docker Compose version v5.5.0" tras la instalación.
comando_instalar_docker() {
  if [[ "$1" == "arch" ]]; then
    printf 'sudo pacman -S --needed --noconfirm docker docker-compose && sudo systemctl enable --now docker.service'
  else
    printf 'curl -fsSL https://get.docker.com | sudo sh'
  fi
}

comando_instalar_shellcheck() {
  if [[ "$1" == "arch" ]]; then
    printf 'sudo pacman -S --needed --noconfirm shellcheck'
  else
    printf 'sudo apt-get install -y shellcheck'
  fi
}

comando_instalar_vagrant() {
  local distro="$1"
  local ayudante

  if [[ "$distro" != "arch" ]]; then
    printf 'sudo apt-get install -y vagrant'
    return 0
  fi

  ayudante="$(ayudante_aur)"
  if [[ -n "$ayudante" ]]; then
    printf '%s -S vagrant  (AUR, sin --noconfirm: revisa el PKGBUILD que te muestre)' "$ayudante"
  else
    printf 'Instala un ayudante de AUR (yay o paru) y luego ejecuta: <ayudante> -S vagrant'
  fi
}

# El proveedor es libvirt/KVM en las dos distribuciones: está en el árbol del
# kernel, no necesita módulos DKMS ni repositorios de terceros (a diferencia
# de VirtualBox, que en Debian/Ubuntu exigiría agregar el repositorio de
# Oracle). Nombres de paquete verificados en un contenedor debian:13 limpio:
# libvirt-daemon-system, qemu-system-x86 y dnsmasq instalan sin problema, la
# unidad systemd sigue llamándose libvirtd.service, y vagrant-libvirt
# (empaquetado en Debian, a diferencia de Arch) queda registrado como plugin
# de sistema: "vagrant plugin list" lo muestra como "vagrant-libvirt
# (versión, system)". El "&& sudo systemctl enable --now libvirtd.service"
# del final no se pudo verificar en contenedor: sin systemd como PID 1,
# systemd 257 (Debian 13) rechaza "--now" y el comando completo sale con
# error, aunque los paquetes ya hayan quedado instalados. En una máquina real
# con systemd corriendo, ese tramo se ejecuta sin problema.
comando_instalar_proveedor() {
  if [[ "$1" == "arch" ]]; then
    printf 'sudo pacman -S --needed --noconfirm libvirt qemu-desktop dnsmasq && sudo systemctl enable --now libvirtd.service'
  else
    printf 'sudo apt-get install -y libvirt-daemon-system qemu-system-x86 dnsmasq vagrant-libvirt && sudo systemctl enable --now libvirtd.service'
  fi
}

comando_instalar_az() {
  if [[ "$1" == "arch" ]]; then
    printf 'sudo pacman -S --needed --noconfirm azure-cli'
  else
    printf 'curl -sL https://aka.ms/InstallAzureCLIDeb | sudo bash'
  fi
}

imprimir_fila() {
  local nombre="$1" presente="$2" comando="$3"
  local etiqueta
  if [[ "$presente" == "si" ]]; then
    etiqueta="presente"
  else
    etiqueta="falta"
  fi
  printf '    %-32s %-10s %s\n' "$nombre" "$etiqueta" "$comando"
}

diagnosticar_kvm() {
  if verificar_kvm; then
    info "Aceleración KVM (/dev/kvm): disponible."
  else
    aviso "Aceleración KVM (/dev/kvm) no está disponible. El proveedor libvirt la necesita: revisa que la virtualización esté activada en la BIOS/UEFI y que el módulo kvm esté cargado."
  fi
}

diagnosticar() {
  local distro="$1"
  local presente

  paso "Diagnóstico de entorno (${distro})"

  presente="$(estado_si_no verificar_go)"
  imprimir_fila "Go" "$presente" "$(comando_instalar_go "$distro")"

  presente="$(estado_si_no verificar_docker)"
  imprimir_fila "Docker" "$presente" "$(comando_instalar_docker "$distro")"

  presente="$(estado_si_no verificar_docker_compose)"
  imprimir_fila "Docker Compose (plugin)" "$presente" "$(comando_instalar_docker "$distro")"

  presente="$(estado_si_no verificar_shellcheck)"
  imprimir_fila "shellcheck" "$presente" "$(comando_instalar_shellcheck "$distro")"

  presente="$(estado_si_no verificar_vagrant)"
  imprimir_fila "Vagrant" "$presente" "$(comando_instalar_vagrant "$distro")"

  presente="$(estado_si_no verificar_proveedor "$distro")"
  imprimir_fila "$ETIQUETA_PROVEEDOR" "$presente" "$(comando_instalar_proveedor "$distro")"

  presente="$(estado_si_no verificar_az)"
  imprimir_fila "Azure CLI (az)" "$presente" "$(comando_instalar_az "$distro")"

  diagnosticar_kvm
}

# Ejecuta el comando de instalación mostrándolo primero, para que sudo nunca
# se invoque en silencio: el integrante ve el comando completo (con sudo
# incluido cuando hace falta) antes de que corra.
ejecutar_instalacion() {
  local descripcion="$1"
  local comando="$2"

  info "Ejecutando: ${comando}"
  if bash -c "$comando"; then
    return 0
  fi
  aviso "Falló la instalación de ${descripcion}."
  return 1
}

instalar_go() {
  local distro="$1"
  if verificar_go; then
    info "Go ya está instalado."
    return 0
  fi
  ejecutar_instalacion "Go" "$(comando_instalar_go "$distro")" || HUBO_FALLO=1
}

instalar_docker() {
  local distro="$1"
  if verificar_docker && verificar_docker_compose; then
    info "Docker y el plugin compose ya están instalados."
    return 0
  fi
  if ejecutar_instalacion "Docker" "$(comando_instalar_docker "$distro")"; then
    info "No se modifica el grupo docker del usuario. Para ejecutar docker sin sudo: sudo usermod -aG docker \$USER (requiere cerrar sesión)."
  else
    HUBO_FALLO=1
  fi
}

instalar_shellcheck() {
  local distro="$1"
  if verificar_shellcheck; then
    info "shellcheck ya está instalado."
    return 0
  fi
  ejecutar_instalacion "shellcheck" "$(comando_instalar_shellcheck "$distro")" || HUBO_FALLO=1
}

instalar_vagrant() {
  local distro="$1"
  local ayudante

  if verificar_vagrant; then
    info "Vagrant ya está instalado."
    return 0
  fi

  if [[ "$distro" != "arch" ]]; then
    ejecutar_instalacion "Vagrant" "$(comando_instalar_vagrant "$distro")" || HUBO_FALLO=1
    return 0
  fi

  ayudante="$(ayudante_aur)"
  if [[ -z "$ayudante" ]]; then
    aviso "No se encontró un ayudante de AUR (yay o paru). Instálalo y ejecuta: <ayudante> -S vagrant. Vagrant no se instaló."
    HUBO_FALLO=1
    return 0
  fi
  ejecutar_instalacion "Vagrant" "${ayudante} -S vagrant" || HUBO_FALLO=1
}

# Se llama para las dos distribuciones, pero en la práctica solo actúa en
# Arch: en Debian/Ubuntu, comando_instalar_proveedor ya instala vagrant-libvirt
# como paquete apt, y "vagrant plugin list" lo muestra registrado como
# "vagrant-libvirt (versión, system)" apenas se instala, así que esta función
# encuentra el plugin listo y no hace nada. En Arch no existe ese paquete: el
# plugin se instala con el gestor de plugins propio de Vagrant.
instalar_plugin_libvirt() {
  if ! verificar_vagrant; then
    aviso "Vagrant no está instalado, se omite el plugin vagrant-libvirt."
    return 0
  fi
  if vagrant plugin list 2>/dev/null | grep -q '^vagrant-libvirt '; then
    info "El plugin vagrant-libvirt ya está instalado."
    return 0
  fi
  ejecutar_instalacion "plugin vagrant-libvirt" "vagrant plugin install vagrant-libvirt" || HUBO_FALLO=1
}

instalar_proveedor() {
  local distro="$1"

  if verificar_proveedor "$distro"; then
    info "El proveedor de Vagrant ya está listo."
  else
    ejecutar_instalacion "$ETIQUETA_PROVEEDOR" "$(comando_instalar_proveedor "$distro")" || HUBO_FALLO=1
  fi

  instalar_plugin_libvirt
  diagnosticar_kvm
}

instalar_az() {
  local distro="$1"
  if verificar_az; then
    info "Azure CLI ya está instalado."
    return 0
  fi
  ejecutar_instalacion "Azure CLI" "$(comando_instalar_az "$distro")" || HUBO_FALLO=1
}

instalar_todo() {
  local distro="$1"

  paso "Instalando herramientas faltantes (${distro})"
  instalar_go "$distro"
  instalar_docker "$distro"
  instalar_shellcheck "$distro"
  instalar_vagrant "$distro"
  instalar_proveedor "$distro"
  instalar_az "$distro"

  paso "Resultado de la instalación"
  if [[ "$HUBO_FALLO" -eq 0 ]]; then
    info "Todas las herramientas quedaron instaladas o ya estaban presentes."
  else
    aviso "Alguna herramienta no se pudo instalar. Revisa los avisos anteriores."
  fi
}

principal() {
  local opcion="${1:-}"

  case "$opcion" in
    --ayuda | -h)
      mostrar_ayuda
      return 0
      ;;
    "") ;;
    --instalar)
      MODO_INSTALAR="si"
      ;;
    *)
      error_fatal "Opción desconocida: ${opcion}. Usa --ayuda para ver las opciones disponibles."
      ;;
  esac

  local distro
  distro="$(detectar_distribucion)"

  if [[ "$distro" == "desconocida" ]]; then
    aviso "No se reconoce la distribución (revisa /etc/os-release). Este script solo soporta Arch Linux y Debian/Ubuntu."
    info "Herramientas que hacen falta instalar a mano: Go, Docker con el plugin compose, shellcheck, Vagrant con su proveedor de virtualización y Azure CLI (az)."
    return 0
  fi

  if [[ "$MODO_INSTALAR" == "si" ]]; then
    instalar_todo "$distro"
  else
    diagnosticar "$distro"
  fi
}

principal "$@"

if [[ "$MODO_INSTALAR" == "si" ]]; then
  exit "$HUBO_FALLO"
fi
exit 0

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
# El proveedor no se limita a los paquetes: también verifica (e instala) lo
# que "vagrant up --provider=libvirt" necesita para funcionar de verdad
# (grupo libvirt del usuario, socket/servicio activo, red "default" activa,
# plugin vagrant-libvirt). Ver la sección "Proveedor libvirt/KVM" más abajo.
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
# comando que necesita sudo se muestra completo antes de ejecutarse, o el
# script dice explícitamente que lo va a pedir.
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

# El usuario que ejecuta el script, no root: "vagrant up --provider=libvirt"
# lo corre el integrante desde su propia cuenta.
verificar_grupo_libvirt() {
  id -nG | grep -qw libvirt
}

# libvirtd.socket (activación por socket) es la unidad recomendada en systemd
# moderno; libvirtd.service es el respaldo en distribuciones que todavía no
# la traen. Ninguna de las dos existe hasta que se instala el proveedor.
detectar_unidad_libvirt() {
  if systemctl list-unit-files libvirtd.socket --no-legend 2>/dev/null | grep -q .; then
    printf 'libvirtd.socket'
  elif systemctl list-unit-files libvirtd.service --no-legend 2>/dev/null | grep -q .; then
    printf 'libvirtd.service'
  else
    printf ''
  fi
}

verificar_socket_libvirt() {
  local unidad
  unidad="$(detectar_unidad_libvirt)"
  [[ -n "$unidad" ]] && systemctl is-active --quiet "$unidad"
}

# Sin privilegios, solo lo intenta si el usuario ya está en el grupo libvirt.
# No se decide mirando el texto de error de virsh: se probó en un contenedor
# sin D-Bus del sistema y el mensaje fue "Unable to get system bus
# connection", no un "Permission denied" reconocible; el texto además puede
# venir traducido según el idioma del sistema. El grupo es la señal
# confiable, no depende de idioma ni de qué tan roto esté el entorno.
verificar_red_default() {
  local activa
  # "|| true" para que un virsh que falla (red inexistente, sin conexión) no
  # dispare set -e por sí solo: esta asignación tiene que poder seguir hasta
  # el "[[ ]]" de abajo pase lo que pase.
  activa="$(virsh -c qemu:///system net-info default 2>/dev/null | awk '/^Active:/ {print $2}' || true)"
  [[ "$activa" == "yes" ]]
}

verificar_plugin_libvirt() {
  comando_existe vagrant && vagrant plugin list 2>/dev/null | grep -q '^vagrant-libvirt '
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
# libvirt-daemon-system, qemu-system-x86 y dnsmasq instalan sin problema, y
# vagrant-libvirt (empaquetado en Debian, a diferencia de Arch) queda
# registrado como plugin de sistema: "vagrant plugin list" lo muestra como
# "vagrant-libvirt (versión, system)". Esta función ya NO habilita el
# servicio: eso lo cubre comando_instalar_socket_libvirt, como un paso propio
# y verificable aparte.
comando_instalar_proveedor() {
  if [[ "$1" == "arch" ]]; then
    printf 'sudo pacman -S --needed --noconfirm libvirt qemu-desktop dnsmasq'
  else
    printf 'sudo apt-get install -y libvirt-daemon-system qemu-system-x86 dnsmasq vagrant-libvirt'
  fi
}

# "id -un" en vez de "$USER": en un entorno mínimo (por ejemplo, un
# contenedor sin sesión de login) $USER puede llegar vacío aunque el usuario
# real sí exista, y "usermod -aG libvirt ''" fallaría con un nombre vacío.
usuario_actual() {
  id -un
}

comando_instalar_grupo_libvirt() {
  if getent group libvirt &>/dev/null; then
    printf 'sudo usermod -aG libvirt "%s"' "$(usuario_actual)"
  else
    printf 'sudo usermod -aG libvirt "%s"  (el grupo lo crea el paquete del proveedor: instálalo primero)' "$(usuario_actual)"
  fi
}

comando_instalar_socket_libvirt() {
  local unidad
  unidad="$(detectar_unidad_libvirt)"
  if [[ -z "$unidad" ]]; then
    printf '(depende de que el proveedor esté instalado) sudo systemctl enable --now libvirtd.socket'
  else
    printf 'sudo systemctl enable --now %s' "$unidad"
  fi
}

comando_instalar_red_default() {
  printf 'sudo virsh net-start default && sudo virsh net-autostart default'
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

# Para filas que dependen de un privilegio o de otro paso: ni "presente" ni
# "falta" son honestos, así que se marcan aparte en vez de forzarlas a "falta".
imprimir_fila_incierta() {
  local nombre="$1" motivo="$2"
  printf '    %-32s %-10s %s\n' "$nombre" "sin verificar" "$motivo"
}

diagnosticar_kvm() {
  if verificar_kvm; then
    info "Aceleración KVM (/dev/kvm): disponible."
  else
    aviso "Aceleración KVM (/dev/kvm) no está disponible. El proveedor libvirt la necesita: revisa que la virtualización esté activada en la BIOS/UEFI y que el módulo kvm esté cargado."
  fi
}

diagnosticar_grupo_libvirt() {
  local presente
  presente="$(estado_si_no verificar_grupo_libvirt)"
  imprimir_fila "Grupo libvirt (usuario)" "$presente" "$(comando_instalar_grupo_libvirt)"
}

diagnosticar_socket_libvirt() {
  local unidad presente
  unidad="$(detectar_unidad_libvirt)"
  if [[ -z "$unidad" ]]; then
    imprimir_fila "Socket/servicio libvirtd" "no" "$(comando_instalar_socket_libvirt)"
    return 0
  fi
  presente="$(estado_si_no verificar_socket_libvirt)"
  imprimir_fila "Socket/servicio libvirtd (${unidad})" "$presente" "$(comando_instalar_socket_libvirt)"
}

diagnosticar_red_default() {
  if ! comando_existe virsh; then
    imprimir_fila "Red libvirt default" "no" "(depende de que el proveedor esté instalado)"
    return 0
  fi
  if ! verificar_grupo_libvirt; then
    imprimir_fila_incierta "Red libvirt default" "sin privilegios: el usuario no está en el grupo libvirt"
    return 0
  fi
  local presente
  presente="$(estado_si_no verificar_red_default)"
  imprimir_fila "Red libvirt default" "$presente" "$(comando_instalar_red_default)"
}

diagnosticar_plugin_libvirt() {
  if ! verificar_vagrant; then
    imprimir_fila "Plugin vagrant-libvirt" "no" "(depende de que Vagrant esté instalado)"
  else
    local presente
    presente="$(estado_si_no verificar_plugin_libvirt)"
    imprimir_fila "Plugin vagrant-libvirt" "$presente" "vagrant plugin install vagrant-libvirt  (sin sudo)"
  fi
  info "El plugin vagrant-libvirt compila extensiones nativas al instalarse: es el paso del proveedor que más se rompe."
}

# Verificación final de que "vagrant up --provider=libvirt" tiene chance de
# funcionar de verdad: los paquetes instalados no bastan. Reporta cada item
# por separado, nunca con un mensaje global, en el orden que pidió el equipo:
# grupo, socket, red, plugin.
verificar_usabilidad_proveedor() {
  paso "¿El proveedor libvirt/KVM queda usable para \"vagrant up\"?"

  if verificar_grupo_libvirt; then
    info "Grupo libvirt: el usuario ya pertenece."
  else
    aviso "Grupo libvirt: el usuario todavía NO pertenece. Sin esto, vagrant-libvirt falla con un error de permisos que no menciona el grupo. Cierra sesión y vuelve a entrar (o usa 'newgrp libvirt' en la terminal donde vayas a ejecutar vagrant)."
  fi

  if ! comando_existe virsh; then
    aviso "Socket/servicio libvirtd: no se puede verificar, el proveedor no está instalado."
  elif verificar_socket_libvirt; then
    info "Socket/servicio libvirtd: activo."
  else
    aviso "Socket/servicio libvirtd: inactivo. \"vagrant up\" va a fallar hasta que se active."
  fi

  if ! comando_existe virsh; then
    aviso "Red libvirt default: no se puede verificar, el proveedor no está instalado."
  elif ! verificar_grupo_libvirt; then
    aviso "Red libvirt default: no se pudo verificar sin privilegios (el usuario no está en el grupo libvirt)."
  elif verificar_red_default; then
    info "Red libvirt default: activa."
  else
    aviso "Red libvirt default: inactiva. \"vagrant up\" va a fallar con \"Network default is not active\" hasta que se active."
  fi

  if ! verificar_vagrant; then
    aviso "Plugin vagrant-libvirt: no se puede verificar, Vagrant no está instalado."
  elif verificar_plugin_libvirt; then
    info "Plugin vagrant-libvirt: instalado."
  else
    aviso "Plugin vagrant-libvirt: falta. \"vagrant up --provider=libvirt\" no funciona sin él."
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

  diagnosticar_grupo_libvirt
  diagnosticar_socket_libvirt
  diagnosticar_red_default
  diagnosticar_plugin_libvirt

  presente="$(estado_si_no verificar_az)"
  imprimir_fila "Azure CLI (az)" "$presente" "$(comando_instalar_az "$distro")"

  diagnosticar_kvm
  verificar_usabilidad_proveedor
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
# plugin se instala con el gestor de plugins propio de Vagrant, sin sudo:
# los plugins de Vagrant se instalan por usuario, y con sudo quedarían donde
# el usuario no los ve.
instalar_plugin_libvirt() {
  if ! verificar_vagrant; then
    aviso "Vagrant no está instalado, se omite el plugin vagrant-libvirt."
    return 0
  fi
  if verificar_plugin_libvirt; then
    info "El plugin vagrant-libvirt ya está instalado."
    return 0
  fi
  info "vagrant plugin install compila extensiones nativas: es el paso que más se rompe."
  ejecutar_instalacion "plugin vagrant-libvirt" "vagrant plugin install vagrant-libvirt" || HUBO_FALLO=1
}

# Solo agrega al usuario si el grupo ya existe (lo crea el paquete del
# proveedor). Un cambio de grupo no aplica a la sesión en curso: el script no
# puede fingir que ya quedó listo, así que siempre avisa qué hacer después.
instalar_grupo_libvirt() {
  if verificar_grupo_libvirt; then
    info "El usuario ya pertenece al grupo libvirt."
    return 0
  fi
  if ! getent group libvirt &>/dev/null; then
    aviso "El grupo libvirt no existe todavía (el proveedor no se instaló). Se omite agregar el usuario."
    return 0
  fi
  if ejecutar_instalacion "Grupo libvirt" "$(comando_instalar_grupo_libvirt)"; then
    aviso "El usuario se agregó al grupo libvirt, pero esta sesión NO lo tiene todavía. Cierra sesión y vuelve a entrar, o ejecuta 'newgrp libvirt' en la terminal donde vayas a correr vagrant, antes de intentar 'vagrant up'."
  else
    HUBO_FALLO=1
  fi
}

instalar_socket_libvirt() {
  local unidad
  unidad="$(detectar_unidad_libvirt)"
  if [[ -z "$unidad" ]]; then
    aviso "No se encontró libvirtd.socket ni libvirtd.service (el proveedor no se instaló). Se omite habilitar el servicio."
    return 0
  fi
  if verificar_socket_libvirt; then
    info "${unidad} ya está activo."
    return 0
  fi
  ejecutar_instalacion "Socket/servicio libvirtd (${unidad})" "sudo systemctl enable --now ${unidad}" || HUBO_FALLO=1
}

# La red "default" queda definida pero inactiva y sin autoarranque justo
# después de instalar el proveedor: es la causa típica de que "vagrant up"
# falle con "Network default is not active". El chequeo previo usa sudo
# (--instalar ya lo tiene disponible de forma interactiva): net-start se
# omite si ya está activa, así que nunca falla por eso; net-autostart se
# ejecuta siempre porque repetirlo no tiene efectos.
instalar_red_default() {
  if ! comando_existe virsh; then
    aviso "virsh no está instalado (el proveedor no se instaló), se omite activar la red default."
    return 0
  fi

  local activa
  info "Ejecutando: sudo virsh net-info default (para revisar el estado antes de decidir)"
  # "|| true": esta función se llama sin ningún if/&&/|| que la proteja, así
  # que un virsh que falla (red sin definir, libvirtd recién arrancando)
  # dispararía set -e aquí mismo si no se contiene.
  activa="$(sudo virsh -c qemu:///system net-info default 2>/dev/null | awk '/^Active:/ {print $2}' || true)"

  if [[ "$activa" == "yes" ]]; then
    info "La red default de libvirt ya está activa."
  else
    ejecutar_instalacion "Red libvirt default" "sudo virsh net-start default" || HUBO_FALLO=1
  fi
  ejecutar_instalacion "Autoarranque de la red default" "sudo virsh net-autostart default" || HUBO_FALLO=1
}

instalar_proveedor() {
  local distro="$1"

  if verificar_proveedor "$distro"; then
    info "El proveedor de Vagrant ya está listo."
  else
    ejecutar_instalacion "$ETIQUETA_PROVEEDOR" "$(comando_instalar_proveedor "$distro")" || HUBO_FALLO=1
  fi

  instalar_socket_libvirt
  instalar_grupo_libvirt
  instalar_red_default
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

  verificar_usabilidad_proveedor

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

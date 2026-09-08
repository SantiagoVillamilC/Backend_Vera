#!/usr/bin/env bash
# infra/local/preparar-entorno.sh (SCRUM-27)
# Diagnostica (o instala, con --instalar) Go, Docker, shellcheck, Vagrant/libvirt y Azure CLI.
set -euo pipefail

MODO_INSTALAR="no"
HUBO_FALLO=0
# libvirt/KVM es el proveedor en las dos distribuciones: no hay rama por distro para esta etiqueta.
ETIQUETA_PROVEEDOR="Proveedor Vagrant (libvirt/KVM)"

paso() { printf '==> %s\n' "$1"; }
info() { printf '    %s\n' "$1"; }
aviso() { printf '[aviso] %s\n' "$1"; }
error_fatal() { printf '[error] %s\n' "$1" >&2; exit 1; }

comando_existe() {
  command -v "$1" &>/dev/null
}

# Traduce el código de salida de "$@" a si/no sin disparar set -e: siempre termina en printf, estado 0.
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

# Lee /etc/os-release en un subshell para no pisar variables del script; devuelve arch, debian o desconocida.
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

# En Arch, Azure CLI está en el repositorio oficial; Vagrant solo en AUR, por eso solo Vagrant necesita ayudante.
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

# libvirtd.socket es la unidad recomendada en systemd moderno; libvirtd.service es el respaldo. Ninguna existe sin el proveedor instalado.
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
verificar_red_default() {
  local activa
  # "|| true": un virsh que falla no debe disparar set -e antes del "[[ ]]" de abajo.
  activa="$(virsh -c qemu:///system net-info default 2>/dev/null | awk '/^Active:/ {print $2}' || true)"
  [[ "$activa" == "yes" ]]
}

verificar_plugin_libvirt() {
  comando_existe vagrant && vagrant plugin list 2>/dev/null | grep -q '^vagrant-libvirt '
}

# El bridge de vagrant-libvirt lo asigna libvirt dinámicamente (no siempre "virbr1"): se consulta con virsh, nunca se asume.
detectar_bridge_vagrant_libvirt() {
  virsh -c qemu:///system net-info vagrant-libvirt 2>/dev/null | awk '/^Bridge:/ {print $2}' || true
}

# Requiere root (iptables sin atajo de grupo): quien llama debe verificar antes con hay_sudo_cacheado para no disparar un prompt.
verificar_forward_docker_libvirt() {
  local bridge="$1"
  sudo iptables -C DOCKER-USER -i "$bridge" -j ACCEPT &>/dev/null \
    && sudo iptables -C DOCKER-USER -o "$bridge" -j ACCEPT &>/dev/null
}

# Prueba no interactiva (sudo -n): nunca pide contraseña, solo indica si hay credencial vigente.
hay_sudo_cacheado() {
  sudo -n true &>/dev/null
}

comando_instalar_forward_docker_libvirt() {
  local bridge="$1"
  printf 'sudo iptables -C DOCKER-USER -i %s -j ACCEPT 2>/dev/null || sudo iptables -I DOCKER-USER -i %s -j ACCEPT; sudo iptables -C DOCKER-USER -o %s -j ACCEPT 2>/dev/null || sudo iptables -I DOCKER-USER -o %s -j ACCEPT' \
    "$bridge" "$bridge" "$bridge" "$bridge"
}

comando_instalar_go() {
  if [[ "$1" == "arch" ]]; then
    printf 'sudo pacman -S --needed --noconfirm go'
  else
    printf 'sudo apt-get install -y golang-go'
  fi
}

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

# Mismo proveedor en las dos distros, solo cambia el gestor de paquetes; ya no habilita el servicio, eso lo hace comando_instalar_socket_libvirt aparte.
comando_instalar_proveedor() {
  if [[ "$1" == "arch" ]]; then
    printf 'sudo pacman -S --needed --noconfirm libvirt qemu-desktop dnsmasq'
  else
    printf 'sudo apt-get install -y libvirt-daemon-system qemu-system-x86 dnsmasq vagrant-libvirt'
  fi
}

# "id -un" en vez de "$USER": en un entorno mínimo $USER puede llegar vacío y usermod fallaría con un nombre vacío.
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

diagnosticar_forward_docker_libvirt() {
  if ! comando_existe docker; then
    imprimir_fila "Reenvío Docker->vagrant-libvirt" "no" "(depende de que Docker esté instalado)"
    return 0
  fi

  local bridge
  bridge="$(detectar_bridge_vagrant_libvirt)"
  if [[ -z "$bridge" ]]; then
    imprimir_fila_incierta "Reenvío Docker->vagrant-libvirt" "la red vagrant-libvirt todavía no existe (la crea 'vagrant up' la primera vez)"
    return 0
  fi

  if ! hay_sudo_cacheado; then
    imprimir_fila_incierta "Reenvío Docker->vagrant-libvirt (${bridge})" "sin privilegios: iptables -C exige root, sin atajo de grupo como libvirt"
    return 0
  fi

  local presente
  presente="$(estado_si_no verificar_forward_docker_libvirt "$bridge")"
  imprimir_fila "Reenvío Docker->vagrant-libvirt (${bridge})" "$presente" "$(comando_instalar_forward_docker_libvirt "$bridge")"
}

# Verifica que "vagrant up --provider=libvirt" pueda funcionar de verdad: reporta cada ítem por separado (grupo, socket, red, plugin), nunca un mensaje global.
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

  if ! comando_existe docker; then
    info "Reenvío Docker->vagrant-libvirt: no aplica, Docker no está instalado."
    return 0
  fi
  local bridge
  bridge="$(detectar_bridge_vagrant_libvirt)"
  if [[ -z "$bridge" ]]; then
    info "Reenvío Docker->vagrant-libvirt: no se puede verificar todavía, la red la crea \"vagrant up\" la primera vez."
  elif ! hay_sudo_cacheado; then
    aviso "Reenvío Docker->vagrant-libvirt: no se pudo verificar sin privilegios (iptables -C exige root). Corre este script con --instalar para aplicarlo, o revísalo a mano con sudo."
  elif verificar_forward_docker_libvirt "$bridge"; then
    info "Reenvío Docker->vagrant-libvirt (${bridge}): permitido."
  else
    aviso "Reenvío Docker->vagrant-libvirt (${bridge}): bloqueado. Docker deja \"policy drop\" en su cadena FORWARD y nunca agrega una regla para este bridge: la VM va a arrancar y responder SSH, pero cualquier conexión saliente DESDE DENTRO de la VM (apt-get, curl) se va a colgar sin error. Corre este script con --instalar para agregar la regla."
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
  diagnosticar_forward_docker_libvirt

  presente="$(estado_si_no verificar_az)"
  imprimir_fila "Azure CLI (az)" "$presente" "$(comando_instalar_az "$distro")"

  diagnosticar_kvm
  verificar_usabilidad_proveedor
}

# Muestra el comando completo antes de ejecutarlo: sudo nunca se invoca en silencio.
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

# Solo actúa en Arch: en Debian llega empaquetado con el proveedor. Sin sudo: los plugins de Vagrant son por usuario.
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

# Solo agrega si el grupo ya existe; un cambio de grupo no aplica a la sesión en curso, así que siempre avisa qué hacer después.
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

# net-start se omite si la red ya está activa (nunca falla por eso); net-autostart se ejecuta siempre, repetirlo no tiene efecto.
instalar_red_default() {
  if ! comando_existe virsh; then
    aviso "virsh no está instalado (el proveedor no se instaló), se omite activar la red default."
    return 0
  fi

  local activa
  info "Ejecutando: sudo virsh net-info default (para revisar el estado antes de decidir)"
  # "|| true": sin protección de if/&&/||, un virsh que falla dispararía set -e aquí mismo.
  activa="$(sudo virsh -c qemu:///system net-info default 2>/dev/null | awk '/^Active:/ {print $2}' || true)"

  if [[ "$activa" == "yes" ]]; then
    info "La red default de libvirt ya está activa."
  else
    ejecutar_instalacion "Red libvirt default" "sudo virsh net-start default" || HUBO_FALLO=1
  fi
  ejecutar_instalacion "Autoarranque de la red default" "sudo virsh net-autostart default" || HUBO_FALLO=1
}

# A diferencia de instalar_*, esta función sí necesita sudo para su propio chequeo de idempotencia; siempre visible en el "Ejecutando: ...".
instalar_forward_docker_libvirt() {
  if ! comando_existe docker; then
    info "Docker no está instalado, se omite el reenvío Docker->vagrant-libvirt."
    return 0
  fi

  local bridge
  bridge="$(detectar_bridge_vagrant_libvirt)"
  if [[ -z "$bridge" ]]; then
    aviso "La red vagrant-libvirt todavía no existe (la crea 'vagrant up' la primera vez). Vuelve a correr --instalar después de un primer 'vagrant up' si la VM se queda sin red saliente."
    return 0
  fi

  if ! sudo iptables -L DOCKER-USER -n &>/dev/null; then
    # No se distingue el motivo (Docker sin arrancar o sudo sin autenticar): mismo criterio que con virsh, no se adivina la causa.
    aviso "No se pudo confirmar la cadena DOCKER-USER (Docker sin arrancar, o sudo sin autenticar). Se omite el reenvío Docker->vagrant-libvirt; vuelve a correr --instalar si persiste."
    return 0
  fi

  if verificar_forward_docker_libvirt "$bridge"; then
    info "El reenvío Docker->vagrant-libvirt (${bridge}) ya está permitido."
    return 0
  fi

  if ejecutar_instalacion "Reenvío Docker->vagrant-libvirt" "$(comando_instalar_forward_docker_libvirt "$bridge")"; then
    aviso "Regla de iptables agregada, pero NO sobrevive un reinicio del host (no hay netfilter-persistent/iptables-services de por medio, a propósito, para no interferir con el firewall que ya tenga cada integrante). Si 'vagrant up' se queda sin red saliente después de reiniciar, vuelve a correr 'preparar-entorno.sh --instalar'."
  else
    HUBO_FALLO=1
  fi
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
  instalar_forward_docker_libvirt
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

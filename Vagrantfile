# Vagrantfile
#
# VM local del carril A (tarjeta opcional) para probar el aprovisionamiento del
# servidor sin gastar crédito de Azure.
#
# Caja: cloud-image/debian-13, versión 20260831.2587.0 (verificado contra la
# API de Vagrant Cloud). Está construida a partir de la imagen cloud oficial
# de Debian: la misma familia con la que arranca la VM de Azure del servidor
# real (ver infra/azure/variables.sh). Comparten serial de compilación de
# Debian (20260831-2587), pero no son el mismo artefacto byte a byte: la
# imagen de Azure es la variante de proveedor "azure" de esa compilación y
# esta caja es una variante cloud genérica; la capa de integración con la
# nube difiere entre una y otra.
#
# cloud-image/debian-13 publica libvirt, qemu y virtualbox, ambos en amd64 y
# arm64. No publica variante hyperv. El equipo usa dos de esos proveedores:
#
#   Proveedor   Arquitectura   Para quién
#   libvirt     amd64          Linux (KVM)
#   virtualbox  amd64          Windows
#
# qemu queda disponible sin usar por ahora: es la vía para una futura VM
# arm64 en Apple Silicon si se decide soportar macOS (ver el párrafo
# siguiente). Windows entra por virtualbox porque esta caja no ofrece hyperv.
#
# macOS queda fuera por ahora a propósito. En Apple Silicon habría que usar el
# plugin vagrant-qemu con la variante arm64 de la caja, y entonces la VM local
# sería arm64 mientras el servidor de Azure es amd64: dejaría de ser idéntica
# al destino real. Se decide cuando haga falta.
Vagrant.configure("2") do |config|
  config.vm.box = "cloud-image/debian-13"
  config.vm.box_version = "20260831.2587.0"

  # 2048 MB de RAM y 1 vCPU en los dos proveedores, para reflejar el
  # Standard_B1ms de Azure (1 vCPU, 2 GB) que es el destino real del
  # aprovisionamiento. El objetivo es parecerse al servidor real, no ir rápido.
  config.vm.provider "libvirt" do |lv|
    lv.memory = 2048
    lv.cpus = 1
  end

  config.vm.provider "virtualbox" do |vb|
    vb.memory = 2048
    vb.cpus = 1
  end

  # Sin carpeta compartida: vagrant-libvirt, a diferencia de virtualbox, no
  # monta /vagrant por defecto. Esta caja tampoco trae rsync, así que ni
  # siquiera la carpeta sincronizada de tipo rsync funcionaría. Se desactiva
  # el montaje por defecto en vez de dejarlo declarado y roto en silencio.
  config.vm.synced_folder ".", "/vagrant", disabled: true

  # Dos variantes ya probadas y descartadas, para que nadie del equipo las
  # vuelva a intentar:
  #
  #   1. s.path apuntando a aprovisionar.sh: el provisionador shell con
  #      "path" solo sube ESE archivo al invitado. lib/comun.sh y pasos/
  #      nunca llegaban y el script moría en el primer source.
  #   2. s.inline = "bash /vagrant/infra/servidor/aprovisionar.sh": asumía
  #      que Vagrant sincroniza el proyecto en /vagrant por defecto, como
  #      hace virtualbox. Con libvirt no hay tal montaje: falla con
  #      "No such file or directory".
  #
  # La solución es el provisionador "file": copia por SCP sobre la conexión
  # SSH que Vagrant ya tiene abierta, sin depender de ningún demonio en el
  # anfitrión ni de paquetes extra en el invitado (funciona igual con
  # libvirt y con virtualbox). Copia solo infra/servidor/, que es
  # exactamente lo que infra/servidor/README.md documenta que rsync envía a
  # la VM de Azure real: la VM local queda fiel al despliegue real, en vez
  # de recibir el repositorio completo que nunca tendría en producción.
  #
  # El provisionador "file" corre como el usuario "vagrant" (no como root),
  # así que el destino tiene que ser escribible por ese usuario: /tmp sirve,
  # /root y /opt no.
  config.vm.provision "file",
    source: "infra/servidor",
    destination: "/tmp/infra-servidor"

  config.vm.provision "shell",
    privileged: true,
    inline: "bash /tmp/infra-servidor/aprovisionar.sh"
end

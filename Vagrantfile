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

  # s.inline en lugar de s.path: el provisionador shell con "path" solo sube
  # ese archivo al invitado. lib/comun.sh y pasos/ nunca llegarían y el script
  # moriría en el primer source. Con s.inline se ejecuta el aprovisionador
  # desde /vagrant, que es donde Vagrant sincroniza por defecto el directorio
  # completo del proyecto (incluidos lib/ y pasos/).
  config.vm.provision "shell" do |s|
    s.privileged = true
    s.inline = "bash /vagrant/infra/servidor/aprovisionar.sh"
  end
end

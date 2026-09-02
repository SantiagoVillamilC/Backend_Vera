#!/usr/bin/env bash
# infra/servidor/pasos/20-docker.sh
#
# Instala Docker Engine y el plugin docker compose (tarjeta SCRUM-31), y deja
# el servicio docker habilitado y activo.
#
# Este archivo se carga con source desde aprovisionar.sh, no se ejecuta como
# proceso aparte (misma nota que 10-ssh.sh sobre por qué no usa "return" para
# salir temprano).
#
# Por qué apt y no "curl -fsSL https://get.docker.com | sh": se verificó
# contra los repositorios reales de Debian 13 (trixie), la misma versión
# fijada en imagen-fijada.sh, que ya traen Docker Engine 26.1.5+dfsg1-9 y
# Docker Compose 2.26.1-4 (paquetes docker.io, docker-cli y docker-compose).
# Instalar desde el repositorio propio de Debian evita ejecutar un script de
# terceros como root, evita agregar un repositorio apt y una llave GPG
# ajenos, y hereda las actualizaciones de seguridad que ya llegan por
# trixie-security (docker.io 26.1.5+dfsg1-9+deb13u1 en ese repositorio).
#
# El paquete "docker-compose" de Debian tiene un nombre que engaña: no es el
# docker-compose v1 obsoleto (el que dejó de mantenerse en Python), es Docker
# Compose v2 empaquetado bajo el nombre viejo. La versión candidata
# verificada es 2.26.1-4, y el paquete instala el binario en dos rutas a la
# vez: /usr/bin/docker-compose (invocación con guion, compatible con v1) y
# /usr/libexec/docker/cli-plugins/docker-compose (registrado como plugin de
# la CLI de Docker), así que "docker compose version" (con espacio, la
# invocación moderna) funciona igual que "docker-compose version".
#
# docker-cli se instala explícito, no se deja como dependencia implícita:
# tanto docker.io como docker-compose solo lo declaran como "Recommends" en
# el repositorio de Debian (verificado con apt-cache depends), no como
# "Depends". apt instala los Recommends por defecto, pero algunas imágenes
# cloud minimizadas deshabilitan esa política globalmente
# (APT::Install-Recommends "0"), y si eso ocurriera en la imagen fijada, el
# comando "docker" (el cliente, en el paquete docker-cli) faltaría en
# silencio aunque el motor (docker.io) quedara instalado. Se probó
# instalando los tres paquetes con --no-install-recommends para confirmar
# que "docker" y "docker compose" quedan disponibles en ese escenario.
#
# El usuario administrador (centinela) NO se agrega al grupo "docker" en
# este paso: pertenecer a ese grupo equivale a acceso root sin contraseña
# sobre el host (el socket de Docker no aplica los mismos controles que
# sudo), y esa es una decisión de superficie de ataque que le corresponde al
# equipo, no algo que la tarjeta SCRUM-31 pida. Mientras no se decida, los
# comandos docker en el servidor se ejecutan con sudo.

NOMBRE_PASO="Instalar Docker y Docker Compose"
TARJETA="SCRUM-31"

paso "${NOMBRE_PASO} (${TARJETA})"

instalar_paquetes docker.io docker-cli docker-compose

# asegurar_servicio_activo (lib/comun.sh) decide con "-d /run/systemd/system"
# si hay servicio que tocar, no con "command -v systemctl": instalar
# docker.io trae como dependencia los paquetes systemd y systemd-sysv, así
# que el binario systemctl queda instalado en el contenedor de prueba de
# probar-en-docker.sh aunque systemd no sea ahí el proceso 1. Ver el
# comentario de esa función en lib/comun.sh para el porqué completo.
asegurar_servicio_activo docker

# Verificación de los binarios de la CLI: no dependen del daemon, así que
# corren siempre, incluso en el contenedor sin systemd de
# probar-en-docker.sh. Sirven para confirmar que los paquetes de verdad
# dejaron los comandos esperados, no solo que "apt-get install" salió con
# código 0.
if ! docker --version &>/dev/null; then
  error_fatal "Los paquetes se instalaron pero el comando 'docker' no responde a --version."
fi
info "Confirmado: $(docker --version)"

if ! docker compose version &>/dev/null; then
  error_fatal "Los paquetes se instalaron pero 'docker compose version' (el plugin, con espacio) no responde."
fi
info "Confirmado: $(docker compose version)"

# Verificación del daemon: a diferencia de las dos de arriba, esta sí
# necesita systemd como proceso 1 (para que el paso anterior haya podido
# arrancar el servicio docker) y por eso se omite en el contenedor de
# prueba. Se guarda con "-d /run/systemd/system", no con
# "command -v systemctl": ese comando ya está instalado en el contenedor de
# prueba (llega como dependencia de docker.io), pero systemd no es el
# proceso 1 ahí, así que "docker info" fallaría de verdad (el daemon nunca
# arrancó) en vez de reportar con claridad que la verificación se omitió a
# propósito. "docker info" falla si el cliente no logra hablar con dockerd
# a través del socket, así que confirma que el servicio no solo quedó
# "activo" según systemd, sino que de verdad está aceptando conexiones (la
# misma distinción que 10-ssh.sh hace verificando con "ss -tln" después de
# reiniciar sshd, en vez de confiar solo en el estado que reporta
# systemctl).
if [[ ! -d /run/systemd/system ]]; then
  aviso "systemd no es el proceso 1 en este sistema: se omite la verificación del daemon de Docker. Verifícalo en la VM real (Azure o Vagrant) con: docker info"
else
  if ! docker info &>/dev/null; then
    error_fatal "El servicio docker está activo según systemd, pero 'docker info' no logra hablar con el daemon a través del socket."
  fi
  info "Confirmado con 'docker info': el daemon de Docker responde por el socket."
fi

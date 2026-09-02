#!/usr/bin/env bash
# infra/servidor/red-fijada.sh
#
# Única fuente de verdad del puerto SSH del servidor. La necesitan los dos
# lados de la frontera de despliegue: la regla del NSG en infra/azure/ (el
# puerto que abre en el grupo de seguridad) y sshd en el servidor real (el
# puerto que fija infra/servidor/pasos/10-ssh.sh). infra/azure/ no viaja al
# servidor (ver infra/servidor/README.md), así que este archivo tiene que
# vivir del lado que sí viaja: infra/azure/variables.sh lo lee cruzando la
# frontera con BASH_SOURCE, igual que ya hace con imagen-fijada.sh. Mismo
# patrón que ese archivo: solo declaraciones "${VAR:=valor}", sin lógica.
#
# Para sobrescribir el valor sin editar este archivo:
#   PUERTO_SSH=2202 bash infra/servidor/aprovisionar.sh

# Por qué 22022 y no otro puerto:
# - Se descartó 2222: es el puerto alternativo obvio, y los escáneres
#   automatizados lo prueban casi con la misma frecuencia que el 22.
# - Se descartó cualquier puerto por encima de 32767: ese es el piso del
#   rango efímero de salida del kernel Linux (net.ipv4.ip_local_port_range,
#   32768 a 60999 por defecto). Una colisión ahí produce un fallo
#   intermitente (el puerto queda ocupado por una conexión saliente
#   cualquiera de vez en cuando) difícil de diagnosticar porque no falla
#   siempre.
# - Se verificó en el servidor real que 22022 está libre en /etc/services.
#
# Advertencia que hay que dejar escrita sin adornarla, porque presentarlo de
# otra forma sería falso: mover el puerto SSH del 22 al 22022 reduce el
# ruido de bots que escanean el puerto por defecto, NO reduce el riesgo. Un
# "nmap" sobre el rango completo de puertos encuentra el 22022 en segundos.
# La protección real contra acceso no autorizado es la autenticación
# exclusivamente por llave pública que fija infra/servidor/pasos/10-ssh.sh
# (PasswordAuthentication no), no el número de puerto.
: "${PUERTO_SSH:=22022}"

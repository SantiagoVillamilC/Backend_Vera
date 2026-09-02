# Aprovisionamiento del servidor

Script de aprovisionamiento idempotente para el servidor de Centinela. Prepara
una máquina Debian 13 (trixie) desde cero: acceso SSH, Docker, nginx, Postgres
y el certificado TLS. El primer paso (`00-verificar-sistema.sh`) exige el
sistema operativo fijado en `imagen-fijada.sh` (por defecto `ID=debian` y
`VERSION_ID=13`): el script se niega a correr sobre cualquier otro sistema,
salvo que se pida explícitamente lo contrario (ver la sección de pasos más
abajo).

## Regla de idempotencia

Ejecutar el script dos veces seguidas sobre el mismo servidor tiene que dejarlo
en el mismo estado que ejecutarlo una sola vez. La segunda corrida debe
reportar 0 cambios aplicados. Cada paso usa los helpers de `lib/comun.sh` (los
que empiezan con `asegurar_` o `instalar_`), que primero verifican el estado
actual y solo aplican un cambio cuando el estado real difiere del esperado.

## Cómo ejecutarlo

### VM de Azure (destino real)

```bash
scp -r infra/servidor usuario@servidor:/tmp/infra-servidor
ssh usuario@servidor 'sudo mkdir -p /opt/centinela && sudo cp -rT /tmp/infra-servidor /opt/centinela/infra-servidor && sudo bash /opt/centinela/infra-servidor/aprovisionar.sh'
```

La copia va por `scp` sobre la conexión SSH existente, en lugar de `rsync`.
La imagen cloud de Debian de Azure no trae `rsync` en el invitado, y `rsync`
necesita el binario en los dos extremos de la conexión: si falta en el destino,
el comando falla. `scp` no requiere paquetes extra en el invitado y funciona
igual que la caja de Vagrant (ver el Vagrantfile, que también copia por `scp`
a través del provisionador `file`).

El destino final es `/opt/centinela/infra-servidor`, no `/tmp`: se comprobó
en la VM real que Debian limpia `/tmp` en cada arranque, así que todo lo
copiado ahí se pierde cada vez que la VM se reinicia. Con SCRUM-29
encendiendo y apagando la VM a diario, eso deja de ser un detalle menor.
`/tmp` sigue siendo la escala obligatoria de `scp`: corre como el usuario
administrador (`centinela`, no root), que no puede escribir directamente en
`/opt`. Por eso la copia real a `/opt/centinela` la hace un `sudo cp -rT`
dentro de la misma sesión `ssh`, después de que `scp` ya dejó los archivos
en `/tmp`, no `scp` apuntando a `/opt` directamente. `infra/azure/crear-servidor.sh`
imprime este mismo comando (con el usuario, la IP y, cuando ya no quede la
regla NSG del puerto 22, el puerto de conexión correctos) al final de su
ejecución.

### VM local con Vagrant

```bash
vagrant up
```

Levanta la caja `cloud-image/debian-13` (versión `20260831.2587.0`) definida
en el `Vagrantfile` de la raíz del repositorio, con 4096 MB de RAM y 2 vCPU
(para reflejar el `Standard_B2als_v2` de Azure), y un disco de 30 GiB
(`lv.machine_virtual_size` en el proveedor `libvirt`; en `virtualbox` no hay
equivalente configurado, ver el comentario del `Vagrantfile`). El arranque
local sigue este orden:

1. Vagrant crea la VM y la arranca con la caja fijada.
2. El `Vagrantfile` desactiva el montaje por defecto de `/vagrant`
   (`config.vm.synced_folder ".", "/vagrant", disabled: true`). A diferencia
   de `virtualbox`, el proveedor `vagrant-libvirt` no monta esa carpeta
   compartida por defecto, y esta caja tampoco trae `rsync`, así que ni
   siquiera una carpeta sincronizada serviría de respaldo.
3. Un provisionador `file` copia el contenido de `infra/servidor/` (solo esa
   carpeta, no el repositorio completo) a `/tmp/infra-servidor` dentro de la
   VM, por SCP sobre la conexión SSH que Vagrant ya tiene abierta. No
   necesita ningún demonio en el anfitrión ni paquetes extra en el invitado,
   y funciona igual con `libvirt` y con `virtualbox`. Copiar exactamente
   `infra/servidor/`, la misma carpeta que envía el despliegue real hacia la
   VM de Azure (ver más abajo), reproduce en la VM local lo que ocurre en
   producción en vez de darle al invitado un repositorio que nunca tendría
   en el destino real. El provisionador `file` corre como el usuario
   `vagrant`, no como root, por eso el destino es `/tmp` y no `/root` ni
   `/opt`.
4. Un provisionador `shell` en modo `inline`, con `privileged: true`, ejecuta
   `bash /tmp/infra-servidor/aprovisionar.sh`. Como `lib/comun.sh` y
   `pasos/` viajaron junto con el resto de `infra/servidor/` en el paso
   anterior, el script principal los encuentra sin depender de ninguna
   carpeta compartida.

El `Vagrantfile` deja comentado por qué se descartaron dos variantes
anteriores: un provisionador `shell` con `path` (solo sube un archivo, así
que `lib/` y `pasos/` nunca llegaban) y un `inline` que asumía `/vagrant`
sincronizado por defecto (falla en `libvirt` con "No such file or
directory").

Esta caja está construida sobre la misma imagen cloud de Debian que la VM de
Azure (mismo serial de compilación de Debian, ver
[`infra/azure/variables.sh`](../azure/variables.sh)), aunque no es el mismo
artefacto byte a byte: la imagen de Azure trae su propia capa de integración
con esa nube.

El `Vagrantfile` declara bloques de proveedor para `libvirt` (Linux/KVM) y
`virtualbox` (Windows), así cada integrante usa el que tenga instalado sin
editar el archivo. Esta caja no publica variante `hyperv`. macOS queda fuera
por ahora: ver el comentario de cabecera del `Vagrantfile`.

Para dejar el proveedor y el propio Vagrant instalados, ver
[`infra/local/preparar-entorno.sh`](../local/preparar-entorno.sh). Los
paquetes del proveedor no bastan para que `vagrant up --provider=libvirt`
funcione: hay un orden que respetar.

1. `infra/local/preparar-entorno.sh --instalar`. Instala Vagrant, los
   paquetes de libvirt/KVM, y de paso intenta el resto de los pasos de abajo.
2. Entrar al grupo `libvirt`. El paso anterior agrega al usuario, pero un
   cambio de grupo no aplica a la sesión en curso: cerrar sesión y volver a
   entrar, o ejecutar `newgrp libvirt` en la terminal donde se vaya a correr
   Vagrant. Sin esto, `vagrant-libvirt` falla con un error de permisos que no
   menciona el grupo.
3. Confirmar que la red `default` de libvirt está activa. Queda definida pero
   apagada justo después de instalar: `sudo virsh net-list --all` debe
   mostrarla `active`; si no, `sudo virsh net-start default && sudo virsh
   net-autostart default`. Sin esto, `vagrant up` falla con "Network default
   is not active".
4. Confirmar el plugin `vagrant-libvirt`: `vagrant plugin list` debe
   mostrarlo. En Debian/Ubuntu llega como paquete apt; en Arch se instala con
   `vagrant plugin install vagrant-libvirt` (sin `sudo`: los plugins de
   Vagrant son por usuario, no del sistema).
5. Recién ahí, `vagrant up --provider=libvirt`.

`preparar-entorno.sh` (sin argumentos) diagnostica los cuatro puntos y, al
final, imprime una verificación explícita de si el proveedor queda usable o
no, item por item.

### Prueba de idempotencia con Docker

```bash
infra/servidor/probar-en-docker.sh
```

No gasta crédito de Azure ni requiere Vagrant. Ejecuta el script dos veces
seguidas dentro de un mismo contenedor `debian:13` y falla con un mensaje claro
si la segunda corrida reporta más de 0 cambios. El contenedor no tiene
systemd: los pasos que dependan de servicios (`asegurar_servicio_activo`) se
verifican después en la VM real. La imagen `debian:13` de Docker tampoco trae
`curl`, así que `00-verificar-sistema.sh` avisa que falta `curl` en vez de
llegar a preguntarle algo al servicio de metadata de Azure; en cualquiera de
los dos casos, no falla.

## Configuración local

Si algún paso necesita variables propias del servidor, se crea un archivo
`aprovisionar.env` junto a `aprovisionar.sh`. Ese archivo no se versiona (está
en `.gitignore`).

## Cómo agregar un paso nuevo

1. Crear `pasos/NN-nombre.sh` con el siguiente número de dos dígitos
   disponible.
2. Declarar arriba las variables `NOMBRE_PASO` y `TARJETA`.
3. Reemplazar la llamada a `paso_pendiente` por la implementación real, usando
   los helpers de `lib/comun.sh` para que cada cambio quede registrado.
4. Ejecutar `infra/servidor/probar-en-docker.sh` para confirmar que el paso
   sigue siendo idempotente.
5. Ejecutar `shellcheck infra/servidor/**/*.sh` desde la raíz del repositorio
   (`infra/local/preparar-entorno.sh` lo instala si falta). Salir con código
   distinto de 0 solo por notas `SC1091` informativas en líneas `source` con
   ruta real (por ejemplo, `source "${DIRECTORIO_SCRIPT}/lib/comun.sh"`) es
   normal: sin la bandera `-x`, shellcheck no las sigue aunque tengan una
   directiva `# shellcheck source=<ruta>` bien formada arriba. Para
   verificarlo sin ese ruido, ejecutar `shellcheck -x <archivo>` estando
   parado en el mismo directorio del archivo.

## Pasos

| Archivo | Paso | Tarjeta | Estado |
|---|---|---|---|
| `00-verificar-sistema.sh` | Verificar que el sistema es el fijado | SCRUM-27 | Implementado |
| `10-ssh.sh` | Endurecer el acceso por SSH | SCRUM-28 | Implementado |
| `20-docker.sh` | Instalar Docker y Docker Compose | SCRUM-31 | Implementado |
| `30-nginx.sh` | Instalar y configurar nginx como proxy inverso | SCRUM-32 | Pendiente |
| `40-postgres.sh` | Instalar y configurar Postgres en contenedor | SCRUM-33 | Pendiente |
| `50-certbot.sh` | Emitir el certificado TLS con certbot y su temporizador | SCRUM-30 | Pendiente |

### `imagen-fijada.sh`

Única fuente de verdad del sistema operativo y la imagen que deben correr los
tres destinos (VM de Azure, VM local de Vagrant, contenedor de prueba):
`IMAGEN_URN`, `IMAGEN_VERSION_ESPERADA`, `SERIAL_IMAGEN_DEBIAN`,
`IMAGEN_OS_ID` e `IMAGEN_OS_VERSION_ID`. Solo declaraciones
`: "${VAR:=valor}"`, sin lógica. Vive dentro de `infra/servidor/` a
propósito: es lo único que viaja en el despliegue hacia el servidor real (ver
más arriba), así que `aprovisionar.sh` lo carga con `source` justo después de
`lib/comun.sh`, antes de recorrer los pasos, y todos ellos (no solo el gate)
pueden usar sus variables.

[`infra/azure/variables.sh`](../azure/variables.sh), que se queda del lado de
la máquina local, hace `source` de este archivo (resuelto con `BASH_SOURCE`, no con una
ruta relativa al directorio de trabajo) en vez de duplicar los valores. **Si
cambia la imagen fijada, este es el único archivo que hay que tocar.**

### `red-fijada.sh`

Mismo patrón y mismo criterio que `imagen-fijada.sh`: única fuente de verdad
del puerto SSH (`PUERTO_SSH`, por defecto `22022`), solo declaraciones
`: "${VAR:=valor}"`, sin lógica. Vive dentro de `infra/servidor/` por la
misma razón: lo necesitan los dos lados de la frontera de despliegue (la
regla del NSG en `infra/azure/crear-servidor.sh`, y sshd en el servidor real
vía `infra/servidor/pasos/10-ssh.sh`), y `infra/azure/` no viaja al
servidor, así que el archivo tiene que estar del lado que sí viaja.
`infra/azure/variables.sh` lo lee cruzando la frontera con `BASH_SOURCE`,
igual que ya hace con `imagen-fijada.sh`. Documenta por qué `22022` (se
descartó `2222` por ser el puerto alternativo obvio, y todo lo que pase de
32767 por chocar con el rango efímero de salida del kernel) y deja escrita
la advertencia de que mover el puerto reduce el ruido de bots que escanean
el 22 por defecto, pero no reduce el riesgo.

### `00-verificar-sistema.sh`

Gate de entrada, corre primero por el prefijo `00-`. Exige `ID=${IMAGEN_OS_ID}`
y `VERSION_ID=${IMAGEN_OS_VERSION_ID}` en `/etc/os-release` (por defecto,
`debian` y `13`); si no cuadra, corta con `error_fatal`. Si `curl` está
instalado y el servicio de metadata de instancia de Azure responde, compara la
versión exacta de la imagen contra `IMAGEN_VERSION_ESPERADA`. Dos caminos no
fatales, cada uno con su propio mensaje: si falta `curl` lo dice explícito
("curl no está instalado"), y si `curl` existe pero el servicio no responde
(fuera de Azure, por ejemplo en la prueba de Docker o en la VM de Vagrant) dice
que no está en Azure. No modifica nada del sistema, así que nunca cuenta como
cambio aplicado.

Salida de escape: `OMITIR_VERIFICACION_SISTEMA=si` salta el gate por completo,
con un aviso visible de que se saltó a propósito. Sirve para el día en que
Azure pode la versión de imagen fijada y el equipo necesite seguir trabajando
mientras se actualiza `imagen-fijada.sh`.

El gate es en sí mismo un paso más, pero `aprovisionar.sh --solo <otro-paso>`
sí lo ejecuta primero (a menos que el paso pedido sea el propio gate, para no
duplicarlo): ninguna corrida dirigida a un paso puntual se salta la
verificación de sistema.

### `10-ssh.sh`

Fija en `/etc/ssh/sshd_config.d/10-centinela.conf` el puerto de
`PUERTO_SSH` (ver `red-fijada.sh` arriba) y la autenticación exclusivamente
por llave pública: `PasswordAuthentication no`, `PubkeyAuthentication yes`,
`PermitRootLogin no`, `KbdInteractiveAuthentication no`. `PasswordAuthentication no`
se fija explícitamente aunque cloud-init ya lo haya puesto en
`50-cloud-init.conf`: ese archivo puede cambiar con una actualización de la
imagen o editarse a mano, y el aprovisionamiento tiene que dejar el estado
explícito y verificable, no heredado de otro archivo.

El número `10-` (menor que el `50-` de cloud-init) no es cosmético: sshd
incluye `sshd_config.d/*.conf` al principio de `sshd_config` y procesa esos
archivos en orden alfabético, y `sshd_config(5)` es explícito en que gana
el **primer** valor obtenido, no el último (al revés de la intuición que
viene de runlevels o de los sitios de nginx). Pero el número por sí solo es
solo una apuesta razonable: otra versión de cloud-init podría usar un
número más chico y volver a ganar la precedencia en silencio. La garantía
real es que el paso relee la configuración efectiva con `sshd -T` **antes**
de reiniciar sshd y compara cada directiva contra lo esperado (incluyendo
un chequeo aparte para `Port`, que `sshd_config(5)` documenta como
directiva que admite múltiples valores a la vez, no "primer valor gana");
si algo no cuadra, corta con `error_fatal` sin tocar el servicio.

Antes de escribir nada, comprueba `systemctl is-enabled ssh.socket`: con
activación por socket, la directiva `Port` de `sshd_config` se ignora en
silencio (systemd es quien escucha el puerto, no sshd), así que el paso
cortaría con `error_fatal` en vez de reportar éxito sobre una configuración
que nunca tomó efecto. Si systemd no es el proceso 1 (no existe `/run/systemd/system`, el caso del
contenedor de prueba de `probar-en-docker.sh`, e incluso de una VM real
donde `systemctl` ya haya quedado instalado como dependencia de otro
paquete pero systemd todavía no haya arrancado), se omite el paso completo
con un `aviso`, igual que `asegurar_servicio_activo`.

El reinicio de sshd es condicional: solo ocurre si `resumen_cambios`
aumentó al escribir las directivas. Si nada cambió, el paso lo dice con
`info` y no reinicia nada, para no romper la regla de 0 cambios en la
segunda corrida. Cuando sí reinicia, después de reiniciar además confirma
con `ss -tln` que sshd de verdad quedó escuchando en el puerto nuevo (la
comparación contra `sshd -T` solo prueba que el archivo ganaría esa
precedencia, no que el proceso reiniciado haya podido abrir ese socket).

La secuencia de transición que evita quedarse sin acceso: la regla NSG
vieja del puerto 22 (`permitir-ssh`) y la nueva (`permitir-ssh-${PUERTO_SSH}`,
ver `infra/azure/crear-servidor.sh`) coexisten a propósito mientras dura la
transición. El paso termina con un `aviso` muy visible: verificar desde
otra terminal que la conexión funciona por el puerto nuevo antes de cerrar
la sesión actual, y solo después borrar la regla vieja del NSG (a mano,
nunca desde un script: la política es que una regla existente no se edita
ni se borra desde código).

### `20-docker.sh`

Instala Docker Engine y el plugin `docker compose` desde los paquetes
propios de Debian 13 (`docker.io`, `docker-cli`, `docker-compose`), en vez
de `curl -fsSL https://get.docker.com | sh`. Se descartó el script de
terceros porque el repositorio de Debian ya trae una versión reciente
(Docker Engine 26.1.5+dfsg1-9, Compose 2.26.1-4, verificado con
`apt-cache policy` contra la misma imagen fijada en `imagen-fijada.sh`) y
porque instalar desde apt evita ejecutar un script ajeno como root, evita
sumar un repositorio y una llave GPG externos, y hereda las actualizaciones
de seguridad que ya llegan por `trixie-security`.

El nombre del paquete `docker-compose` en Debian engaña: no es el Compose
v1 obsoleto, es Compose v2 empaquetado bajo el nombre viejo (confirmado con
`apt-cache policy`, versión candidata `2.26.1-4`). El paquete deja el
binario en dos rutas: `/usr/bin/docker-compose` (invocación con guion) y
`/usr/libexec/docker/cli-plugins/docker-compose` (registrado como plugin),
así que tanto `docker-compose version` como `docker compose version` (la
invocación moderna, con espacio) funcionan.

`docker-cli` se instala explícito porque tanto `docker.io` como
`docker-compose` solo lo declaran como `Recommends` en el repositorio de
Debian, no como `Depends` (verificado con `apt-cache depends`). apt instala
los `Recommends` por defecto, pero algunas imágenes cloud minimizadas
deshabilitan esa política, y si la imagen fijada llegara a cambiar en ese
sentido, el cliente `docker` faltaría en silencio aunque el motor quedara
instalado. Se probó instalando los tres paquetes con
`--no-install-recommends` para confirmar que el escenario queda cubierto.

El usuario administrador (`centinela`) no se agrega al grupo `docker` en
este paso: ese grupo equivale a acceso root sin contraseña sobre el host
(el socket de Docker no pasa por los mismos controles que `sudo`), y es una
decisión de superficie de ataque que le corresponde al equipo, no algo que
pida la tarjeta SCRUM-31. Mientras no se decida, los comandos `docker` en
el servidor se ejecutan con `sudo`.

La verificación tiene dos niveles. `docker --version` y
`docker compose version` no dependen del daemon, así que corren siempre,
incluso en el contenedor sin systemd de `probar-en-docker.sh`: confirman
que los paquetes dejaron los comandos esperados, no solo que
`apt-get install` salió con código 0. `docker info` sí necesita que systemd sea el proceso 1 (para que el
servicio docker haya podido arrancar), así que se omite cuando no existe
`/run/systemd/system` (mismo criterio que usa `asegurar_servicio_activo`
en `lib/comun.sh`, no `command -v systemctl`: ese binario puede llegar
como dependencia de otro paquete, como pasa con `systemd`/`systemd-sysv`
al instalar `docker.io`, sin que systemd sea de verdad el proceso 1), y en
la VM real confirma que el
servicio no solo quedó "activo" según systemd, sino que de verdad está
aceptando conexiones por el socket, la misma distinción que `10-ssh.sh`
hace verificando con `ss -tln` después de reiniciar sshd en vez de confiar
solo en lo que reporta `systemctl`.

Confirmado contra la VM real de Azure (`centinela-vm`, 2 de septiembre de
2026): el paquete `docker.io` de Debian sí deja el servicio `docker`
habilitado y activo desde su propio postinst
(`Created symlink '/etc/systemd/system/multi-user.target.wants/docker.service'`
en la salida de `apt-get install`), así que `asegurar_servicio_activo
docker` tomó la rama trivial ("El servicio docker ya está habilitado y
activo") desde la primera corrida. La rama de `systemctl enable --now`
sigue sin ejercitarse de verdad contra ningún sistema: solo se probaría si
algún día `docker.io` dejara de habilitar su propio servicio. Primera
corrida contra la VM real: `Cambios aplicados: 3` (los tres paquetes).
Segunda corrida: `Cambios aplicados: 0`, con `docker info` confirmando el
daemon real en las dos.

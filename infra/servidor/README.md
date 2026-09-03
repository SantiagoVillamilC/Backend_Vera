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
| `30-nginx.sh` | Instalar y configurar nginx como proxy inverso | SCRUM-32 | Implementado |
| `40-postgres.sh` | Instalar y configurar Postgres en contenedor | SCRUM-33 | Implementado |
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

### `dominio-fijado.sh`

Mismo patrón y mismo criterio que `imagen-fijada.sh` y `red-fijada.sh`: única
fuente de verdad del dominio del servidor (`DOMINIO_SERVIDOR`), solo
declaraciones `: "${VAR:=valor}"`, sin lógica. La necesitan
`infra/servidor/pasos/30-nginx.sh` (el `server_name` de
`nginx/conf.d/centinela.conf`) y, a futuro,
`infra/servidor/pasos/50-certbot.sh` (a qué dominio emitirle el
certificado).

El valor por defecto es `_`, el comodín de nginx para "acepta cualquier
Host": SCRUM-24 (la tarjeta que consigue el dominio `.dev`) sigue en curso,
y mientras no exista un dominio real, el servidor se sirve solo por IP.
Cuando SCRUM-24 cierre, `DOMINIO_SERVIDOR` es la única variable que hay que
cambiar (editando este archivo o pasándola por entorno), y `30-nginx.sh` la
propaga sola al siguiente aprovisionamiento sin tocar el resto de la
configuración.

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

### `30-nginx.sh`

Instala y configura nginx como proxy inverso corriendo como contenedor
Docker, no como paquete de apt: decisión de equipo, consistente con
Postgres (`40-postgres.sh`, tarjeta SCRUM-33, implementado, ver
`### 40-postgres.sh` más abajo) y con la app, que ya trae su propio
`Dockerfile` con `EXPOSE 8080`. El razonamiento
completo, con los pros y contras de la alternativa descartada (paquete de
apt), vive en el comentario de Jira de la tarjeta SCRUM-32 y en la
bitácora técnica del proyecto de grado, no se repite acá.

nginx es dueño de `compose.yaml`, en la raíz de `infra/servidor/`, donde
también declara `name: centinela` y el bloque top-level `networks:` que
comparten el resto de los servicios del proyecto (ver `### 40-postgres.sh`
más abajo, que documenta por qué Postgres NO reescribe este mismo archivo
sino que escribe el suyo propio). Ese archivo, igual que
`nginx/conf.d/centinela.conf` y `nginx/snippets/raiz-http.conf`, no es un
archivo estático versionado aparte: los arma `30-nginx.sh` en tiempo de
aprovisionamiento con `asegurar_archivo_con_contenido` (`lib/comun.sh`), a
partir de heredocs embebidos en el propio script. Lo que viaja al
servidor por `scp` como parte de `infra/servidor/` es el script que arma
esos tres archivos, no los tres archivos ya armados; el próximo
aprovisionamiento los sobreescribe si el contenido esperado cambió. El
campo `name: centinela` está fijo, no derivado del nombre del directorio,
porque `infra/servidor/` se copia a rutas distintas según el destino
(`/opt/centinela/infra-servidor` en Azure, `/tmp/infra-servidor` en
Vagrant, `/root/infra-servidor` en el contenedor de prueba de
`probar-en-docker.sh`), y sin ese campo el nombre del proyecto (y por lo
tanto el prefijo de los contenedores) cambiaría según dónde se ejecute.

El gotcha real de este paso está en `nginx/snippets/raiz-http.conf`:
`proxy_pass http://api:8080;` con el nombre de host a secas hace que
`nginx -t` y el arranque del contenedor fallen con "host not found in
upstream" mientras no exista ningún contenedor `api` en la red
`centinela`, y hoy no existe ninguno: no hay todavía ningún paso de
`infra/servidor/pasos/` que despliegue la app. Se resuelve difiriendo la
resolución del nombre a tiempo de petición, con el DNS embebido de
Docker, en vez de a tiempo de carga de la configuración:
`resolver 127.0.0.11 valid=10s;` seguido de `set $api http://api:8080;` y
`proxy_pass $api;`. Verificado de forma directa, no solo leído: sin que
exista siquiera la red `centinela` ni ningún contenedor `api` en ninguna
parte, `docker run --rm ... nginx -t` (sin declarar `--network`, así que
ni siquiera comparte red con el contenedor real) pasa igual, y ya con el
contenedor de compose arriba, `curl -sI http://localhost/` devuelve
`502 Bad Gateway` (nginx arriba, snippet cargado, sin backend real
detrás), no un fallo de arranque.

El reparto de archivos con la tarjeta SCRUM-30 (certbot, todavía
esqueleto) es deliberado. `nginx/conf.d/centinela.conf` (el bloque
`server { listen 80 ... }` con el `location /.well-known/acme-challenge/`)
es dueño de esta tarjeta, SCRUM-32, y SCRUM-30 no lo va a tocar. El
`location /` vive aparte, en `nginx/snippets/raiz-http.conf`, incluido
desde `centinela.conf` con `include`, porque SCRUM-30 va a sobreescribir
ese snippet completo con una redirección a HTTPS en cuanto exista el
certificado, sin tocar el resto de `centinela.conf`. Mientras tanto,
`nginx/snippets/raiz-http.conf` con el truco del `resolver` es el único
punto de entrada real al backend.

El dominio del `server_name` sale de `DOMINIO_SERVIDOR`
(`dominio-fijado.sh`, ver más abajo), hoy `_` porque SCRUM-24 sigue en
curso: el servidor se sirve solo por IP mientras tanto.

La regresión real de cobertura que deja este paso: el contenedor de
prueba de `probar-en-docker.sh` no tiene acceso al socket de Docker (no
monta `/var/run/docker.sock`, no corre `--privileged`), así que, aunque
tuviera systemd como proceso 1 (tampoco lo tiene), no podría levantar el
contenedor nginx de todos modos. Esa prueba se limita a confirmar que los
tres archivos (`compose.yaml`, `centinela.conf`, `raiz-http.conf`) se
generan con el contenido esperado y que la segunda corrida no vuelve a
escribirlos; que el contenedor nginx de verdad arranca, que `nginx -t`
pasa contra la imagen pinneada, y que el puerto 80 queda escuchando pasa
a depender enteramente de la VM real (Azure o Vagrant). Confirmado
contra Azure el 3 de septiembre de 2026 (ver más abajo); la VM local de
Vagrant sigue sin verificarse. Antes de esa confirmación se había corrido
una vez de punta a punta contra un sistema con systemd de verdad, aunque
no fuera la VM: la máquina de desarrollo (Arch Linux, systemd como
proceso 1, daemon de Docker local).
Primera corrida: creó la red `centinela`, el contenedor `centinela-nginx`
y los tres archivos; `nginx -t` pasó, `ss -tln` confirmó el puerto 80
escuchando y `curl -sI http://localhost/` devolvió `502 Bad Gateway`.
Segunda corrida: `Cambios aplicados: 0`, con el mensaje "El contenedor
nginx ya estaba corriendo con la configuración actual".

Confirmado contra la VM real de Azure (`centinela-vm`, 3 de septiembre de
2026, vía SSH en el puerto `22022`): primera corrida de
`aprovisionar.sh`, `nginx -t` validó la configuración, se creó la red
`centinela` y el contenedor `centinela-nginx`, y `ss -tln` confirmó el
puerto 80 escuchando (`Cambios aplicados: 1`, el contenedor). Segunda
corrida inmediata: `Cambios aplicados: 0` (idempotencia confirmada contra
la VM real, no solo contra la máquina de desarrollo). `curl -I` contra la
IP pública del servidor devolvió `502 Bad Gateway`, el mismo resultado
esperado que en local: nginx arriba y sirviendo, sin contenedor `api`
todavía. Evidencia completa en el comentario de la tarjeta SCRUM-32 en
Jira. Es la misma honestidad que ya deja escrita `20-docker.sh` sobre la
rama de `systemctl enable --now`, que tampoco se ha ejercitado nunca
contra un sistema real.

### `40-postgres.sh`

Instala y configura Postgres en contenedor Docker (tarjeta SCRUM-33). El
texto literal de la tarjeta fija la decisión de diseño: "El volumen va
declarado fuera del contenedor. Un docker compose down -v mal dado borra
la base." El volumen de datos es un bind mount a
`/opt/centinela/postgres/datos`, montado en `/var/lib/postgresql/data`
dentro del contenedor, fuera por completo del árbol que gestiona
`docker compose down -v` (que solo alcanza volúmenes NOMBRADOS declarados
en un `compose*.yaml`) y fuera también de `infra/servidor/` (que viaja
completo por `scp` a rutas distintas según el destino, y cuyo equivalente
en `/tmp` además se limpia en cada reinicio de Debian, ver la sección
"VM de Azure" más arriba). El
directorio se crea con permiso `700` y sin dueño asignado desde el script:
la imagen oficial de Postgres arranca su contenedor como root y hace
`chown` del datadir al usuario `postgres` dentro de su propio entrypoint,
antes de bajar privilegios para `initdb`; ese `chown` ocurre DENTRO del
contenedor, y verificarlo sin el contenedor corriendo no era posible (ver
más abajo la verificación real que sí lo confirmó).

Diseño multi-archivo de compose: esto era justo el problema que la
bitácora técnica del proyecto (`2026-09-02-nginx-contenedor-vs-paquete.md`,
sección "Qué queda abierto") dejaba explícitamente pendiente al cerrar
SCRUM-32, sin resolverlo todavía en ese momento. Se resolvió con el
mecanismo nativo de `docker compose` de fusionar varios archivos con
`-f`, no reescribiendo el `compose.yaml` de nginx. `30-nginx.sh` sigue
siendo dueño exclusivo de `compose.yaml` (con `name: centinela` y el
bloque top-level `networks:`, compartidos por todos los servicios);
`40-postgres.sh` escribe su propio `compose.postgres.yaml`, con
solamente `services: postgres:`, sin repetir ninguna de esas dos claves.
`asegurar_servicio_compose` (`lib/comun.sh`) dejó de recibir "el" archivo
de compose como argumento fijo: ahora deriva el directorio del argumento
que recibe y arma, con un glob `compose*.yaml`, la lista completa de
fragmentos que encuentre ahí, pasándolos todos como banderas `-f` tanto al
`ps -q` de "antes" como al `up -d`. Ningún paso reescribe el bloque
`services:` de otro, y el orden en que corran los dos pasos no importa: el
que dispare `up -d` primero fusiona lo que ya exista en ese momento. El
glob ordena `compose.postgres.yaml` antes que `compose.yaml`
(alfabéticamente, "p" antes que "y"), y se verificó con
`docker compose -f compose.postgres.yaml -f compose.yaml config` que ese
orden igual resuelve `name: centinela` y ambos servicios correctamente: la
fusión de claves de nivel superior de `docker compose` no depende de cuál
archivo declare `name:` esté primero o segundo en la lista de `-f`.

La contraseña de Postgres se genera una sola vez y se persiste en
`/opt/centinela/postgres/postgres.env` (variable `POSTGRES_PASSWORD`,
permisos `600`), fuera de `infra/servidor/` por la misma razón que el
directorio de datos. `asegurar_archivo_con_secreto` y `generar_secreto`
(`lib/comun.sh`) son los helpers nuevos: el primero no hace nada si el
archivo ya existe (es la propia existencia del archivo, no una variable de
estado aparte, la que decide si "ya se generó" o no, lo que lo vuelve
idempotente por diseño), y el segundo arma 32 caracteres alfanuméricos a
partir de `/dev/urandom` y `base64` (los dos confirmados presentes en la
imagen mínima `debian:13`, sin depender de `openssl`), con el recorte a 32
caracteres hecho con expansión de parámetros de bash en vez de con
`head -c 32` al final de la tubería, para no exponerse a un SIGPIPE
intermitente bajo el `set -o pipefail` que hereda todo lo que se carga con
`source` desde `aprovisionar.sh`. El valor de ese archivo solo importa en
el primer arranque del contenedor (`initdb` lo usa para crear el rol
`centinela`); editarlo después no cambia nada en la base ya inicializada,
y rotar la contraseña de verdad requiere `ALTER ROLE` dentro de la base,
no solo tocar el archivo. `compose.postgres.yaml` no publica ningún
`ports:`: Postgres solo lo consume la app (contenedor `api`, todavía sin
ningún paso que lo despliegue) dentro de la red `centinela`, por nombre de
contenedor, el mismo argumento de seguridad que ya usa `30-nginx.sh` para
no publicar el 8080 de `api` hacia el host.

La verificación, a diferencia de la de nginx (que confirma un puerto
publicado), es en parte negativa: además de reintentar
`docker exec centinela-postgres pg_isready -U centinela -d centinela
-h localhost` en una ventana acotada (30 intentos, 2 segundos entre cada
uno, porque `docker compose up -d` devuelve el control en cuanto el
contenedor arranca, no cuando `initdb` termina de inicializar la primera
vez), también confirma con `ss -tln` que el puerto 5432 NO aparece
escuchando en el host. El `healthcheck` del propio
`compose.postgres.yaml` usa `pg_isready` sin `-h`, a propósito distinto de
la verificación del script: ese test lo evalúa Docker desde dentro del
contenedor a intervalos regulares, no una sola vez bajo presión de tiempo
como la verificación del script, donde forzar `-h localhost` evita que un
`pg_isready` sin argumentos reporte "listo" contra el servidor temporal
que el entrypoint de la imagen levanta por socket Unix mientras todavía
está inicializando, antes de que el servidor real esté aceptando
conexiones.

La misma honestidad de cobertura que ya usan las demás secciones de este
README: `probar-en-docker.sh` no tiene acceso al socket de Docker, así que
se limita a confirmar que `compose.postgres.yaml` se genera con el
contenido esperado (junto con el directorio de datos y el archivo de
secreto) y que la segunda corrida no vuelve a escribir nada (`Cambios
aplicados: 0`, verificado el 3 de septiembre de 2026: en la primera
corrida, este paso por sí solo aplicó 4 cambios (el directorio de datos,
las dos escrituras de `asegurar_archivo_con_secreto` sobre el directorio y
el archivo de secreto, y `compose.postgres.yaml`); el total de la corrida
completa del pipeline depende de qué otros archivos ya existieran en el
árbol montado, no es un número fijo de este paso). Que el contenedor Postgres arranca de
verdad, que `pg_isready` responde, y que el puerto 5432 no queda expuesto
SÍ se verificó de forma directa ese mismo día, pero no contra la VM real
de Azure ni contra Vagrant (esas dos siguen pendientes): se construyó un
contenedor `debian:13` efímero con `systemd` real como proceso 1
(`/sbin/init`, `--privileged --cgroupns=host` y `/sys/fs/cgroup` montado)
y Docker anidado propio (instalado por el mismo `20-docker.sh`, sin montar
el socket del Docker del anfitrión), y se corrió
`aprovisionar.sh --solo 20-docker.sh`, luego `--solo 30-nginx.sh` y luego
`--solo 40-postgres.sh`, en ese orden, dentro de ese contenedor. Resultado:
`centinela-postgres` quedó `Up ... (healthy)` según `docker compose ps`,
`pg_isready -h localhost` confirmó `accepting connections`,
`docker compose -f compose.postgres.yaml -f compose.yaml config` mostró la
fusión correcta (`name: centinela`, ambos servicios, la red compartida), y
`ss -tln` no mostró el 5432. El datadir, después de que el entrypoint de
la imagen lo tocara, quedó con `uid=70` y `gid=0`, modo `700`. Se
inspeccionó el entrypoint mismo para confirmar que ese resultado no es
casualidad (`docker run --rm --entrypoint cat postgres:16.15-alpine3.24
/usr/local/bin/docker-entrypoint.sh`): hace `chmod 00700 "$PGDATA" || :`
(exactamente el modo que ya deja `asegurar_directorio` desde este script,
así que el chmod del entrypoint es un no-op sobre el estado que este paso
ya dejó) y `find "$PGDATA" \! -user postgres -exec chown postgres '{}' +`,
que cambia el usuario a `postgres` (UID 70 en esta imagen `-alpine`, 999
en las variantes basadas en Debian) SIN tocar el grupo, de ahí el
`gid=0` observado. Correr `--solo 40-postgres.sh` una segunda vez contra ese
mismo contenedor ya corriendo reportó `Cambios aplicados: 0`, con "El
contenedor postgres ya estaba corriendo con la configuración actual", y
`pg_isready` y `ss -tln` se repitieron limpios. El contenedor efímero
(imagen y `docker run`) se destruyó al terminar la prueba; no dejó rastro
en `/opt/centinela` de la máquina de desarrollo real, que nunca instaló
nada de esto directamente.

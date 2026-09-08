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
reiniciando la VM a diario, eso deja de ser un detalle menor.
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
anteriores del provisionador; el detalle vive en la bitácora técnica del
proyecto.

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

`50-certbot.sh` (SCRUM-30) lee de ahí dos variables:

- `CORREO_CERTBOT`: el correo que certbot registra ante Let's Encrypt. No
  tiene default: un correo no es una constante del proyecto (a diferencia de
  `DOMINIO_SERVIDOR`, que sí vive en `dominio-fijado.sh`). Si falta, el paso
  entero se omite con un `aviso` (no falla con `exigir_variable`), para que
  `probar-en-docker.sh` siga en verde sin definir nada de esto.
- `CERTBOT_STAGING`: opcional, por defecto `no`. En `si`, agrega `--staging`
  a `certonly` para probar contra el entorno de pruebas de Let's Encrypt sin
  gastar el límite real (5 validaciones fallidas por hora, 5 certificados
  duplicados por semana). Ver la advertencia de la sección `50-certbot.sh`
  más abajo antes de alternar entre `si` y `no` sobre el mismo dominio.

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
| `50-certbot.sh` | Emitir el certificado TLS con certbot y su temporizador | SCRUM-30 | Implementado |

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
`nginx/conf.d/centinela.conf`, y desde SCRUM-30 también el predicado que
decide qué snippet incluye) y `infra/servidor/pasos/50-certbot.sh` (a qué
dominio emitirle el certificado).

El valor por defecto es `veraapp.tech`: SCRUM-24 (la tarjeta que consiguió el
dominio) ya cerró, el DNS ya apunta a la IP estática del servidor (verificado
con `dig`, propagado en Cloudflare y Google), y el dominio es definitivo. El
comodín de nginx `_` ("acepta cualquier Host") ya no es el default; si hiciera
falta servir solo por IP de nuevo (por ejemplo, mientras se prueba un cambio),
`DOMINIO_SERVIDOR=_` sigue funcionando pasado por entorno o por
`aprovisionar.env`, sin tocar este archivo. Si el dominio cambiara a futuro,
esta sigue siendo la única variable que hay que actualizar, y `30-nginx.sh` la
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
`PermitRootLogin no`, `KbdInteractiveAuthentication no`. Se fija explícito
aunque cloud-init ya lo haya puesto en `50-cloud-init.conf`: ese archivo
puede cambiar o editarse a mano, y el aprovisionamiento necesita dejar el
estado explícito y verificable.

Antes de reiniciar, relee la configuración efectiva con `sshd -T` y compara
cada directiva contra lo esperado, con un chequeo aparte para `Port`
(admite varios valores a la vez, no gana el primero como el resto de las
directivas). Si algo no cuadra, corta con `error_fatal` sin tocar el
servicio. El reinicio es condicional (solo si algo cambió) y, cuando
reinicia, confirma además con `ss -tln` que sshd quedó escuchando en el
puerto nuevo. Si `ssh.socket` está habilitado (systemd escucha el puerto
en vez de sshd) o `/run/systemd/system` no existe, se omite el paso
completo con un `aviso`.

**Al mover el puerto**: la regla NSG vieja del 22 (`permitir-ssh`) y la
nueva (`permitir-ssh-${PUERTO_SSH}`, ver `infra/azure/crear-servidor.sh`)
coexisten a propósito durante la transición. Verificar desde otra terminal
que el puerto nuevo conecta ANTES de cerrar la sesión actual, y borrar la
regla vieja del NSG a mano (nunca desde un script) recién después.

El porqué de estos dos chequeos (por qué `10-` y no otro número, por qué
`Port` se verifica distinto al resto de las directivas) y la secuencia
completa de la transición contra la VM real quedan documentados en la
bitácora técnica del proyecto.

### `20-docker.sh`

Instala Docker Engine y el plugin `docker compose` desde los paquetes de
Debian 13 (`docker.io`, `docker-cli`, `docker-compose`), no con
`get.docker.com`. El paquete `docker-compose` de Debian es Compose v2 bajo
el nombre viejo, no el v1 obsoleto: deja el binario en
`/usr/bin/docker-compose` y también como plugin en
`/usr/libexec/docker/cli-plugins/docker-compose`, así que tanto
`docker-compose version` como `docker compose version` funcionan.
`docker-cli` se instala explícito porque los otros dos paquetes solo lo
declaran `Recommends`, no `Depends`.

El usuario administrador (`centinela`) no entra al grupo `docker` en este
paso (equivale a acceso root sin contraseña sobre el host, decisión que le
corresponde al equipo, no a la tarjeta SCRUM-31): mientras no se decida,
los comandos `docker` en el servidor se ejecutan con `sudo`.

Verificación en dos niveles: `docker --version` y `docker compose version`
corren siempre, sin depender del daemon (incluso en el contenedor sin
systemd de `probar-en-docker.sh`); `docker info` solo corre cuando
`/run/systemd/system` existe, y en la VM real confirma que el daemon de
verdad responde por el socket, no solo que systemd lo reporta activo.

El porqué de la elección apt sobre `get.docker.com`, los dos gotchas de
nombre de paquete, y la verificación completa contra la VM real quedan
documentados en la bitácora técnica del proyecto.

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
`proxy_pass $api;`. Con esto nginx arranca solo, sin que exista todavía
ningún contenedor `api`, y responde `502 Bad Gateway` en vez de fallar el
arranque. Verificado contra la VM real, documentado en la bitácora técnica
del proyecto.

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
(`dominio-fijado.sh`, ver más abajo), `veraapp.tech` desde que SCRUM-24
cerró (ver la sección `dominio-fijado.sh`).

#### Predicado de propiedad frente a SCRUM-30 (certbot)

Un solo predicado, `[[ -f /opt/centinela/letsencrypt/live/${DOMINIO_SERVIDOR}/fullchain.pem ]]`
(variables `DIRECTORIO_LETSENCRYPT` y `ARCHIVO_FULLCHAIN_TLS`), decide tres cosas en este paso:

- **(a) Qué snippet incluye `centinela.conf`**: `raiz-http.conf` (proxy
  directo, propiedad de este mismo paso) mientras no exista el certificado,
  `raiz-redirigir.conf` (redirección a HTTPS, propiedad de `50-certbot.sh`,
  nunca escrito por este paso) en cuanto exista.
- **(b) Si el chequeo suelto de `nginx -t` de este paso monta
  `/etc/letsencrypt`**: solo si el certificado ya existe.
- **(c) Un reload condicional del contenedor** (`docker kill -s HUP
  centinela-nginx`) cuando el CONTENIDO de `centinela.conf` cambió (el
  predicado volteó de snippet) pero `asegurar_servicio_compose` no recreó el
  contenedor. Mismo patrón que el reinicio condicional de sshd en
  `10-ssh.sh`.

`raiz-http.conf` sigue siendo 100 % propiedad de este paso: `50-certbot.sh`
nunca lo toca, solo agrega su propio `raiz-redirigir.conf` y decide (este
paso, no aquel) cuál de los dos se incluye. El efecto completo (snippet de
redirección + puerto 443) no se aplica hasta la corrida de
`aprovisionar.sh` SIGUIENTE a la que emitió el certificado por primera vez
(ver `50-certbot.sh` más abajo).

`probar-en-docker.sh` no puede levantar el contenedor nginx (sin acceso al
socket de Docker): se limita a confirmar que `compose.yaml`,
`centinela.conf` y `raiz-http.conf` se generan con el contenido esperado y
que la segunda corrida no los reescribe. Que el contenedor arranca de
verdad, que `nginx -t` pasa, y que el puerto queda escuchando depende de la
VM real (Azure o Vagrant).

El porqué de este diseño (la revisión que forzó a abandonar el reparto de
archivos original, y la verificación en vivo contra Azure) queda
documentado en la bitácora técnica del proyecto.

### `40-postgres.sh`

Instala y configura Postgres en contenedor Docker (tarjeta SCRUM-33). El volumen de datos es un
bind mount a `/opt/centinela/postgres/datos`, montado en `/var/lib/postgresql/data` dentro del
contenedor: fuera del árbol que gestiona `docker compose down -v` (que solo alcanza volúmenes
NOMBRADOS) y fuera de `infra/servidor/` (que se limpia en cada arranque en `/tmp`, ver "VM de
Azure" arriba). El directorio se crea con permiso `700`; la imagen oficial de Postgres hace
`chown` al usuario `postgres` dentro de su propio entrypoint al primer arranque.

Compose multi-archivo: `30-nginx.sh` sigue siendo dueño exclusivo de `compose.yaml` (con `name:
centinela` y el `networks:` compartido); este paso escribe su propio `compose.postgres.yaml`, solo
con `services: postgres:`. `asegurar_servicio_compose` (`lib/comun.sh`) arma con un glob
`compose*.yaml` la lista de fragmentos y los pasa todos como `-f` tanto al `ps -q` de "antes" como
al `up -d`; ningún paso reescribe el bloque `services:` de otro y el orden en que corran los dos
pasos no importa.

La contraseña se genera una sola vez y se persiste en `/opt/centinela/postgres/postgres.env`
(`POSTGRES_PASSWORD`, permisos `600`), fuera de `infra/servidor/` por la misma razón que el
directorio de datos. `asegurar_archivo_con_secreto` no hace nada si el archivo ya existe
(idempotente por la propia existencia del archivo); `generar_secreto` arma 32 caracteres
alfanuméricos desde `/dev/urandom` + `base64`. Rotar la contraseña de verdad requiere `ALTER ROLE`
dentro de la base, no solo editar el archivo.

`compose.postgres.yaml` no publica ningún `ports:`: solo lo consume la app dentro de la red
`centinela`, por nombre de contenedor. La verificación del script reintenta
`docker exec centinela-postgres pg_isready -U centinela -d centinela -h localhost` (30 intentos,
2s) y confirma con `ss -tln` que el puerto 5432 NO aparece escuchando en el host.

El razonamiento de diseño (por qué compose multi-archivo, por qué el volumen fuera del árbol
desplegado) y la verificación completa contra un contenedor con systemd real quedan documentados
en la bitácora técnica del proyecto.

### `50-certbot.sh`

Emite el certificado TLS con certbot y programa su renovación (tarjeta
SCRUM-30). El dominio ya es definitivo (`veraapp.tech`, ver
`dominio-fijado.sh`). Nunca toca el contenedor `nginx` ni escribe
`raiz-http.conf`: solo deja archivos en disco (`compose.certbot.yaml`,
`nginx/conf.d/centinela-tls.conf`, `nginx/snippets/raiz-redirigir.conf`) y
gestiona el certificado y su timer de renovación. Es `30-nginx.sh` quien
recoge esos archivos la próxima vez que corra completo: la activación real
del puerto 443 y la redirección a HTTPS quedan diferidas una corrida
completa de `aprovisionar.sh` respecto a la que emite el certificado por
primera vez (`30-nginx.sh` corre en la posición `30`, antes que este paso
en `50`, así que en la corrida de emisión nginx ya está sirviendo el
puerto 80 pero todavía no ve ni el certificado ni `compose.certbot.yaml`).

**Guards de salto**, en orden, cada uno con su propio `aviso` (nunca
fatales, para que `probar-en-docker.sh` siga en verde sin definir nada de
esto): (1) `systemd` no es el proceso 1; (2) `DOMINIO_SERVIDOR` sigue
siendo el comodín `_`; (3) falta `CORREO_CERTBOT` en `aprovisionar.env`
(ver "Configuración local" más arriba).

**Emisión**: contenedor efímero `docker run --rm` con el plugin webroot,
montando `nginx/webroot` (sin `:ro`: certbot necesita escribir el reto ahí)
y `/etc/letsencrypt` COMPLETO (no solo `live/`: son symlinks hacia
`archive/`). Idempotente por existencia de `fullchain.pem`: si ya existe,
no se vuelve a invocar `certonly`.

**Trampa de `CERTBOT_STAGING`**: un certificado de staging queda en la
MISMA ruta (`live/${DOMINIO_SERVIDOR}/fullchain.pem`) que uno real. Como la
idempotencia de este paso decide por la existencia del archivo, no por su
origen, alternar `CERTBOT_STAGING=si` a `no` sobre un dominio que ya tiene
un certificado de staging NO dispara una reemisión real. Para forzarla:
borrar a mano `live/`, `archive/` y `renewal/*.conf` del dominio antes de
volver a correr el paso. El paso imprime un `aviso` explícito cada vez que
`CERTBOT_STAGING=si` está activo, recordando esto.

**Cutover de staging a producción**: si `nginx/conf.d/centinela-tls.conf`
ya existe en disco (de una emisión anterior) y se borra el certificado a
mano para forzar la reemisión, una corrida de `aprovisionar.sh` completo
hace fallar el `nginx -t` de `30-nginx.sh` ("cannot load certificate"): el
predicado de `30-nginx.sh` ve "no existe certificado" y no monta
`/etc/letsencrypt` en su chequeo, mientras `centinela-tls.conf` sigue
exigiendo esa misma ruta. **Forma recomendada de hacer este cutover
mientras nadie automatice el reload o el gate**: reemitir con
`aprovisionar.sh --solo 50-certbot.sh` (nunca ejecuta `30-nginx.sh` ni su
`nginx -t`) y recargar nginx a mano con `docker kill -s HUP
centinela-nginx` inmediatamente después, en vez de correr `aprovisionar.sh`
completo.

**Imagen fijada por tag, mismo criterio que nginx y Postgres**:
`certbot/certbot:v5.8.0`.

**Puerto 443 y volumen de certificados, vía `compose.certbot.yaml`**:
agrega `ports: ["443:443"]` y
`volumes: ["/opt/centinela/letsencrypt:/etc/letsencrypt:ro"]` al servicio
`nginx` (dueño en `compose.yaml`), sin declarar `name:` ni `networks:`,
mismo patrón de fusión multi-archivo que `compose.postgres.yaml` (ver
`40-postgres.sh` arriba).

**Timer de renovación con systemd, NO con cron**: `OnBootSec=15min` +
`OnUnitActiveSec=24h` en el `.timer`, nunca `OnCalendar=daily` +
`Persistent=true` (razón: `Persistent=true` solo dispara una corrida
perdida si existe un stamp de una activación previa, y no lo hay la
primera vez que el timer se activa tras un reinicio de la VM; SCRUM-29
reinicia la VM a diario). El `.service` declara DOS `ExecStart`: primero
`docker run --rm ... certbot/certbot:v5.8.0 renew --webroot -w
/var/www/certbot`, y si ese comando sale con código 0,
`docker kill -s HUP centinela-nginx` (con `Type=oneshot`, si el primer
`ExecStart` falla el segundo no se ejecuta). El hook de despliegue de
certbot (`--deploy-hook`) no sirve para esto: corre DENTRO del propio
contenedor efímero de certbot, sin acceso al socket de Docker del host.
`certbot renew` sale con código 0 incluso cuando no hay nada que renovar,
así que el segundo `ExecStart` se ejecuta en CADA disparo del timer que no
falle, no solo cuando de verdad renovó un certificado (el reload es
barato, sin caída de conexiones).

El razonamiento completo de estas decisiones (por qué se descartó que
`50-certbot.sh` recreara el contenedor directamente, la revisión de
propiedad frente a `30-nginx.sh`, y la verificación en vivo contra Let's
Encrypt real y contra Azure, incluido el cutover de staging a producción)
queda documentado en la bitácora técnica del proyecto.

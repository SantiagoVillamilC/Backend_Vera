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
rsync -az infra/servidor/ usuario@servidor:/opt/centinela/infra-servidor/
ssh usuario@servidor 'sudo /opt/centinela/infra-servidor/aprovisionar.sh'
```

### VM local con Vagrant

```bash
vagrant up
```

Levanta la caja `cloud-image/debian-13` (versión `20260831.2587.0`) definida
en el `Vagrantfile` de la raíz del repositorio, con 2048 MB de RAM y 1 vCPU
(para reflejar el `Standard_B1ms` de Azure), y ejecuta `aprovisionar.sh`
mediante un provisionador de shell en modo `inline` que llama a
`/vagrant/infra/servidor/aprovisionar.sh`. Esa ruta es la del directorio del
proyecto ya sincronizado dentro de la VM, así que `lib/comun.sh` y `pasos/`
llegan junto con el script principal.

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
[`infra/local/preparar-entorno.sh`](../local/preparar-entorno.sh).

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
| `10-ssh.sh` | Endurecer el acceso por SSH | SCRUM-28 | Pendiente |
| `20-docker.sh` | Instalar Docker y Docker Compose | SCRUM-31 | Pendiente |
| `30-nginx.sh` | Instalar y configurar nginx como proxy inverso | SCRUM-32 | Pendiente |
| `40-postgres.sh` | Instalar y configurar Postgres en contenedor | SCRUM-33 | Pendiente |
| `50-certbot.sh` | Emitir el certificado TLS con certbot y su temporizador | SCRUM-30 | Pendiente |

### `imagen-fijada.sh`

Única fuente de verdad del sistema operativo y la imagen que deben correr los
tres destinos (VM de Azure, VM local de Vagrant, contenedor de prueba):
`IMAGEN_URN`, `IMAGEN_VERSION_ESPERADA`, `SERIAL_IMAGEN_DEBIAN`,
`IMAGEN_OS_ID` e `IMAGEN_OS_VERSION_ID`. Solo declaraciones
`: "${VAR:=valor}"`, sin lógica. Vive dentro de `infra/servidor/` a
propósito: es lo único que viaja con el `rsync` al servidor real (ver más
arriba), así que `aprovisionar.sh` lo carga con `source` justo después de
`lib/comun.sh`, antes de recorrer los pasos, y todos ellos (no solo el gate)
pueden usar sus variables.

[`infra/azure/variables.sh`](../azure/variables.sh), que sí queda fuera del
`rsync`, hace `source` de este archivo (resuelto con `BASH_SOURCE`, no con una
ruta relativa al directorio de trabajo) en vez de duplicar los valores. **Si
cambia la imagen fijada, este es el único archivo que hay que tocar.**

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

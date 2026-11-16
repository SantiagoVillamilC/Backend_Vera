# Modelos de Vera en RunPod: instalación en la VM de Azure

Fecha: 15 de noviembre de 2026. Estado: **código integrado y probado sin pod, nada aplicado**. No se creó ningún pod, no se tocó
Azure, la VM, GitHub ni RunPod. Este documento es la lista de cambios que Christian aprueba antes de ejecutar nada.

Marcas: **[V]** verificado (con cómo), **[I]** inferido, **[N]** no verificado y por comprobar.

Complementa el diseño aprobado (`docs/despliegue-modelos-runpod.md` de la rama de despliegue, aún no está en `dev`). Lo que
cambia respecto de ese diseño está en la sección 15.

## 0. Tres cosas antes de leer el resto

**1. Decisión de red (opción B, sin puentes nginx).** La API corre en el contenedor `centinela-api`, en la red docker
`centinela` (`infra/servidor/pasos/60-api.sh`, `networks: - centinela`) [V]. Dentro del contenedor `127.0.0.1` es su propio
loopback, así que no llega a lo que escucha en la VM. Solución aprobada:

```
 centinela-api (red docker "centinela")
     VERA_*_URL = http://GATEWAY:{11434,8767,8766}      GATEWAY = IP del gateway de esa red, leída de la VM real
     VERA_MODELOS_HOST_PERMITIDO = GATEWAY               (solo esa IP privada exacta, ninguna otra)
          |  TCP dentro de la VM
          v
 modelos-control (servicio systemd en el host) escucha en GATEWAY:{11434,8766,8767}
```

- `GATEWAY` **no se inventa**: se lee con `docker network inspect centinela --format '{{(index .IPAM.Config 0).Gateway}}'`.
  Hasta entonces es el marcador `<GATEWAY_DE_DOCKER>` en `.env.example` y en la plantilla del compose. Con el marcador sin
  sustituir `modelos-control` no arranca (configuración inválida, código 2) [V, ejecutado en esta máquina].
- `modelos-control` acepta escuchar en loopback o en una IPv4 literal de 10/8, 172.16/12 o 192.168/16, nunca en `0.0.0.0` ni en un
  nombre de host (`internal/modelos/config/config.go`, `direccionEscucha`) [V]. Los clientes de la API (`vozlocal`, `sttlocal` y la
  validación de `VERA_OLLAMA_URL`) aceptan esa IP exacta solo si está en `VERA_MODELOS_HOST_PERMITIDO`
  (`internal/copilot/destinolocal`) [V].
- `infra/modelos-runpod/vm/red-api.sh` escribe el gateway en los dos únicos sitios donde debe coincidir (`modelos-control.env` y
  `compose.z-modelos.yaml`) y `verificar` delata cualquier desfase.
- Probado en esta máquina con un contenedor de una red docker temporal que alcanzó el binario real de `modelos-control` ligado a
  la IP del gateway de esa red y recibió `503 modelos_apagados` (sección 7) [V]. Falta repetirlo en la VM real [N].

**2. Orden de integración.** Esta rama (`integracion/modelos-runpod`) junta cuatro ramas locales sin PR: `feat/kokoro-gpu`,
`feat/modelos-host-privado`, `feat/modelos-control` y `feat/modelos-runpod-infra`. La última nació con puentes nginx y aquí se
alineó con la decisión B y con el binario real. El orden de PRs está en la sección 14. El commit de `MODELOS_REPO_SHA` debe contener
la variante GPU de Kokoro, así que solo existe después de que `feat/kokoro-gpu` entre en `dev`.

**3. Qué se prueba sin gastar y qué no.** Hay pruebas automáticas que no necesitan pod (sección 8). La instalación real de Ollama,
pip, las descargas, el arranque de los tres servidores, la VRAM, RunPod y la VM solo se pueden verificar con un pod.

## 1. Arquitectura

```
 Usuario (app Vera) -> HTTPS -> nginx -> contenedor centinela-api (red docker "centinela")
                                           VERA_PROVEEDOR=local, VERA_*_URL hacia GATEWAY, VERA_MODELOS_HOST_PERMITIDO=GATEWAY
                                                |  TCP hacia GATEWAY (IP del bridge "centinela", dentro de la VM)
 VM centinela-vm (Debian 13) ------------------ v -------------------------------------------
   vera-modelos-control.service  (usuario vera-modelos, sin shell)
      escucha GATEWAY:{11434,8766,8767}  (proxy HTTP, 503 rápido con el pod apagado)
      y 127.0.0.1:8790 (control: POST /encender, POST /apagar, GET /estado, con token Bearer)
      crea/apaga el pod por la API de RunPod, abre el túnel SSH (saliente) y reconcilia
   vera-modelos-guardian.timer   cada 5 min: modelos-control reconcile (pods huérfanos o vencidos)
                                                |  ssh -L, saliente, hacia IP pública y puerto del pod
 RunPod, pod "vera-modelos" (RTX A5000, Secure, imagen runpod/pytorch Ubuntu 24.04, sin Docker, sin volumen de red)
   arrancar-modelos.sh  ->  Whisper small GPU :8767 (X-Vera-Token), Kokoro fp32 CUDA :8766 (X-Vera-Token),
                            Ollama + gemma4-e4b-local :11434. Todo en 127.0.0.1 del pod.
```

Quién enciende y apaga: el botón (control de `modelos-control`), el apagado por inactividad de 10 minutos y el guardián.
Tope duro de gasto: el saldo prepagado bajo de la cuenta de RunPod.

## 2. Contrato con `modelos-control` (M1)

Leído del código integrado (`internal/modelos/config`, `internal/modelos/control`, `cmd/modelos-control`) [V]. `modelos-control
--seco` imprime la configuración efectiva sin tocar la red ni leer secretos, y es la referencia de los valores por defecto.

| Pieza | Valor real |
|-------|-----------|
| Uso | `modelos-control [--seco] [servir]`, `modelos-control reconcile` (no existe `estado` como subcomando) |
| Clave de RunPod | `RUNPOD_API_KEY` del entorno cuando `MODELOS_ARCHIVO_CLAVE_RUNPOD` está **definido y vacío** (si no se define, lee `/etc/vera/runpod.env`) |
| Tokens de voz y STT | archivo `MODELOS_ARCHIVO_TOKENS` con `VERA_STT_TOKEN` y `VERA_VOZ_TOKEN`, modo 0600 o más estricto, **legible por el usuario del servicio** |
| Token de control | archivo `MODELOS_ARCHIVO_TOKEN_CONTROL` (una línea), mismo requisito |
| Llave SSH | `MODELOS_LLAVE_SSH` y `MODELOS_LLAVE_PUBLICA_SSH` (esta última se inyecta en el pod como `PUBLIC_KEY`) |
| Pod | `MODELOS_GPUS` (coma, por defecto A5000 y A4000), `MODELOS_IMAGEN`, `MODELOS_DISCO_GB` (40), `MODELOS_TARIFA_TOPE_USD_H` (0,35), `MODELOS_INACTIVIDAD` (`10m`), `MODELOS_SESION_MAXIMA` (`4h`) |
| Escucha de los modelos | `MODELOS_ESCUCHA_{OLLAMA,VOZ,STT}` con formato `IP:puerto`: loopback o IPv4 privada literal. Valores por defecto en loopback (`11434`, `8766`, `8767`) |
| Solo loopback | `MODELOS_PUERTO_CONTROL` (`127.0.0.1:8790`) y los extremos del túnel `MODELOS_INTERNO_*` (`18434`, `18766`, `18767`) |
| Script | `MODELOS_SCRIPT_ARRANQUE` (en la VM), `MODELOS_SCRIPT_REMOTO` (en el pod, `/tmp/vera-arranque.sh`), `MODELOS_REPO_SHA` |
| Estado | `MODELOS_DIR_ESTADO` (por defecto `/var/lib/modelos-control`, las unidades usan `/var/lib/vera-modelos`) |
| Esperas | pod 10 m, ssh 10 m, arranque 25 m, modelos 10 m, vigilancia 30 s (`MODELOS_ESPERA_*`, `MODELOS_VIGILANCIA`) |
| Pruebas | `MODELOS_RUNPOD_URL` (https, o http solo hacia loopback), `MODELOS_SSH_BIN`, `MODELOS_SCP_BIN` |

Cómo habla `modelos-control` con el pod y qué hace `arrancar-modelos.sh` con eso [V, leído en `control/encendido.go` y `control/sonda.go`]:

1. Copia el script por `scp` a `MODELOS_SCRIPT_REMOTO`.
2. Ejecuta `ssh ... bash -s` y por stdin envía `export WHISPER_TOKEN='...'`, `export KOKORO_TOKEN='...'`, `export REPO_SHA='...'` (este solo si
   `MODELOS_REPO_SHA` no está vacío) y `bash /tmp/vera-arranque.sh >/tmp/vera-arranque.sh.log 2>&1 </dev/null || { ...; exit $c; }`. Los tokens no
   van en argv. Espera a que el comando **termine** (hasta 25 minutos). Si el script falla, devuelve el final de su log por stderr.
3. `arrancar-modelos.sh` toma los tokens del entorno (`WHISPER_TOKEN` y `VERA_LAB_TOKEN` o `KOKORO_TOKEN`), los quita de su propio entorno para que no los hereden
   `pip` ni `curl`, y los guarda en `/workspace/vera-estado/tokens.env` (0600) para `salud-modelos.sh`. `REPO_SHA` equivale a `VERA_CODIGO_SHA`.
   Los servidores leen `WHISPER_TOKEN` y `VERA_LAB_TOKEN` (no `KOKORO_TOKEN`) [V en `servidor.py`].
4. Los servicios quedan en segundo plano con `setsid nohup`, stdin nulo y salida a un log, sin heredar el bloqueo (`9>&-`), y el script **sale con 0 solo al
   final**, cuando Whisper, Kokoro y Ollama ya respondieron. No hay `exec sleep infinity`, así que el `ssh` vuelve [V, `modelos-pod_test.sh`].
5. Después `modelos-control` abre el túnel y sondea por él: Ollama `GET /api/version` sin token, Whisper y Kokoro `GET /healthz` con `X-Vera-Token`.
   Los puertos del pod que reenvía son 11434, 8767 y 8766 y coinciden con los del script [V]. M1 no lee `progreso.jsonl` ni ejecuta `salud-modelos.sh`.

Esta alineación quedó fijada por tres pruebas: la de Go que exige las líneas exactas del stdin (`internal/modelos/control/encendido_test.go`), la de bash que
ejecuta ese stdin literal contra el script (`pod/modelos-pod_test.sh`) y la de `servir` de punta a punta de la sección 7.

Formato de `progreso.jsonl` (en el pod, `/workspace/vera-estado/progreso.jsonl`, una línea por evento, se trunca al empezar cada corrida):

```json
{"epoch": 1763251200, "paso": "gguf", "estado": "en_curso", "codigo": 0, "detalle": ""}
```

La última línea siempre es `listo` o `fallo` (una trampa `EXIT` lo garantiza). Se toma un `flock` antes de truncar: una segunda corrida sale con 5 sin tocar el
progreso de la primera.

Códigos de salida de `arrancar-modelos.sh` (visibles para M1 como fallo del `ssh`):

| Código | Significado | Qué debería hacer `modelos-control` |
|--------|-------------|-------------------------------------|
| 0 | Listo (o reutilizado: los tres ya respondían) | Abrir túnel y servir |
| 1 | Fallo genérico (falta una herramienta, Ollama no se pudo fijar) | Reintentar una vez, luego terminar el pod |
| 2 | Entrada inválida (tokens, variables, falta el código) | No reintentar: es configuración |
| 3 | sha256 o tamaño del GGUF distinto del lockfile | No reintentar con el mismo commit |
| 4 | Un servicio no quedó sano a tiempo | Reintentar una vez |
| 5 | Ya hay otra corrida en curso | Esperar, no relanzar |
| 13 | Ollama no cabe entero en VRAM | Terminar el pod, probar otra GPU |
| 14 | El driver del host no ofrece CUDA 13.0 | **Terminar el pod y crear otro** (otro host) |
| 15 | Kokoro abortó porque `CUDAExecutionProvider` no cargó | Igual que 14 |

Hoy M1 trata cualquier fallo del script como error de arranque: termina el pod y deja el motivo, sin distinguir 14 y 15 [V, `encender` en `encendido.go`]. Reintentar
en otro host solo con 14 y 15 es una mejora por pedirle.

**Hallazgos de la integración** (el binario acepta más de lo que el script necesita, no se corrigió el Go porque un arreglo hecho solo en la rama de integración no llegaría
a `dev` por los PRs por rama):

- **`MODELOS_REPO_SHA` es obligatorio en la práctica.** `modelos-control` lo acepta vacío o corto (`[A-Za-z0-9._-]{0,64}`). En un pod nuevo el script exige 40 caracteres
  hexadecimales y sale con 2, **después de crear y cobrar el pod**. `red-api.sh verificar` marca el valor de ceros del `.env.example` como pendiente.
- **Alfabeto de los tokens.** `modelos-control` admite `[A-Za-z0-9._~+=/-]{32,200}` y el script solo `[A-Za-z0-9_-]{32,}`. Los tokens de `generar-secretos.sh` son hexadecimales y
  pasan por los dos. Un token con `.` o `+` pasaría en la VM y fallaría en el pod con código 2.
- **El repositorio es público** (`gh repo view`: `PUBLIC`) [V], así que `codeload.github.com/SantiagoVillamilC/Backend_Vera/tar/<sha>` responde sin credenciales. El commit solo existirá
  cuando los PRs entren a una rama del repositorio [I].
- `OLLAMA_NUM_PARALLEL` no se lo pasa M1 al script: queda en 2.
- Con RunPod inaccesible al arrancar, `servir` queda en estado `error` con el mensaje `no se pudo reconciliar con RunPod al arrancar` y sigue respondiendo 503 [V, ejecutado]. Con
  el directorio de estado de una corrida anterior, el mensaje viejo sobrevivió aunque el estado volvió a `apagado` [V]. Si el estado `error` se recupera solo o exige `POST /encender` no se probó [N].

## 3. Lista de cambios que Christian aprueba antes de ejecutar

Todo es nuevo. **No se modifica ningún archivo existente de la VM** salvo `compose.api.yaml` indirectamente: la API se recrea con un override. El orden de la sección 4 es el de esta tabla.

| # | Qué | Dónde | Dueño y modo | Comando que lo crea |
|---|-----|-------|--------------|---------------------|
| C1 | Copia del repositorio de la pieza | `/opt/vera-modelos/` (`pod/`, `vm/`, `systemd/`, `.env.example`) | root:root 0755, scripts 0755 | `scp` y `sudo cp -rT` |
| C2 | Binario `modelos-control` | `/usr/local/bin/modelos-control` | root:root 0755 | `sudo install` (sección 5) |
| C3 | Usuario de sistema `vera-modelos` | `/etc/passwd` | sin shell (`/usr/sbin/nologin`), sin home, **fuera del grupo docker** | `generar-secretos.sh` (`useradd --system`) |
| C4 | Configuración y clave de RunPod | `/etc/vera-modelos/modelos-control.env` | root:root **0600** | `generar-secretos.sh` y `red-api.sh instalar` |
| C5 | Tokens de voz y STT para `modelos-control`, y token de control | `/etc/vera-modelos/tokens.env` y `control.token` | vera-modelos **0600** | `generar-secretos.sh` |
| C6 | Tokens de la API (solo `VERA_STT_TOKEN` y `VERA_VOZ_TOKEN`, **sin** la clave de RunPod) | `/opt/centinela/api/modelos.env` | root:root **0600** | `generar-secretos.sh` |
| C7 | Llave SSH del túnel (VM hacia pod) | `/etc/vera-modelos/ssh/tunel_ed25519` y `.pub` | privada vera-modelos **0600**, pública 0644 | `generar-secretos.sh` (`ssh-keygen -t ed25519`) |
| C8 | Tres unidades systemd | `/etc/systemd/system/vera-modelos-control.service`, `vera-modelos-guardian.service` y `.timer` | root:root 0644 | `sudo install` |
| C9 | Carpeta de estado | `/var/lib/vera-modelos` | vera-modelos 0700 (la crea systemd) | `StateDirectory=` |
| C10 | Override de compose de la API | `/opt/centinela/infra-servidor/compose.z-modelos.yaml` | root:root 0644 | `red-api.sh instalar` |
| C11 | **Recreación de `centinela-api`** con `VERA_PROVEEDOR=local` | docker | n/a | `red-api.sh aplicar` |
| C12 | Activar `vera-modelos-control.service` y el temporizador del guardián | systemd | n/a | `systemctl enable --now` |

- **Firewall y red de Azure: ninguna regla nueva.** El túnel SSH hacia el pod y las llamadas a la API de RunPod son **salientes**. Los sockets nuevos son
  `127.0.0.1:8790` (control) y `GATEWAY:{11434,8766,8767}` (IP del bridge docker, alcanzable desde la VM y sus contenedores). Nada escucha en `0.0.0.0`. La NSG no se toca.
  El repositorio no configura firewall de host (solo la NSG de Azure) [V: sin resultados de `ufw|nftables|iptables` en `infra/servidor`]. En esta máquina un contenedor de una
  red docker alcanzó la IP del gateway sin reglas adicionales [V]. En la VM de Azure falta comprobarlo [N].
- **Tiempo de indisponibilidad:** C11 recrea la API: unos segundos de 502 en nginx. Hacerlo fuera de horario de uso.
- **Lo que NO se hace:** no se edita `banderas.tsv`, `.env.*.example` de destino, `compose.api.yaml`, `deploy-ci.sh` ni `authorized_keys`. No se crean usuarios en el grupo `docker`.
  No se abre ningún puerto en Azure. No hay contenedores nuevos ni puentes.

## 4. Procedimiento exacto

Prerrequisitos: los cuatro PRs de la sección 14 en `dev`, y `go` instalado en tu máquina (`command -v go`: go1.27.4 [V en esta máquina]). La VM es `centinela-vm`,
Debian 13, en `centinela-rg` (diseño aprobado [V]).

### Paso 0. Conectarte (solo lectura de Azure)

```bash
IP="$(az vm show -d -g centinela-rg -n centinela-vm --query publicIps -o tsv)"
PUERTO="$(bash -c '. infra/servidor/red-fijada.sh && printf %s "$PUERTO_SSH"')"   # 22022 por defecto
ssh -t -p "$PUERTO" centinela@"$IP"
```

Usa `ssh -t` (terminal real) en los pasos que piden la clave de RunPod. **No uses `az vm run-command` para el paso 4**: el texto del script queda guardado en Azure y la
clave no debe pasar por ahí. Sí sirve para pasos sin secretos.

### Paso 1. Copiar los archivos (C1 y C2)

```bash
# En tu máquina, desde un checkout de dev. Compila todos los comandos, incluido modelos-control, en out/bin:
make build
sha256sum out/bin/modelos-control
scp -P "$PUERTO" -r infra/modelos-runpod centinela@"$IP":/tmp/vera-modelos
scp -P "$PUERTO" out/bin/modelos-control centinela@"$IP":/tmp/modelos-control
# En la VM:
sudo install -d -m 0755 -o root -g root /opt/vera-modelos
sudo cp -rT /tmp/vera-modelos /opt/vera-modelos
sudo chown -R root:root /opt/vera-modelos
sudo chmod 0755 /opt/vera-modelos/pod/*.sh /opt/vera-modelos/vm/*.sh
sha256sum /tmp/modelos-control      # debe coincidir con el de tu máquina
sudo install -m 0755 -o root -g root /tmp/modelos-control /usr/local/bin/modelos-control
```

`scp` y `cp -rT` son el mismo patrón de `infra/servidor/README.md`: la imagen de Azure no trae `rsync` [V en ese README]. Otras formas de entregar el binario, en la
sección 5. Rollback: `sudo rm -rf /opt/vera-modelos /usr/local/bin/modelos-control` (en la VM, ruta fija, sin variables).

### Paso 2. Comprobar prerrequisitos en la VM (solo lectura)

```bash
command -v openssl ssh ssh-keygen scp docker systemd-analyze
docker compose version
docker network inspect centinela --format '{{(index .IPAM.Config 0).Gateway}}'
sudo ss -ltn
```

`openssl`, `ssh`, `scp` y `ssh-keygen` en la VM son **[N]**: `modelos-control` invoca `/usr/bin/ssh` y `/usr/bin/scp`, y `generar-secretos.sh` necesita `openssl` y
`ssh-keygen`. Si falta `openssl` o `ssh-keygen`, el script aborta con código 69 antes de cambiar nada. Si falta `ssh` o `scp`, instala `openssh-client` con la ventana
de mantenimiento acordada. El comando de `docker network inspect` imprime el gateway real. Anótalo: es el valor que debe leer también `red-api.sh`.

### Paso 3. Secretos (C3 a C7): sin que pasen por el chat ni por el repositorio

```bash
sudo bash /opt/vera-modelos/vm/generar-secretos.sh --seco --codigo-sha <COMMIT_DE_40_CARACTERES>
sudo bash /opt/vera-modelos/vm/generar-secretos.sh --codigo-sha <COMMIT_DE_40_CARACTERES>
```

La primera línea es el ensayo: imprime lo que haría y no cambia nada. La segunda pide la clave de RunPod **en la terminal, sin eco** (`read -rs`): se pega ahí, nunca en el chat,
nunca como argumento. Los tokens se generan con `openssl rand -hex 32` y no se imprimen. Se imprime solo la parte pública de la llave SSH, que no es secreta. El
`<COMMIT_DE_40_CARACTERES>` es el commit de `dev` que ya contiene los cuatro PRs. Ver la sección 6.

### Paso 4. Escribir el gateway en `modelos-control.env` y en el override (C4 y C10)

```bash
sudo bash /opt/vera-modelos/vm/red-api.sh gateway                 # solo lectura, imprime el gateway de la red centinela
sudo bash /opt/vera-modelos/vm/red-api.sh instalar --seco         # imprime los cambios, no escribe nada
sudo bash /opt/vera-modelos/vm/red-api.sh instalar                # escribe el gateway y genera compose.z-modelos.yaml
sudo cat /opt/centinela/infra-servidor/compose.z-modelos.yaml
sudo bash /opt/vera-modelos/vm/red-api.sh verificar               # RESULTADO=OK
```

`instalar` reemplaza el marcador `<GATEWAY_DE_DOCKER>` de las tres líneas `MODELOS_ESCUCHA_*` por el gateway leído de docker, conserva el resto del archivo (incluida la
clave de RunPod, que nunca se imprime) y genera el override. **Revisa el override con `cat`, no con `docker compose config`**: `config` mezcla el contenido de todos los
`env_file` e imprimiría en la terminal `JWT_SECRET`, los peppers, `CUSTODIO_TOKEN`, las URL de base de datos con contraseña y los tokens de los modelos. El override
generado **no contiene ningún secreto** [V, hay una prueba]. Debe mostrar `VERA_PROVEEDOR: "local"`, `VERA_MODELOS_HOST_PERMITIDO` con el gateway y las tres URL con ese mismo gateway.

### Paso 5. Instalar las unidades y comprobarlas (C8, C9)

```bash
sudo install -m 0644 /opt/vera-modelos/systemd/vera-modelos-control.service \
  /opt/vera-modelos/systemd/vera-modelos-guardian.service /opt/vera-modelos/systemd/vera-modelos-guardian.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemd-analyze verify /etc/systemd/system/vera-modelos-control.service /etc/systemd/system/vera-modelos-guardian.service
sudo systemd-analyze security vera-modelos-control.service
```

En esta máquina `systemd-analyze verify` (systemd 262) solo se quejó de que `/usr/local/bin/modelos-control` no existe [V]. La VM usa Debian 13 con systemd 257 [N]. La exposición de
`systemd-analyze security` no se recalculó tras los cambios de reinicio de la unidad.

### Paso 6. Ensayo en seco de `modelos-control` (no crea nada, no gasta)

```bash
sudo systemd-run --wait --pipe --collect -p User=vera-modelos -p StateDirectory=vera-modelos \
  -p EnvironmentFile=/etc/vera-modelos/modelos-control.env /usr/local/bin/modelos-control --seco
```

Valida la configuración y muestra el plan **sin imprimir secretos** [V, `internal/modelos/secreto`]. En `escucha` deben aparecer las tres direcciones con el gateway, y en
`rutas_de_secretos` las rutas de `/etc/vera-modelos`. **[N]** que la sintaxis de `systemd-run` funcione en la VM.

### Paso 7. Encender `modelos-control` y aplicar (C11, C12)

Primero el servicio, después la API: así la API tiene a quién llamar (aunque el pod esté apagado).

```bash
sudo systemctl enable --now vera-modelos-control.service
sudo systemctl status vera-modelos-control.service --no-pager
sudo ss -ltn            # nuevos: 127.0.0.1:8790 y GATEWAY:{11434,8766,8767}, nada en 0.0.0.0
sudo bash /opt/vera-modelos/vm/red-api.sh aplicar --seco
sudo bash /opt/vera-modelos/vm/red-api.sh aplicar         # recrea la API: unos segundos de 502
docker exec centinela-api wget -S -qO- "http://GATEWAY:8766/healthz"   # con el pod apagado: HTTP 503
sudo systemctl enable --now vera-modelos-guardian.timer
```

El `wget` se ejecuta dentro de `centinela-api` porque su imagen ya lo trae (se instala para el healthcheck). Sustituye `GATEWAY` por la IP del paso 4.

### Rollback completo, en este orden

```bash
sudo systemctl disable --now vera-modelos-guardian.timer vera-modelos-control.service
sudo bash /opt/vera-modelos/vm/red-api.sh desinstalar     # quita el override y recrea la API en simulado
sudo rm -f /etc/systemd/system/vera-modelos-control.service /etc/systemd/system/vera-modelos-guardian.service \
  /etc/systemd/system/vera-modelos-guardian.timer
sudo systemctl daemon-reload
# Si hubo un pod: terminarlo en la consola de RunPod y confirmar que desapareció (el guardián ya no corre).
# Opcional, si no se va a reintentar: borrar secretos y usuario.
sudo rm -rf /etc/vera-modelos /opt/centinela/api/modelos.env /var/lib/vera-modelos
sudo userdel vera-modelos
```

Cada paso es reversible por separado. Los pasos 1 a 6 no cambian el comportamiento de producción: solo el paso 7 (`aplicar`) lo hace.

## 5. Cómo llega `modelos-control` a la VM

Hoy el CD construye una imagen con `api`, `migrate` y `cargar-catalogo` (`Dockerfile`) y la despliega como contenedor por SSH con comando forzado (`cd.yml` y
`infra/servidor/deploy-ci.sh`) [V]. `modelos-control` es un binario del **host**, no del contenedor, y no estaba en ninguno de los dos: se agregó a `COMANDOS` del `Makefile`
(`make build` lo compila, estático, con `CGO_ENABLED=0`) [V, ejecutado]. Los workflows no se modificaron.

| # | Camino | Qué exige | Estado |
|---|--------|-----------|--------|
| 1 | **Compilar en tu máquina y `scp`** (paso 1) | `go` local (go1.27.4 [V]) y acceso SSH a la VM | **El más simple y el recomendado para la primera instalación.** Compila hoy sin cambios en CI. Verifica con `sha256sum` a los dos lados |
| 2 | Compilar en la VM con un contenedor efímero de Go | La VM descarga la imagen `golang:1.27-alpine` y los módulos, y baja el código por commit de `codeload.github.com` (repositorio público [V]) | [N] No se ejecutó: la imagen de Go no está en esta máquina y esta pasada no usa red externa |
| 3 | Extraer el binario de la imagen que el CD ya publica (borrador) | Cambiar el `Dockerfile` | Borrador de abajo, no aplicado |
| 4 | Publicarlo como artefacto del CD (borrador) | Un job nuevo en `cd.yml` | Borrador de abajo, no aplicado |

Camino 2, solo como referencia (no ejecutado):

```bash
SHA=<COMMIT_DE_40_CARACTERES>
mkdir -p /tmp/vera-src && curl -fsSL "https://codeload.github.com/SantiagoVillamilC/Backend_Vera/tar/${SHA}" | tar -xz -C /tmp/vera-src --strip-components=1
docker run --rm -v /tmp/vera-src:/src -w /src -e CGO_ENABLED=0 golang:1.27-alpine go build -trimpath -o /src/modelos-control ./cmd/modelos-control
sudo install -m 0755 -o root -g root /tmp/vera-src/modelos-control /usr/local/bin/modelos-control
```

**Borrador del camino 3** (la imagen de la API pasa a llevar también el binario, y el CD ya la publica en GHCR). En `Dockerfile`:

```dockerfile
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /app/bin/modelos-control ./cmd/modelos-control
...
COPY --from=build /app/bin/modelos-control ./modelos-control
```

En la VM: `docker create --name vera-extraer ghcr.io/santiagovillamilc/backend_vera:main`, luego `docker cp vera-extraer:/app/modelos-control /tmp/modelos-control` y `docker rm vera-extraer`.
Contra: la imagen de la API pasa a incluir un binario que puede crear pods (no lleva la clave de RunPod, pero es código que la API no necesita).

**Borrador del camino 4** (job nuevo en `.github/workflows/cd.yml`, con acciones fijadas por SHA como el resto del archivo, los SHA de abajo se completan al aplicarlo):

```yaml
  binario-modelos-control:
    name: Build modelos-control
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@<SHA_DE_SETUP_GO>
        with:
          go-version-file: go.mod
      - run: CGO_ENABLED=0 go build -trimpath -o modelos-control ./cmd/modelos-control && sha256sum modelos-control > modelos-control.sha256
      - uses: actions/upload-artifact@<SHA_DE_UPLOAD_ARTIFACT>
        with:
          name: modelos-control
          path: |
            modelos-control
            modelos-control.sha256
```

Actualizar el binario es una decisión de operación aparte: reiniciar `vera-modelos-control` con un pod encendido lo deja sin túnel hasta que `Recuperar` lo retome.

## 6. Llaves y tokens sin pasar por el chat ni por el repositorio

- **Tokens** (control, voz, STT): los genera `generar-secretos.sh` con `openssl rand -hex 32` dentro de la VM, los escribe con `umask 077` y `mv` atómico, y **nunca los
  imprime**. Voz y STT están en dos archivos 0600: `/etc/vera-modelos/tokens.env` (dueño `vera-modelos`, para `modelos-control`) y `/opt/centinela/api/modelos.env` (dueño root, para
  el contenedor de la API, que lo recibe por `env_file` del override). El token de control está en `/etc/vera-modelos/control.token`. La clave de RunPod vive solo en
  `modelos-control.env` (root): **no entra** al contenedor de la API. Son largos y **estáticos**: no cambian por arranque. Los dos archivos de voz y STT coinciden tras generar y
  tras rotar [V, `vm/generar-secretos_test.sh`].
- **Variables de la API en producción** (todas llegan por el override, nada se escribe a mano): `VERA_PROVEEDOR=local`, `VERA_MODELO=gemma4-e4b-local`, `VERA_VOZ_URL`,
  `VERA_STT_URL`, `VERA_OLLAMA_URL` con el gateway, `VERA_MODELOS_HOST_PERMITIDO` con el mismo gateway, y `VERA_VOZ_TOKEN` y `VERA_STT_TOKEN` desde `/opt/centinela/api/modelos.env`.
  `.env.example` las documenta comentadas, con el marcador `<GATEWAY_DE_DOCKER>`.
- **Clave de RunPod:** se escribe en la terminal de la VM al ejecutar el script (entrada sin eco). El script también la lee de stdin cuando no hay terminal: puedes enviarla por un pipe
  desde tu gestor local de contraseñas hacia `ssh -p "$PUERTO" centinela@"$IP" 'sudo bash /opt/vera-modelos/vm/generar-secretos.sh ...'` sin dejarla en el historial ni en argv.
  Comprueba primero que ese gestor esté instalado en tu máquina. Este trabajo no leyó ningún secreto.
- **Llave SSH (VM hacia pod):** `ssh-keygen -t ed25519 -N ''` en la VM, privada en `/etc/vera-modelos/ssh/` con dueño `vera-modelos` y modo 0600 (OpenSSH la rechaza si otros
  usuarios pueden leerla), pública 0644. `modelos-control` la entrega al pod al crearlo como `PUBLIC_KEY` [V en su `LeerLlavePublica`, **[N]** que la plantilla de RunPod la
  aplique]. No hay llave de CI hacia la VM: no se toca `authorized_keys` ni `deploy-ci.sh`.
- **Rotación:** `sudo bash generar-secretos.sh --rotar` genera tokens nuevos, conserva la clave de RunPod y los valores no secretos, y avisa que hay que reiniciar
  `vera-modelos-control` y recrear la API (`red-api.sh aplicar`). La llave SSH no se rota con ese comando: borra el par y ejecuta el script de nuevo.
- **Qué hacer ante una fuga:** terminar cualquier pod en la consola de RunPod, rotar la clave en RunPod y volver a ejecutar el script con `--clave-runpod`, y rotar los tokens con `--rotar`.
- **Hacia el pod:** `modelos-control` envía los tokens por stdin de `bash -s`. `arrancar-modelos.sh` los valida (32 caracteres o más, solo `A-Za-z0-9_-`), los quita de su
  entorno y los guarda en un archivo 0600 en el pod. Las sondas los envían con `curl -K -` (stdin), por lo que **no aparecen en `ps`** [V, la prueba usa un `curl` falso y comprueba que
  el token no está en argv].

## 7. Verificaciones hechas en esta integración

Todas en esta máquina, con secretos de prueba generados en un directorio temporal, sin RunPod, sin Azure y sin la VM [V]:

| Verificación | Resultado |
|--------------|-----------|
| `go build ./...`, `go vet ./...` | sin salida (sin errores) |
| `go test -race -count=1 ./cmd/modelos-control/... ./internal/modelos/... ./internal/copilot/... ./cmd/api/...` | todos los paquetes `ok` |
| `go test ./...` completo (sin la etiqueta `integration`) | 52 paquetes `ok`, 0 `FAIL`, 1 prueba omitida (`TestGeminiManual`, pide credenciales). Las pruebas con etiqueta `integration` (`internal/nucleo`, `cmd/migrate`, `cmd/semilla-escenario`) necesitan Postgres y **no se ejecutaron** |
| `shellcheck -x infra/modelos-runpod/pod/*.sh infra/modelos-runpod/vm/*.sh infra/voz-local/iniciar.sh` | 0 hallazgos |
| `bash infra/modelos-runpod/pod/modelos-pod_test.sh` | 40 pruebas, 0 fallas (incluye el stdin literal de M1) |
| `bash infra/modelos-runpod/vm/red-api_test.sh` | 51 pruebas, 0 fallas |
| `bash infra/modelos-runpod/vm/generar-secretos_test.sh` | 32 pruebas, 0 fallas |
| `modelos-control --seco` con el entorno que genera `generar-secretos.sh` + `red-api.sh instalar` contra una red docker temporal | configuración efectiva con las tres direcciones en el gateway, sin secretos |
| `modelos-control --seco` con el marcador `<GATEWAY_DE_DOCKER>` sin sustituir | sale con 2 y nombra las tres variables |
| `systemd-analyze verify` de las tres unidades | solo se queja de que el binario no existe |
| `docker compose config` con `compose.api.yaml` de prueba y el override generado | válido, las seis variables de la API presentes |
| Binario real `servir` ligado al gateway de una red docker temporal, RunPod falso en loopback | solo `GATEWAY:{11434,8766,8767}` y `127.0.0.1:8790` escuchan, el control rechaza sin token (401) |
| Contenedor `busybox` de esa red hacia `GATEWAY:{8766,8767,11434}` con el pod apagado | `HTTP/1.1 503 Service Unavailable` en los tres, y nada responde en `127.0.0.1:8766` del host |
| `cmd/modelos-control/integracion_clientes_test.go` | `vozlocal` y `sttlocal` con `VERA_MODELOS_HOST_PERMITIDO` apuntando a una IPv4 privada real de la máquina: rechazan la IP sin autorización, reciben 503 con el pod apagado, y con un pod falso encendido (RunPod, ssh y modelos falsos) sintetizan y transcriben. Tras apagar vuelve el 503 |

Qué no cubren: el túnel SSH real (el ssh falso no reenvía puertos, los modelos falsos ocupan los extremos locales), RunPod, el pod, `docker compose up` y nginx en la VM, el
firewall y el orden de arranque de la VM.

## 8. Verificaciones seguras que puedes repetir

```bash
shellcheck -x infra/modelos-runpod/pod/*.sh infra/modelos-runpod/vm/*.sh
bash infra/modelos-runpod/pod/modelos-pod_test.sh
bash infra/modelos-runpod/vm/red-api_test.sh
bash infra/modelos-runpod/vm/generar-secretos_test.sh
bash infra/modelos-runpod/pod/arrancar-modelos.sh --seco                         # plan y variables efectivas
go test -race -count=1 ./cmd/modelos-control/... ./internal/modelos/... ./internal/copilot/... ./cmd/api/...
sudo bash /opt/vera-modelos/vm/generar-secretos.sh --seco                        # en la VM
sudo bash /opt/vera-modelos/vm/red-api.sh instalar --seco                        # en la VM
sudo systemd-analyze verify /etc/systemd/system/vera-modelos-*.service           # en la VM
```

`arrancar-modelos.sh --compuerta` solo se ejecuta dentro de un pod. Qué cubren las pruebas de bash [V]: la compuerta de CUDA (12.4 aborta con 14, 13.0 pasa, el formato nuevo
`CUDA UMD Version` pasa, sin `nvidia-smi` aborta con 14), la validación de tokens por archivo y por entorno, el bloqueo entre corridas, la reutilización cuando los tres ya
están sanos, el informe de salud (servidores HTTP falsos con tokens), que el token nunca viaja en argv, la generación y rotación de secretos con modos 0600, y la lectura del
gateway, la escritura del entorno y del override y la detección de un gateway distinto en `red-api.sh`.

## 9. Primera prueba real controlada

**Antes de encender**
- Cuenta de RunPod con **saldo prepagado bajo** (5 USD según el diseño). Es el tope duro: si todo lo demás falla, el pod muere al llegar a 0 USD. Con 0,27 USD/h son unas 18 horas, así que no
  sustituye al guardián.
- Para la primera vez, baja los límites en `/etc/vera-modelos/modelos-control.env` y reinicia el servicio: `MODELOS_SESION_MAXIMA=1h` y `MODELOS_INACTIVIDAD=5m`. Después los devuelves a `4h` y `10m`.
- Confirma que `modelos-control` usa la API v2 de RunPod (la v1 se retiró hoy): `MODELOS_RUNPOD_URL` no debe estar definido o debe apuntar a `https://api.runpod.io/v2`.
- Confirma `red-api.sh verificar` en `RESULTADO=OK` y que `MODELOS_REPO_SHA` es el commit real: con ceros el pod se crea, se cobra y el script sale con 2.
- Ten abierta la consola de RunPod para ver el pod y poder terminarlo a mano.

**Costo y límites** (tarifa de la A5000 Secure: 0,27 USD/h [V en el diseño aprobado, https://www.runpod.io/pricing del 12 de noviembre])

| Escenario | Costo |
|-----------|-------|
| Ensayo de 15 minutos | unos 0,07 USD |
| Primera prueba con el tope de 1 hora | 0,27 USD como máximo |
| Una sesión de 4 horas (la normal) | 1,08 USD |
| Techo por sesión con el tope de 0,35 USD/h | 1,40 USD en 4 horas |

El disco del pod (sin volumen de red) se factura solo mientras el pod existe. El 0,27 es la tarifa de la GPU, **[N]** si el disco del contenedor (40 GB por defecto) suma algo con esta imagen.

**Procedimiento**
1. `sudo systemctl status vera-modelos-control.service` activo y sin reinicios.
2. Enciende desde el puerto de control, con el token que está en `/etc/vera-modelos/control.token` (no lo pegues en comandos que queden en el historial: léelo dentro de la VM):
   `sudo -u vera-modelos sh -c 'curl -s -X POST -H "Authorization: Bearer $(cat /etc/vera-modelos/control.token)" http://127.0.0.1:8790/encender'`
   (el token se expande dentro de la VM y aparece en `ps` un instante: aceptable para una prueba, evítalo en uso normal). `GET /estado` con la misma cabecera muestra la fase.
3. Observa: `sudo journalctl -u vera-modelos-control -f`. En el pod, `tail -n 1 /workspace/vera-estado/progreso.jsonl`. Tiempos esperados: en la campaña de la tesis el bootstrap sin volumen
   tardó **98 s en una 4090**, con 43 s de descarga del GGUF, y la primera carga del modelo unos 50 s [V en el diseño aprobado, solo 4090]. Para la A5000 y el aprovisionamiento del pod no
   hay medida [N]: espera de 3 a 8 minutos hasta `listo` [I].
4. Con `listo`, comprueba el camino completo desde la API: `docker exec centinela-api wget -qO- http://GATEWAY:11434/api/version` (pasa por `modelos-control`, el túnel y el pod). Después prueba un
   turno de texto, una transcripción y una síntesis desde la app o con los scripts de `docs/e2e-voz-local-2026-11-13.md`.
5. Mide y anota: tiempo hasta `listo`, tiempo del primer turno, VRAM (`nvidia-smi` en el pod) y que `size_vram == size`.
6. Apaga con `POST /apagar`. Confirma en la consola de RunPod que el pod **ya no existe** y que `systemctl list-timers 'vera-modelos-*'` sigue activo.
7. Deja otro pod encendido sin tocarlo para comprobar el apagado automático (5 minutos si bajaste el límite) y que el guardián lo termina.
8. Devuelve `MODELOS_SESION_MAXIMA=4h` y `MODELOS_INACTIVIDAD=10m`.

**Qué mirar si algo falla:** código 14 o 15 (otro host), 13 (VRAM), 2 (configuración de tokens o SHA). Si falló un servicio, el campo `detalle` de la línea `fallo` de `progreso.jsonl`
trae las últimas 20 líneas de su log, y el error del `ssh` en el registro de `modelos-control` trae las últimas 20 del log remoto. Los servidores no registran sus tokens, pero revisa ese texto
antes de pegarlo en un chat.

## 10. Qué cambia en producción y cómo se comporta la API sin pod

| Variable | Valor con esta instalación | Antes |
|----------|----------------------------|-------|
| `VERA_PROVEEDOR` | `local` | `simulado` (`banderas.tsv`, columna produccion) |
| `VERA_MODELO` | `gemma4-e4b-local` | vacío |
| `VERA_MODELOS_HOST_PERMITIDO` | el gateway de la red `centinela` | sin definir |
| `VERA_OLLAMA_URL` | `http://GATEWAY:11434` | por defecto `http://127.0.0.1:11434` |
| `VERA_STT_URL` y `VERA_STT_TOKEN` | `http://GATEWAY:8767` y el token de `modelos.env` | sin definir |
| `VERA_VOZ_URL` y `VERA_VOZ_TOKEN` | `http://GATEWAY:8766` y el token de `modelos.env` | URL por defecto, sin token |

Se aplican con el override `compose.z-modelos.yaml`, **no** editando `banderas.tsv`: cambiar `VERA_PROVEEDOR` de producción allí exige tocar a la vez `banderas.tsv`, `.env.produccion.example`
y `config_banderas_test.go` o la prueba falla [V en el diseño aprobado]. Efectos:

- `make verificar-banderas` y `infra/servidor/verificar-banderas.sh produccion` reportarán `DIFF` en `VERA_PROVEEDOR`. Es esperado: el contenedor difiere de la matriz a propósito. Si prefieres
  una matriz honesta, es otro PR que cambia esos tres archivos a la vez.
- Esta política es la (a) del diseño: **`local` permanente**.
- `VERA_MODELOS_HOST_PERMITIDO` no pertenece a la matriz de banderas (`docs/configuracion-entornos.md`). No se agregó a `banderas.tsv`.

**Con el pod apagado** (comportamiento del código actual, diseño aprobado [V], `internal/platform/httpserver`):
- La API **arranca bien**: con `VERA_PROVEEDOR=local` no se sondea Ollama al arrancar. `VERA_STT_URL` y `VERA_STT_TOKEN` están ambas definidas, y `VERA_VOZ_TOKEN` con una URL aceptada no bloquea el arranque.
- Un turno de **texto** responde **503** (`ErrNoDisponibleIA`). No hay caída automática a `simulado`.
- La **transcripción** falla.
- La **síntesis** responde 200 degradado con la cabecera `X-Vera-Audio: no-disponible`.
- Con el pod apagado, el proxy de `modelos-control` responde `503` con `{"error":"modelos_apagados"}` y `Retry-After`, sin retener la conexión [V, ejecutado en la sección 7].
  Con `modelos-control` caído, la conexión al gateway se rechaza y el resultado para la API es el mismo.
- `/readyz` de la API no cambia (el CD y los despliegues siguen su camino normal). `GET /readyz/modelos` reportará los tres como caídos (hace `GET /` sobre cada URL del override, que el proxy responde 503 con el pod apagado). Esas sondas no cuentan como actividad, así que un monitor
que consulte esa ruta no impide el apagado por inactividad [V, `proxy.EsSonda`].

Si el producto no tolera esos 503, la salida es de producto, no de infraestructura: un proveedor compuesto en Go que caiga a `simulado` (decisión 10 del diseño aprobado, no se hace aquí).

**Regla operativa:** un despliegue del CD recrea `centinela-api` y conserva el override, porque `deploy-ci.sh` y `60-api.sh` unen todos los `compose*.yaml` del directorio. Si el glob no ordenara
`compose.z-modelos.yaml` al final, el override no ganaría sobre `compose.api.yaml` **[N]**: `red-api.sh verificar` y el `wget` del paso 7 lo delatan.

## 11. Riesgos conocidos

1. **La IP del gateway puede cambiar** si la red `centinela` se vuelve a crear (`docker compose down` en el directorio de compose). La red no fija su subred (`30-nginx.sh`, `networks: centinela:
   name: centinela`) [V]. Entonces `modelos-control` no puede ligar su IP (`cannot assign requested address`, sale con código 1 [V, ejecutado]), y la API quedaría con la IP vieja. Qué hacer:
   `red-api.sh instalar`, reiniciar `vera-modelos-control` y `red-api.sh aplicar`. `red-api.sh verificar` detecta el desfase. Mejora de fondo, no aplicada: fijar la subred de `centinela` con `ipam`
   en `30-nginx.sh`, lo que exige recrear la red.
2. **Reinicios del servicio.** Si docker aún no levantó la red al arrancar la VM, el bind falla y systemd reintenta cada 10 s sin tope (`StartLimitIntervalSec=0`). Con el límite por defecto de la
   unidad original (5 arranques en 300 s con pausa de 5 s) el servicio quedaría en `failed` a los 25 s. Un error de configuración (código 2 o 64) no se reintenta (`RestartPreventExitStatus=2 64`). El
   orden de arranque real de la VM no se probó **[N]**.
3. **Los puertos del gateway no tienen autenticación de red.** Los alcanza cualquier contenedor de la VM (Postgres, Redis, simuladores, nginx y los de otras redes bridge) [I, el contenedor de una red
   temporal alcanzó su gateway en esta máquina]. Voz y STT exigen su `X-Vera-Token` en el pod, pero **Ollama no lleva token**, y el proxy reenvía cualquier ruta de Ollama (también `/api/pull` y `/api/delete`)
   al pod mientras está encendido. Aceptable en una VM de un solo propósito [I]. Mitigación posible, no hecha: reglas en `DOCKER-USER`.
4. **`MemoryDenyWriteExecute`, `SystemCallFilter=@system-service` y `RestrictAddressFamilies` sin `AF_NETLINK` pueden romper el binario o los `ssh` y `scp` que lanza.** Si el servicio no arranca,
   se relajan de a uno y se documenta cuál fue **[N]**. El sandbox se verificó con `systemd-analyze`, no con el servicio real.
5. **Clave de RunPod con acceso total a la cuenta** (no hay claves restringidas para pods, diseño aprobado [V]). Mitigación: cuenta con prepago bajo. Está en el entorno del proceso `modelos-control`, legible
   solo por root y por el usuario `vera-modelos`.
6. **Confianza en el primer uso del host SSH del pod.** M1 guarda un `known_hosts` por pod con `accept-new` y trata una huella distinta como error fatal [V]. La primera conexión sigue siendo de
   confianza: un atacante en la ruta en esos segundos podría suplantar al pod. Riesgo aceptado en la demo (decisión 8 del diseño).
7. **Cada arranque descarga todo** (sin volumen de red): GGUF de unos 5 GB desde Hugging Face, Ollama desde `ollama.com` o GitHub, wheels desde PyPI, pesos de Kokoro desde GitHub. El GGUF y los pesos de
   Kokoro se verifican con sha256. Los pesos de Whisper los baja `huggingface_hub` con sus propias comprobaciones, **no con un sha256 nuestro**. El tarball del código queda fijado por el commit,
   sin hash propio del archivo. El instalador de Ollama (`install.sh`) se ejecuta como root, igual que en la tesis.
8. **Python del pod.** La imagen por defecto es Ubuntu 24.04, que trae Python 3.12 (los pines de `requirements*.txt` se verificaron contra wheels cp312) [I]. El script prefiere `python3.12`, luego
   `3.11`, luego `python3`.
9. **`OLLAMA_KEEP_ALIVE=24h` del pod no manda**: cada petición del backend lleva `keep_alive: 30m` (`internal/copilot/ollama/proveedor.go`) [V] y lo sobrescribe. Con el apagado a los 10 minutos de
   inactividad no importa.
10. **Una sola GPU compartida.** Whisper, Kokoro y Ollama comparten los 24 GB. Whisper y Kokoro reservan su VRAM antes de que arranque Ollama. Con `OLLAMA_NUM_PARALLEL` alto la VRAM puede no alcanzar (código 13).
11. **Endurecimiento de `VERA_OLLAMA_URL`.** Desde `feat/modelos-host-privado`, Ollama solo se acepta en `127.0.0.1` o en una IP de la lista, y los hostnames, `https` y las IP públicas dejan de arrancar
    (`docs/configuracion-entornos.md`). Quien use hoy otra URL en cualquier entorno debe revisarlo antes de promover.

## 12. Qué no se verificó

- **Nada se ejecutó contra RunPod, Azure, GitHub (salvo una lectura de visibilidad del repositorio) ni la VM.** Solo lecturas locales y experimentos con docker local, que se limpiaron.
- **El gateway real de la VM** (`docker network inspect centinela`): el valor `172.x` de los ejemplos es de una red temporal de esta máquina.
- **`arrancar-modelos.sh` completo en un pod:** instalación de Ollama 0.40.10, descarga del GGUF con `-C -` y reanudación, venvs con `--only-binary=:all:`, `descargar.py` con GPU, arranque de Whisper con
  `WHISPER_GPU=si` (la campaña lo hizo en 4090, no en A5000), Kokoro en CUDA, `ollama create` con `FROM ./archivo` y `size_vram == size`.
- **Que `--wildcards` y `--strip-components=2` de `tar` extraigan bien el tarball de `codeload.github.com`** con ese commit (la lógica es correcta sobre el papel, no se corrió contra un tarball real).
- **Que existan `pgrep`, `pkill`, `flock`, `zstd` y `curl` en la imagen del pod**, que `/workspace` exista y sea escribible, y que `runpod/pytorch:1.0.2-cu1281-torch280-ubuntu2404` siga existiendo.
- **Que `openssl`, `ssh`, `scp` y `ssh-keygen` estén en la VM**, la versión de `docker compose` de la VM y que el glob `compose*.yaml` ordene `z-modelos` al final.
- **Que un contenedor de la red `centinela` alcance el gateway en la VM de Azure** (en esta máquina sí, sin reglas de firewall extra) y que `ss -ltn` no muestre nada en `0.0.0.0`.
- **Que `systemd-analyze verify` sea limpio con el binario presente**, y que el sandbox no lo rompa en Debian 13.
- **El túnel `ssh -L` real** sobre el TCP directo del pod y que `PUBLIC_KEY` entregue la llave pública en la imagen de RunPod.
- **El camino 2 de la sección 5** (compilar en la VM con la imagen de Go).
- **El cobro exacto del disco del contenedor** y la disponibilidad de stock Secure para la A5000.
- **Un `/readyz` unificado en el pod:** no se hizo a propósito. Exigiría un cuarto proceso y un cuarto puerto en el túnel. `salud-modelos.sh --json` y las sondas de `modelos-control` cubren lo mismo.

## 13. Archivos de esta pieza

| Archivo | Para qué |
|---------|----------|
| `cmd/modelos-control/`, `internal/modelos/` | El binario (M1): máquina de estados, cliente de RunPod, ssh y túnel, proxys y API de control |
| `internal/copilot/destinolocal/` y los clientes `vozlocal`, `sttlocal` | Aceptan una IPv4 privada exacta autorizada por `VERA_MODELOS_HOST_PERMITIDO` (M4) |
| `infra/modelos-runpod/pod/arrancar-modelos.sh` | Dentro del pod: prepara y arranca los tres modelos. Idempotente |
| `infra/modelos-runpod/pod/salud-modelos.sh` | Dentro del pod: sonda los tres servicios con sus tokens |
| `infra/modelos-runpod/pod/modelos-pod_test.sh` | 40 pruebas de los dos anteriores, sin pod |
| `infra/modelos-runpod/vm/generar-secretos.sh` | En la VM: usuario, tokens, llave SSH y archivos 0600 |
| `infra/modelos-runpod/vm/red-api.sh` | En la VM: escribe el gateway en `modelos-control.env` y en el override, y recrea la API (gateway, instalar, aplicar, verificar, desinstalar) |
| `infra/modelos-runpod/vm/compose.z-modelos.yaml.plantilla` | Override de la API: `VERA_PROVEEDOR=local`, URL de los modelos y `VERA_MODELOS_HOST_PERMITIDO` |
| `infra/modelos-runpod/vm/*_test.sh` | Pruebas de los dos scripts de la VM |
| `infra/modelos-runpod/systemd/` | `vera-modelos-control.service`, `vera-modelos-guardian.service` y `.timer` |
| `infra/modelos-runpod/.env.example` | Todas las variables de `modelos-control`, con el marcador `<GATEWAY_DE_DOCKER>` y valores de ejemplo no secretos |
| `cmd/modelos-control/integracion_clientes_test.go` | Prueba de punta a punta entre los clientes de la API y `modelos-control` en una IP privada |

## 14. Orden de PRs

Una rama por PR, todos contra `dev`, y después `dev` a `staging` y `staging` a `main`:

1. `feat/kokoro-gpu` (Kokoro en GPU y descarga segura de pesos). Va primero: sin él no existe un commit válido para `MODELOS_REPO_SHA`.
2. `feat/modelos-host-privado` (`VERA_MODELOS_HOST_PERMITIDO` y los clientes).
3. `feat/modelos-control` (binario `cmd/modelos-control` e `internal/modelos`).
4. `integracion/modelos-runpod` (infra alineada con el binario, esta documentación, `Makefile` y la prueba de integración). **No abrir `feat/modelos-runpod-infra` tal cual**: trae los puentes nginx descartados.
   Cuando los PRs 1 a 3 ya estén en `dev`, el diff de este PR se reduce a la infra y la alineación.

## 15. Divergencias con el diseño aprobado y con el encargo

| Diseño aprobado o encargo | Aquí | Motivo |
|---------------------------|------|--------|
| Sidecar docker con el cliente `ssh` abre el túnel | Binario Go `modelos-control` como servicio del host abre el túnel | Decisión de Christian del 15 de noviembre |
| Puentes nginx `stream` entre la API y `modelos-control` en loopback | **Sin puentes.** `modelos-control` escucha en el gateway de la red docker y la API lo usa con `VERA_MODELOS_HOST_PERMITIDO` | Decisión de red aprobada (opción B). Quita dos contenedores, uno con red de host, y la fragilidad ante la recreación de la API |
| Variables `VERA_MODELOS_*` y `modelos-control serve` (encargo) | `MODELOS_*` y `servir`, como parsea el binario | El código de M1 es el contrato real |
| Tokens al pod como variables de entorno al crearlo | Por stdin de `bash -s` en cada arranque, y archivo 0600 en el pod | Es lo que implementa M1. El pod ya existe cuando se ejecuta el script. Sin secretos en la API de RunPod |
| Tokens en un archivo de root | Archivos de dueño `vera-modelos` (0600) para `modelos-control`, y uno de root para la API | M1 lee los tokens por ruta y los valida con su propio usuario |
| Kokoro en CPU | Kokoro fp32 en CUDA con compuerta de proveedor | Decisión de Christian. Exige CUDA 13.0 en el driver y la rama `feat/kokoro-gpu` |
| Política (b): conmutar a `simulado` entre ventanas | Política (a): `local` permanente, 503 con el pod apagado | Decisión de Christian ("producción siempre usa RunPod, nunca simulado") |
| Workflows de GitHub para encender y apagar (con cron cada 15 min) | Botón y temporizador systemd en la VM cada 5 min | Decisión de Christian. No hay secretos de RunPod en GitHub |
| Acceso a la VM por llave de CI con comando forzado | Sin llave nueva de CI: instalación manual por ssh con aprobación | Alcance de este encargo. No se toca `authorized_keys` |
| `arrancar-pod.sh` en `infra/modelos/runpod/` | `infra/modelos-runpod/pod/arrancar-modelos.sh` | Propiedad de archivos del encargo |
| Temporizador que repara el puente tras un despliegue | No existe: sin puente no hay nada que reparar. `red-api.sh verificar` cubre el desfase del gateway | Consecuencia de la opción B |

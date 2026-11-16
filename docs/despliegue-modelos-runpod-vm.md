# Modelos de Vera en RunPod: instalación en la VM de Azure

Fecha: 15 de noviembre de 2026. Estado: **código listo y probado sin pod, nada aplicado**. No se creó ningún pod, no se tocó
Azure, la VM, GitHub ni RunPod. Este documento es la lista de cambios que Christian aprueba antes de ejecutar nada.

Marcas: **[V]** verificado en esta sesión (con cómo), **[I]** inferido, **[N]** no verificado y por comprobar.

Complementa el diseño aprobado (`docs/despliegue-modelos-runpod.md` de la rama de despliegue, aún no está en `dev`). Lo que
cambia respecto de ese diseño está en la sección 12.

## 0. Cuatro cosas antes de leer el resto

**1. El binario del host no llega a la API por sí solo, y se resolvió con un puente.** La API corre en el contenedor
`centinela-api`, en la red docker `centinela` (`infra/servidor/pasos/60-api.sh`, `networks: - centinela`) [V]. Dentro de ese
contenedor `127.0.0.1` es su propio loopback. Los clientes de voz y de transcripción solo aceptan `http://127.0.0.1`
(`internal/copilot/vozlocal/voz.go:29`, `internal/copilot/sttlocal/cliente.go:28`) [V]. Un `modelos-control` que escuche en
el `127.0.0.1` de la VM es inalcanzable desde ahí.

**2. Además, `modelos-control` solo acepta escuchar en loopback.** Lo leí del árbol de trabajo de M1 (rama `feat/modelos-control`,
commit `aa483a7` más cambios sin confirmar): `internal/modelos/config/config.go`, función `direccion`, falla si la IP no es
loopback (`!ip.IsLoopback()`) [V]. Mi primer diseño (escuchar en la IP del gateway) no era posible. Solución que funciona **sin
cambiar a M1**, de dos saltos:

```
 centinela-api:127.0.0.1:P  ->  [puente-modelos, red de la API]  ->  GATEWAY:P
                            ->  [puente-host-modelos, red del host] -> 127.0.0.1:P  (modelos-control)
```

- `GATEWAY` es la IP del gateway de la red docker `centinela` (`puente-modelos.sh gateway`). Ningún proxy escucha en `0.0.0.0`.
- Ambos puentes son `nginx:1.27.5-alpine` con el módulo `stream` (la misma imagen que ya usa `30-nginx.sh`, `nginx -V` muestra
  `--with-stream` [V]), con usuario 101, `read_only`, `cap_drop: ALL` y `no-new-privileges`.
- Probado con docker local el 15 de noviembre [V]: un servidor ligado solo a `127.0.0.1` del host fue alcanzado desde el
  `127.0.0.1` de otro contenedor a través de los dos saltos. El mismo experimento mostró el riesgo: al recrear el contenedor de la
  API, el puente queda "running" pero en el espacio de red viejo y la conexión se rechaza sin ningún aviso. Por eso existe
  `puente-modelos.sh verificar|reparar` y un temporizador.
- Con `docker compose config` se comprobó que `env_file` se suma y `environment` se pisa clave por clave al combinar
  `compose.api.yaml` con el override [V].
- Si M1 aceptara escuchar en una IP privada concreta (por ejemplo la del gateway), `puente-modelos.sh` pasa solo al modo
  **directo** y no crea el salto en el host (probado únicamente con un docker simulado [V], no en una VM).

**3. Dependencias previas.** (a) La rama `feat/kokoro-gpu` (variante GPU de Kokoro: `requirements-cuda.txt`, el nuevo
`descargar.py` y `VERA_VOZ_DISPOSITIVO` en `servidor.py`) debe estar fusionada y el commit de `MODELOS_REPO_SHA` debe contenerla.
Hoy esa rama es local, va 6 commits por delante de `origin/dev` y no tiene PR [V]. `arrancar-modelos.sh` aborta con un mensaje
claro si falta. (b) `cmd/modelos-control` (M1) debe estar en `dev`. (c) La API REST v1 de RunPod se retira hoy, 15 de noviembre de
2026 (diseño aprobado, sección 0): M1 apunta por defecto a `https://api.runpod.io/v2` [V en su `config.go`].

**4. Qué se prueba sin gastar y qué no.** Hay 106 pruebas automáticas (sección 6) que no necesitan pod. La instalación real de
Ollama, pip, las descargas, el arranque de los tres servidores y la VRAM **solo se pueden verificar con un pod**.

## 1. Arquitectura

```
 Usuario (app Vera) -> HTTPS -> nginx -> contenedor centinela-api (red docker "centinela")
                                           VERA_PROVEEDOR=local, 127.0.0.1:{11434,8767,8766}
                                                |  (mismo espacio de red)
                          contenedor centinela-modelos-puente (nginx stream, usuario 101, read_only)
                                                |  TCP hacia GATEWAY (IP del bridge "centinela")
                          contenedor centinela-modelos-puente-host (nginx stream, red del host, usuario 101)
                                                |  TCP hacia 127.0.0.1 de la VM
 VM centinela-vm (Debian 13) ------------------ v -------------------------------------------
   vera-modelos-control.service  (usuario vera-modelos, sin shell)
      escucha 127.0.0.1:{11434,8766,8767}  (proxy HTTP, 503 rápido con el pod apagado)
      y 127.0.0.1:8790 (control: POST /encender, POST /apagar, GET /estado, con token Bearer)
      crea/apaga el pod por la API de RunPod, abre el túnel SSH (saliente) y reconcilia
   vera-modelos-guardian.timer   cada 5 min: modelos-control reconcile (pods huérfanos o vencidos)
   vera-modelos-puente.timer     cada 2 min: puente-modelos.sh reparar (puente huérfano tras un deploy)
                                                |  ssh -L, saliente, hacia IP pública y puerto del pod
 RunPod, pod "vera-modelos" (RTX A5000, Secure, imagen runpod/pytorch Ubuntu 24.04, sin Docker, sin volumen de red)
   arrancar-modelos.sh  ->  Whisper small GPU :8767 (X-Vera-Token), Kokoro fp32 CUDA :8766 (X-Vera-Token),
                            Ollama + gemma4-e4b-local :11434. Todo en 127.0.0.1 del pod.
```

Quién enciende y apaga: el botón (control de `modelos-control`), el apagado por inactividad de 10 minutos y el guardián.
Tope duro de gasto: el saldo prepagado bajo de la cuenta de RunPod.

## 2. Contrato con `modelos-control` (M1)

**Esto es lo que M1 implementa hoy**, leído de su árbol de trabajo (`/home/chris/Documents/Backend_Vera-modelos-control`, commit
`aa483a7` y cambios sin confirmar) [V]. Difiere del encargo original en los nombres: el binario lee `MODELOS_*` (sin `VERA_`), el
subcomando se llama `servir` (no `serve`) y no existe `estado` como subcomando. `.env.example` sigue lo que el binario parsea.
Si M1 cambia antes de fusionar, este es el único archivo de nombres que hay que reconciliar.

| Pieza | Valor real |
|-------|-----------|
| Uso | `modelos-control [--seco] [servir]`, `modelos-control reconcile` |
| Clave de RunPod | `RUNPOD_API_KEY` del entorno cuando `MODELOS_ARCHIVO_CLAVE_RUNPOD` está **vacío** (por defecto lee `/etc/vera/runpod.env`, por eso `.env.example` lo deja vacío) |
| Tokens de voz y STT | archivo `MODELOS_ARCHIVO_TOKENS` con `VERA_STT_TOKEN` y `VERA_VOZ_TOKEN`, modo 0600 y **legible por el usuario del servicio** |
| Token de control | archivo `MODELOS_ARCHIVO_TOKEN_CONTROL` (una línea), mismo requisito |
| Llave SSH | `MODELOS_LLAVE_SSH` y `MODELOS_LLAVE_PUBLICA_SSH` (esta última se inyecta en el pod como `PUBLIC_KEY`) |
| Pod | `MODELOS_GPUS` (coma), `MODELOS_IMAGEN`, `MODELOS_DISCO_GB`, `MODELOS_TARIFA_TOPE_USD_H`, `MODELOS_INACTIVIDAD` (`10m`), `MODELOS_SESION_MAXIMA` (`4h`) |
| Direcciones | `MODELOS_PUERTO_CONTROL`, `MODELOS_ESCUCHA_{OLLAMA,VOZ,STT}` y `MODELOS_INTERNO_*`: **solo loopback** |
| Script | `MODELOS_SCRIPT_ARRANQUE` (en la VM), `MODELOS_SCRIPT_REMOTO` (en el pod, `/tmp/vera-arranque.sh`), `MODELOS_REPO_SHA` |
| Esperas | pod 10 m, ssh 10 m, arranque 25 m, modelos 10 m (por defecto) |

Cómo habla `modelos-control` con el pod, y qué hace `arrancar-modelos.sh` con eso [V, leído en `control/encendido.go`]:

1. Copia el script por `scp` a `MODELOS_SCRIPT_REMOTO`.
2. Ejecuta `ssh ... bash -s` y por stdin envía `export WHISPER_TOKEN='...'`, `export KOKORO_TOKEN='...'`, `export REPO_SHA='...'` y
   `exec bash /tmp/vera-arranque.sh`. Los tokens no van en argv. Espera a que el script **termine** (hasta 25 minutos).
3. `arrancar-modelos.sh` acepta ese camino: toma los tokens del entorno (`WHISPER_TOKEN` y `VERA_LAB_TOKEN` o `KOKORO_TOKEN`), los quita
   de su propio entorno para que no los hereden `pip` ni `curl`, y los guarda en `/workspace/vera-estado/tokens.env` (0600) para que
   `salud-modelos.sh` pueda leerlos después. `REPO_SHA` equivale a `VERA_CODIGO_SHA`. Alternativa: archivo `VERA_TOKENS_ARCHIVO`.
   Los servidores leen `WHISPER_TOKEN` y `VERA_LAB_TOKEN` (no `KOKORO_TOKEN`) [V en `servidor.py`].
4. Los servicios quedan lanzados con `setsid nohup`, stdin nulo y salida a un log, sin heredar el bloqueo (`9>&-`), así el `ssh` vuelve
   cuando el script termina.
5. M1 no lee `progreso.jsonl` ni ejecuta `salud-modelos.sh`: sondea los modelos por el túnel con su propia sonda. Ambos archivos
   existen para depurar a mano y para una integración futura.

Formato de `progreso.jsonl` (en el pod, `/workspace/vera-estado/progreso.jsonl`, una línea por evento, se trunca al empezar cada corrida):

```json
{"epoch": 1763251200, "paso": "gguf", "estado": "en_curso", "codigo": 0, "detalle": ""}
```

- `estado`: `en_curso`, `ok`, `fallo` o `listo`. La **última línea siempre es `listo` o `fallo`** (una trampa `EXIT` lo garantiza).
- `paso`: `inicio`, `entorno`, `compuerta_cuda`, `tokens`, `salud`, `codigo`, `apt`, `ollama_instalar`, `gguf`, `venvs`, `pesos`, `detener`,
  `whisper`, `kokoro`, `ollama`, `listo`. Una línea sin salto final es una escritura en curso: ignorarla.
- Se toma un `flock` antes de truncar el archivo: una segunda corrida sale con 5 sin tocar el progreso de la primera.

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

Hoy M1 trata cualquier fallo del script como error de arranque, sin distinguir 14 y 15 [I, no vi la lógica de reintento]. Es una mejora
para pedirle: reintentar en otro host solo con 14 y 15.

**Discrepancias a reconciliar** (lo que M1 acepta frente a lo que el script necesita):
- `MODELOS_REPO_SHA` en M1 admite letras, dígitos, punto, guion y guion bajo. El script exige **40 caracteres hexadecimales** porque baja
  un tarball de `codeload.github.com` por commit. Con otro valor aborta con 2.
- `OLLAMA_NUM_PARALLEL` no se lo pasa M1 al script: queda el valor por defecto 2. Para cambiarlo hay que editar el script o que M1 exporte la variable.
- M1 sondea con `MODELOS_INTERNO_*` y reenvía por túnel a `puertoPod*` (11434, 8767, 8766). Coinciden con los puertos del script.

## 3. Lista de cambios que Christian aprueba antes de ejecutar

Todo es nuevo. **No se modifica ningún archivo existente de la VM.** El orden de la sección 4 es el de esta tabla.

| # | Qué | Dónde | Dueño y modo | Comando que lo crea |
|---|-----|-------|--------------|---------------------|
| C1 | Copia del repositorio de la pieza | `/opt/vera-modelos/` (`pod/`, `vm/`, `systemd/`, `.env.example`) | root:root 0755, scripts 0755 | `scp` y `sudo cp -rT` |
| C2 | Binario `modelos-control` (compilado de `cmd/modelos-control`) | `/usr/local/bin/modelos-control` | root:root 0755 | `sudo install` |
| C3 | Usuario de sistema `vera-modelos` | `/etc/passwd` | sin shell (`/usr/sbin/nologin`), sin home, **fuera del grupo docker** | `generar-secretos.sh` (`useradd --system`) |
| C4 | Configuración y clave de RunPod | `/etc/vera-modelos/modelos-control.env` | root:root **0600** | `generar-secretos.sh` |
| C5 | Tokens de voz y STT para `modelos-control`, y token de control | `/etc/vera-modelos/tokens.env` y `control.token` | vera-modelos **0600** | `generar-secretos.sh` |
| C6 | Tokens de la API (solo `VERA_STT_TOKEN` y `VERA_VOZ_TOKEN`, **sin** la clave de RunPod) | `/opt/centinela/api/modelos.env` | root:root **0600** | `generar-secretos.sh` |
| C7 | Llave SSH del túnel (VM hacia pod) | `/etc/vera-modelos/ssh/tunel_ed25519` y `.pub` | privada vera-modelos **0600**, pública 0644 | `generar-secretos.sh` (`ssh-keygen -t ed25519`) |
| C8 | Cinco unidades systemd | `/etc/systemd/system/vera-modelos-{control,guardian,puente}.*` | root:root 0644 | `sudo install` |
| C9 | Carpeta de estado | `/var/lib/vera-modelos` | vera-modelos 0700 (la crea systemd) | `StateDirectory=` |
| C10 | Override de compose y configuraciones de los puentes | `/opt/centinela/infra-servidor/compose.z-modelos.yaml` y `modelos-puente/*.conf` | root:root 0644 | `puente-modelos.sh instalar` |
| C11 | Contenedores `centinela-modelos-puente` y `centinela-modelos-puente-host`, y **recreación de `centinela-api`** con `VERA_PROVEEDOR=local` | docker | n/a | `puente-modelos.sh aplicar` |
| C12 | Activar `vera-modelos-control.service` y los dos temporizadores | systemd | n/a | `systemctl enable --now` |

- **Firewall y red de Azure: ninguna regla nueva.** El túnel SSH hacia el pod y las llamadas a la API de RunPod son **salientes**.
  Los sockets nuevos son: `127.0.0.1:{11434,8766,8767,8790}` (`modelos-control`) y `GATEWAY:{11434,8766,8767}` (el salto en el host,
  IP del bridge docker, solo alcanzable desde la VM y sus contenedores). Nada escucha en `0.0.0.0`. La NSG no se toca. El repositorio
  no configura firewall de host (solo la NSG de Azure) [V: sin resultados de `ufw|nftables|iptables|firewall` en `infra/servidor`],
  así que el tráfico contenedor a gateway no está bloqueado [I].
- **Tiempo de indisponibilidad:** C11 recrea la API: unos segundos de 502 en nginx. Hacerlo fuera de horario de uso.
- **Lo que NO se hace:** no se edita `banderas.tsv`, `.env.*.example`, `compose.api.yaml`, `deploy-ci.sh` ni `authorized_keys`.
  No se crean usuarios en el grupo `docker`. No se abre ningún puerto.
- **Un contenedor con `network_mode: host`** (`centinela-modelos-puente-host`) es lo más delicado de esta lista. Corre sin
  privilegios (usuario 101, `cap_drop: ALL`, `read_only`) y solo liga la IP del gateway. Si prefieres no tenerlo, la alternativa es que
  M1 acepte escuchar en esa IP (sección 0, punto 2).

## 4. Procedimiento exacto

Prerrequisitos en tu máquina: el PR de esta rama y el de `modelos-control` ya fusionados en `dev`, y `go` instalado
(`command -v go`: go1.27.4 [V en esta máquina]). La VM es `centinela-vm`, Debian 13, en `centinela-rg` (diseño aprobado [V]).

### Paso 0. Conectarte (solo lectura de Azure)

```bash
IP="$(az vm show -d -g centinela-rg -n centinela-vm --query publicIps -o tsv)"
PUERTO="$(bash -c '. infra/servidor/red-fijada.sh && printf %s "$PUERTO_SSH"')"   # 22022 por defecto
ssh -t -p "$PUERTO" centinela@"$IP"
```

Usa `ssh -t` (terminal real) en los pasos que piden la clave de RunPod. **No uses `az vm run-command` para el paso 4**: el texto del
script queda guardado en Azure y la clave no debe pasar por ahí. Sí sirve para pasos sin secretos.

### Paso 1. Copiar los archivos (C1 y C2)

```bash
# En tu máquina, desde un checkout de dev:
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /tmp/modelos-control ./cmd/modelos-control
scp -P "$PUERTO" -r infra/modelos-runpod centinela@"$IP":/tmp/vera-modelos
scp -P "$PUERTO" /tmp/modelos-control centinela@"$IP":/tmp/modelos-control
# En la VM:
sudo install -d -m 0755 -o root -g root /opt/vera-modelos
sudo cp -rT /tmp/vera-modelos /opt/vera-modelos
sudo chown -R root:root /opt/vera-modelos
sudo chmod 0755 /opt/vera-modelos/pod/*.sh /opt/vera-modelos/vm/*.sh
sudo install -m 0755 -o root -g root /tmp/modelos-control /usr/local/bin/modelos-control
```

`scp` y `cp -rT` son el mismo patrón de `infra/servidor/README.md`: la imagen de Azure no trae `rsync` [V en ese README].
Rollback: `sudo rm -rf /opt/vera-modelos /usr/local/bin/modelos-control` (en la VM, ruta fija, sin variables).

### Paso 2. Comprobar prerrequisitos en la VM (solo lectura)

```bash
command -v openssl ssh ssh-keygen scp docker systemd-analyze
docker compose version
docker network inspect centinela --format '{{(index .IPAM.Config 0).Gateway}}'
sudo ss -ltn
```

`openssl`, `ssh`, `scp` y `ssh-keygen` en la VM son **[N]**: `modelos-control` invoca `/usr/bin/ssh` y `/usr/bin/scp`, y
`generar-secretos.sh` necesita `openssl` y `ssh-keygen`. Si falta `openssl` o `ssh-keygen`, el script aborta con código 69 antes de
cambiar nada. Si falta `ssh` o `scp`, instala `openssh-client` con la ventana de mantenimiento acordada.

### Paso 3. Ver la IP del gateway (solo lectura)

```bash
sudo bash /opt/vera-modelos/vm/puente-modelos.sh gateway
```

Es la IP donde escuchará el salto del host. La red `centinela` no fija su subred (`30-nginx.sh`, `networks: centinela: name:
centinela`) [V], así que puede cambiar si alguien ejecuta `docker compose down` y vuelve a crearla. `puente-modelos.sh instalar`
la vuelve a leer en cada ejecución y `verificar` delata un puente que quedó con la anterior.

### Paso 4. Secretos (C3 a C7): sin que pasen por el chat ni por el repositorio

```bash
sudo bash /opt/vera-modelos/vm/generar-secretos.sh --seco --codigo-sha <COMMIT_DE_40_CARACTERES>
sudo bash /opt/vera-modelos/vm/generar-secretos.sh --codigo-sha <COMMIT_DE_40_CARACTERES>
```

La primera línea es el ensayo: imprime lo que haría y no cambia nada. La segunda pide la clave de RunPod **en la terminal, sin
eco** (`read -rs`): se pega ahí, nunca en el chat, nunca como argumento. Los tres tokens se generan con `openssl rand -hex 32` y no
se imprimen. Se imprime solo la parte pública de la llave SSH, que no es secreta. Ver la sección 5.

### Paso 5. Instalar las unidades y comprobarlas (C8, C9)

```bash
sudo install -m 0644 /opt/vera-modelos/systemd/vera-modelos-control.service \
  /opt/vera-modelos/systemd/vera-modelos-guardian.service /opt/vera-modelos/systemd/vera-modelos-guardian.timer \
  /opt/vera-modelos/systemd/vera-modelos-puente.service /opt/vera-modelos/systemd/vera-modelos-puente.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemd-analyze verify /etc/systemd/system/vera-modelos-control.service /etc/systemd/system/vera-modelos-guardian.service
sudo systemd-analyze security vera-modelos-control.service
```

En esta máquina `systemd-analyze verify` solo se quejó de que los binarios no existen y la exposición dio 1,5 de 10 [V, systemd 262
local, VM en Debian 13 con systemd 257 [N]].

### Paso 6. Ensayo en seco de `modelos-control` (no crea nada, no gasta)

```bash
sudo systemd-run --wait --pipe --collect -p User=vera-modelos -p StateDirectory=vera-modelos \
  -p EnvironmentFile=/etc/vera-modelos/modelos-control.env /usr/local/bin/modelos-control --seco
```

Valida la configuración y muestra el plan **sin imprimir secretos** (M1 lo hace con un redactor [V, `internal/modelos/secreto`]).
Ejecuta el binario con el mismo usuario y entorno que tendrá el servicio, sin dejar nada instalado. **[N]** que la sintaxis de
`systemd-run` y el comportamiento exacto de `--seco` funcionen en la VM.

### Paso 7. Instalar los puentes sin aplicarlos (C10)

```bash
sudo bash /opt/vera-modelos/vm/puente-modelos.sh instalar --seco     # imprime los archivos, no escribe nada
sudo bash /opt/vera-modelos/vm/puente-modelos.sh instalar            # los escribe, no toca contenedores
sudo cat /opt/centinela/infra-servidor/compose.z-modelos.yaml
```

**Revisa el override con `cat`, no con `docker compose config`.** `config` mezcla el contenido de todos los `env_file` en su salida e
imprimiría en la terminal `JWT_SECRET`, los peppers, `CUSTODIO_TOKEN`, las URL de base de datos con contraseña y los tokens de los
modelos. Además, con solo dos archivos fallaría en la VM porque la red `centinela` se declara en `compose.yaml`. El override generado
**no contiene ningún secreto** [V, hay una prueba]. Debe mostrar `VERA_PROVEEDOR: "local"` en `api`, el servicio `puente-modelos` con
`network_mode: "service:api"` y `puente-host-modelos` con `network_mode: host`. Si de todos modos quieres la vista combinada, usa
todos los `compose*.yaml` y la bandera `--no-env-resolution` **[N]** (existe en el compose local, comprueba `docker compose config
--help` en la VM antes) y no pegues la salida en ningún chat.

### Paso 8. Encender `modelos-control` y aplicar (C11, C12)

Primero el servicio, después la API: así los puentes tienen a quién reenviar (aunque el pod esté apagado).

```bash
sudo systemctl enable --now vera-modelos-control.service
sudo systemctl status vera-modelos-control.service --no-pager
sudo ss -ltn            # nuevos: 127.0.0.1:{11434,8766,8767,8790}, nada en 0.0.0.0
sudo bash /opt/vera-modelos/vm/puente-modelos.sh aplicar --seco
sudo bash /opt/vera-modelos/vm/puente-modelos.sh aplicar         # recrea la API: unos segundos de 502
sudo bash /opt/vera-modelos/vm/puente-modelos.sh verificar       # RESULTADO=OK
sudo ss -ltn            # ahora también GATEWAY:{11434,8766,8767} del salto
sudo systemctl enable --now vera-modelos-guardian.timer vera-modelos-puente.timer
```

### Rollback completo, en este orden

```bash
sudo systemctl disable --now vera-modelos-puente.timer vera-modelos-guardian.timer vera-modelos-control.service
sudo bash /opt/vera-modelos/vm/puente-modelos.sh desinstalar     # quita los puentes y recrea la API en simulado
sudo rm -f /etc/systemd/system/vera-modelos-control.service /etc/systemd/system/vera-modelos-guardian.service \
  /etc/systemd/system/vera-modelos-guardian.timer /etc/systemd/system/vera-modelos-puente.service /etc/systemd/system/vera-modelos-puente.timer
sudo systemctl daemon-reload
# Si hubo un pod: terminarlo en la consola de RunPod y confirmar que desapareció (el guardián ya no corre).
# Opcional, si no se va a reintentar: borrar secretos y usuario.
sudo rm -rf /etc/vera-modelos /opt/centinela/api/modelos.env /var/lib/vera-modelos
sudo userdel vera-modelos
```

Cada paso es reversible por separado. Los pasos 1 a 7 no cambian el comportamiento de producción: solo el paso 8 (`aplicar`) lo hace.

## 5. Llaves y tokens sin pasar por el chat ni por el repositorio

- **Tokens** (control, voz, STT): los genera `generar-secretos.sh` con `openssl rand -hex 32` dentro de la VM, los escribe con
  `umask 077` y `mv` atómico, y **nunca los imprime**. Voz y STT están en dos archivos 0600: `/etc/vera-modelos/tokens.env` (dueño
  `vera-modelos`, para `modelos-control`) y `/opt/centinela/api/modelos.env` (dueño root, para el contenedor de la API). El token de
  control está en `/etc/vera-modelos/control.token`. La clave de RunPod vive solo en `modelos-control.env` (root): **no entra** al
  contenedor de la API. Son largos y **estáticos**: no cambian por arranque. Se verificó que los dos archivos de voz y STT coinciden
  tras generar y tras rotar [V, `vm/generar-secretos_test.sh`].
- **Clave de RunPod:** se escribe en la terminal de la VM al ejecutar el script (entrada sin eco). Si prefieres no escribirla, el script
  también la lee de stdin cuando no hay terminal: puedes enviarla por un pipe desde tu gestor local de contraseñas hacia
  `ssh -p "$PUERTO" centinela@"$IP" 'sudo bash /opt/vera-modelos/vm/generar-secretos.sh ...'` sin dejarla en el historial ni en argv.
  Comprueba primero que ese gestor esté instalado en tu máquina. Este agente no leyó ningún secreto.
- **Llave SSH (VM hacia pod):** `ssh-keygen -t ed25519 -N ''` en la VM, privada en `/etc/vera-modelos/ssh/` con dueño `vera-modelos`
  y modo 0600 (OpenSSH la rechaza si otros usuarios pueden leerla), pública 0644. `modelos-control` la entrega al pod al crearlo como
  `PUBLIC_KEY` [V en su `LeerLlavePublica`, **[N]** que la plantilla de RunPod la aplique]. No hay llave de CI hacia la VM: no se
  toca `authorized_keys` ni `deploy-ci.sh`.
- **Rotación:** `sudo bash generar-secretos.sh --rotar` genera tokens nuevos, conserva la clave de RunPod y los valores no secretos,
  y avisa que hay que reiniciar `vera-modelos-control` y recrear la API (`puente-modelos.sh aplicar`). La llave SSH no se rota con ese
  comando: borra el par y ejecuta el script de nuevo.
- **Qué hacer ante una fuga:** terminar cualquier pod en la consola de RunPod, rotar la clave en RunPod y volver a ejecutar el
  script con `--clave-runpod`, y rotar los tokens con `--rotar`.
- **Hacia el pod:** `modelos-control` envía los tokens por stdin de `bash -s`. `arrancar-modelos.sh` los valida (32 caracteres o más,
  solo `A-Za-z0-9_-`), los quita de su entorno y los guarda en un archivo 0600 en el pod. Las sondas los envían con `curl -K -`
  (stdin), por lo que **no aparecen en `ps`** [V, la prueba usa un `curl` falso y comprueba que el token no está en argv].

## 6. Verificaciones seguras, sin pod y sin gasto

Todo corre en tu máquina o en la VM sin crear nada de pago:

```bash
shellcheck -x infra/modelos-runpod/pod/*.sh infra/modelos-runpod/vm/*.sh        # 0 hallazgos
bash infra/modelos-runpod/pod/modelos-pod_test.sh                                # 37 pruebas
bash infra/modelos-runpod/vm/puente-modelos_test.sh                              # 37 pruebas
bash infra/modelos-runpod/vm/generar-secretos_test.sh                            # 32 pruebas
bash infra/modelos-runpod/pod/arrancar-modelos.sh --seco                         # plan y variables efectivas
bash infra/modelos-runpod/pod/arrancar-modelos.sh --compuerta                    # solo CUDA, lo corres dentro de un pod
sudo bash /opt/vera-modelos/vm/generar-secretos.sh --seco
sudo bash /opt/vera-modelos/vm/puente-modelos.sh instalar --seco
sudo systemd-analyze verify /etc/systemd/system/vera-modelos-*.service
/usr/local/bin/modelos-control --seco                                            # (paso 6)
```

Qué cubren las pruebas [V]: la compuerta de CUDA (12.4 aborta con 14, 13.0 pasa, el formato nuevo `CUDA UMD Version` pasa, sin
`nvidia-smi` aborta con 14 también en la corrida completa), la validación de tokens por archivo y por entorno (modo, longitud, claves
no permitidas), el bloqueo entre corridas, la reutilización cuando los tres ya están sanos, el informe de salud (servidores HTTP falsos
con tokens), que el token nunca viaja en argv, la generación y rotación de secretos con modos 0600 y la separación de la clave de
RunPod, el modo salto y el directo del puente, y la detección y reparación de un puente huérfano (con `--no-deps`). Qué **no**
cubren: la instalación de Ollama, pip, descargas, el arranque real de los servidores, la VRAM y el comportamiento real de
`docker compose` y de nginx en la VM.

## 7. Primera prueba real controlada

**Antes de encender**
- Cuenta de RunPod con **saldo prepagado bajo** (5 USD según el diseño). Es el tope duro: si todo lo demás falla, el pod muere al
  llegar a 0 USD. Con 0,27 USD/h son unas 18 horas, así que no sustituye al guardián.
- Para la primera vez, baja los límites en `/etc/vera-modelos/modelos-control.env` y reinicia el servicio:
  `MODELOS_SESION_MAXIMA=1h` y `MODELOS_INACTIVIDAD=5m`. Después los devuelves a `4h` y `10m`.
- Confirma que `modelos-control` usa la API v2 de RunPod (la v1 se retiró hoy): `MODELOS_RUNPOD_URL` no debe estar definido o debe
  apuntar a `https://api.runpod.io/v2`.
- Ten abierta la consola de RunPod para ver el pod y poder terminarlo a mano.

**Costo y límites** (tarifa de la A5000 Secure: 0,27 USD/h [V en el diseño aprobado, https://www.runpod.io/pricing del 12 de noviembre])

| Escenario | Costo |
|-----------|-------|
| Ensayo de 15 minutos | unos 0,07 USD |
| Primera prueba con el tope de 1 hora | 0,27 USD como máximo |
| Una sesión de 4 horas (la normal) | 1,08 USD |
| Techo por sesión con el tope de 0,35 USD/h | 1,40 USD en 4 horas |

El disco del pod (sin volumen de red) se factura solo mientras el pod existe. El 0,27 es la tarifa de la GPU, **[N]** si el disco
del contenedor (40 GB por defecto en M1) suma algo con esta imagen.

**Procedimiento**
1. `sudo systemctl status vera-modelos-control.service` activo y sin reinicios.
2. Enciende desde el puerto de control, con el token que está en `/etc/vera-modelos/control.token` (no lo pegues en comandos que
   queden en el historial: léelo dentro de la VM):
   `sudo -u vera-modelos sh -c 'curl -s -X POST -H "Authorization: Bearer $(cat /etc/vera-modelos/control.token)" http://127.0.0.1:8790/encender'`
   (el token se expande dentro de la VM y aparece en `ps` un instante: aceptable para una prueba, evítalo en uso normal). `GET /estado`
   con la misma cabecera muestra la fase.
3. Observa: `sudo journalctl -u vera-modelos-control -f`. En el pod, `tail -n 1 /workspace/vera-estado/progreso.jsonl`. Tiempos esperados:
   en la campaña de la tesis el bootstrap sin volumen tardó **98 s en una 4090**, con 43 s de descarga del GGUF, y la primera carga del
   modelo unos 50 s [V en el diseño aprobado, solo 4090]. Para la A5000 y el aprovisionamiento del pod no hay medida [N]: espera de
   3 a 8 minutos hasta `listo` [I].
4. Con `listo`, comprueba el camino completo desde la API: `docker exec centinela-modelos-puente wget -qO- http://127.0.0.1:11434/api/version`
   (pasa por los dos puentes, `modelos-control`, el túnel y el pod). Después prueba un turno de texto, una transcripción y una síntesis desde
   la app o con los scripts de `docs/e2e-voz-local-2026-11-13.md`.
5. Mide y anota: tiempo hasta `listo`, tiempo del primer turno, VRAM (`nvidia-smi` en el pod) y que `size_vram == size`.
6. Apaga con `POST /apagar`. Confirma en la consola de RunPod que el pod **ya no existe** y que `systemctl list-timers 'vera-modelos-*'`
   sigue activo.
7. Deja otro pod encendido sin tocarlo para comprobar el apagado automático (5 minutos si bajaste el límite) y que el guardián lo termina.
8. Devuelve `MODELOS_SESION_MAXIMA=4h` y `MODELOS_INACTIVIDAD=10m`.

**Qué mirar si algo falla:** código 14 o 15 (otro host), 13 (VRAM), 2 (configuración de tokens o SHA). Si falló un servicio, el campo
`detalle` de la línea `fallo` de `progreso.jsonl` trae las últimas 20 líneas de su log. Los servidores no registran sus tokens, pero
revisa ese texto antes de pegarlo en un chat.

## 8. Qué cambia en producción y cómo se comporta la API sin pod

| Variable | Valor con esta instalación | Antes |
|----------|----------------------------|-------|
| `VERA_PROVEEDOR` | `local` | `simulado` (`banderas.tsv`, columna produccion) |
| `VERA_MODELO` | `gemma4-e4b-local` | vacío |
| `VERA_OLLAMA_URL` | `http://127.0.0.1:11434` | por defecto, igual |
| `VERA_STT_URL` y `VERA_STT_TOKEN` | `http://127.0.0.1:8767` y el token de `modelos.env` | sin definir |
| `VERA_VOZ_URL` y `VERA_VOZ_TOKEN` | `http://127.0.0.1:8766` y el token de `modelos.env` | URL por defecto, sin token |

Se aplican con el override `compose.z-modelos.yaml`, **no** editando `banderas.tsv`: cambiar `VERA_PROVEEDOR` de producción allí exige
tocar a la vez `banderas.tsv`, `.env.produccion.example` y `config_banderas_test.go` o la prueba falla [V en el diseño aprobado]. Efectos:

- `make verificar-banderas` y `infra/servidor/verificar-banderas.sh produccion` reportarán `DIFF` en `VERA_PROVEEDOR` y las variables
  de modelos. Es esperado: el contenedor difiere de la matriz a propósito. Si prefieres una matriz honesta, es otro PR que cambia
  esos tres archivos a la vez.
- Esta política es la (a) del diseño: **`local` permanente**.

**Con el pod apagado** (comportamiento del código actual, diseño aprobado [V], `internal/platform/httpserver`):
- La API **arranca bien**: con `VERA_PROVEEDOR=local` no se sondea Ollama al arrancar. `VERA_STT_URL` y `VERA_STT_TOKEN` están ambas
  definidas, y `VERA_VOZ_TOKEN` con URL de loopback no bloquea el arranque.
- Un turno de **texto** responde **503** (`ErrNoDisponibleIA`). No hay caída automática a `simulado`.
- La **transcripción** falla.
- La **síntesis** responde 200 degradado con la cabecera `X-Vera-Audio: no-disponible`.
- Con el pod apagado, el proxy de `modelos-control` responde `503` con `{"error":"modelos_apagados"}` y un reintento sugerido de 30 s, sin
  retener la conexión [V en las constantes de `internal/modelos/proxy/proxy.go`, no ejecutado]. Con `modelos-control` caído, el salto recibe un rechazo de
  conexión y el resultado para la API es el mismo.
- `/readyz` de la API no cambia (el CD y los despliegues siguen su camino normal). `GET /readyz/modelos` reportará los tres como caídos.

Si el producto no tolera esos 503, la salida es de producto, no de infraestructura: un proveedor compuesto en Go que caiga a
`simulado` (decisión 10 del diseño aprobado, no se hace aquí).

**Regla operativa:** un despliegue del CD recrea `centinela-api` y deja huérfano el puente. `vera-modelos-puente.timer` lo repara en
2 minutos como máximo, o ejecútalo a mano: `sudo bash /opt/vera-modelos/vm/puente-modelos.sh reparar`.

## 9. Riesgos conocidos

1. **Puente huérfano tras un despliegue** (verificado a mano con docker local). Mitigado con `verificar`, `reparar` y el temporizador, pero
   hay hasta 2 minutos de rechazo de conexión. `reparar` usa `up -d --no-deps --force-recreate` para no arrastrar `api`, `migrate`,
   `postgres` ni `redis` desde un temporizador de root. **[N]** que compose resuelva `service:api` al contenedor actual con `--no-deps`
   en la VM (la prueba usa un docker simulado).
2. **La IP del gateway puede cambiar** si la red `centinela` se vuelve a crear. El salto del host fallaría al ligar su IP. Vuelve a
   ejecutar `puente-modelos.sh instalar` y `aplicar`.
3. **Los puertos del gateway no tienen autenticación y los alcanza cualquier contenedor de la VM** (Postgres, Redis, simuladores, nginx, y
   los de otras redes bridge: se comprobó con docker local [V]). Aceptable en una VM de un solo propósito [I]. Mitigación posible, no hecha:
   reglas en `DOCKER-USER`.
4. **Un contenedor con red de host** (`centinela-modelos-puente-host`). Sin capacidades, solo lectura, usuario 101, liga una sola IP. Es la
   contrapartida de que `modelos-control` solo escuche en loopback.
5. **`MemoryDenyWriteExecute`, `SystemCallFilter=@system-service` y `RestrictAddressFamilies` sin `AF_NETLINK` pueden romper el binario de M1
   o los `ssh` y `scp` que lanza.** Si el servicio no arranca, se relajan de a uno y se documenta cuál fue **[N]**.
6. **Clave de RunPod con acceso total a la cuenta** (no hay claves restringidas para pods, diseño aprobado [V]). Mitigación: cuenta con
   prepago bajo. Está en el entorno del proceso `modelos-control`, legible solo por root y por el usuario `vera-modelos`.
7. **Confianza en el primer uso del host SSH del pod.** M1 guarda un `known_hosts` por pod con `accept-new` y trata una huella distinta como
   error fatal [V]. La primera conexión sigue siendo de confianza: un atacante en la ruta en esos segundos podría suplantar al pod. Riesgo
   aceptado en la demo (decisión 8 del diseño).
8. **Cada arranque descarga todo** (sin volumen de red): GGUF de unos 5 GB desde Hugging Face, Ollama desde `ollama.com` o GitHub, wheels
   desde PyPI, pesos de Kokoro desde GitHub. El GGUF y los pesos de Kokoro se verifican con sha256. Los pesos de Whisper los baja
   `huggingface_hub` con sus propias comprobaciones, **no con un sha256 nuestro**. El tarball del código por commit tampoco tiene hash propio.
   El instalador de Ollama (`install.sh`) se ejecuta como root, igual que en la tesis.
9. **Python del pod.** La imagen por defecto de M1 es Ubuntu 24.04, que trae Python 3.12 (los pines de `requirements*.txt` se verificaron
   contra wheels cp312) [I]. El script prefiere `python3.12`, luego `3.11`, luego `python3`.
10. **`OLLAMA_KEEP_ALIVE=24h` del pod no manda**: cada petición del backend lleva `keep_alive: 30m` (`internal/copilot/ollama/proveedor.go`) [V]
    y lo sobrescribe. Con el apagado a los 10 minutos de inactividad no importa.
11. **Una sola GPU compartida.** Whisper, Kokoro y Ollama comparten los 24 GB. Whisper y Kokoro reservan su VRAM antes de que arranque
    Ollama. Con `OLLAMA_NUM_PARALLEL` alto la VRAM puede no alcanzar (código 13).

## 10. Qué no se verificó

- **Nada se ejecutó contra RunPod, Azure, GitHub ni la VM.** Solo lecturas locales y experimentos con docker local (que se limpiaron).
- **`arrancar-modelos.sh` completo en un pod:** instalación de Ollama 0.40.10 (el orden `install.sh` y luego la release es el de la tesis, no
  sé cuál de los dos caminos se usó en la campaña), descarga del GGUF con `-C -` y reanudación, venvs con `--only-binary=:all:`,
  `descargar.py` con GPU, arranque de Whisper con `WHISPER_GPU=si` (la campaña lo hizo en 4090, no en A5000), Kokoro en CUDA (la compuerta
  de proveedor es la del servidor), `ollama create` con `FROM ./archivo` (igual que la tesis, la ruta absoluta no se probó) y
  `size_vram == size`.
- **Que `--wildcards` y `--strip-components=2` de `tar` extraigan bien el tarball de `codeload.github.com`** con ese commit (la lógica es
  correcta sobre el papel, no se corrió contra un tarball real).
- **Que existan `pgrep`, `pkill`, `flock`, `zstd` y `curl` en la imagen del pod** (la tesis exigía los primeros y usaba otra plantilla), que
  `/workspace` exista y sea escribible, y que `runpod/pytorch:1.0.2-cu1281-torch280-ubuntu2404` siga existiendo.
- **Que `openssl`, `ssh`, `scp` y `ssh-keygen` estén en la VM**, la versión de `docker compose` de la VM y que el glob `compose*.yaml` ordene
  `z-modelos` al final.
- **Los puentes en la VM real:** solo se probaron con docker local y con un docker simulado en las pruebas.
- **Que `systemd-analyze verify` sea limpio con el binario de M1 presente**, y que el sandbox no lo rompa.
- **Que el binario de M1 de `dev` coincida con lo que leí** (tenía cambios sin confirmar).
- **Que `PUBLIC_KEY` entregue la llave pública en la imagen de RunPod** y que `ssh -L` funcione sobre el TCP directo del pod (el diseño ya lo
  marcaba [N]).
- **El cobro exacto del disco del contenedor** y la disponibilidad de stock Secure para la A5000.
- **Un `/readyz` unificado en el pod:** no se hizo a propósito. Exigiría un cuarto proceso y un cuarto puerto en el túnel.
  `salud-modelos.sh --json` y las sondas de `modelos-control` cubren lo mismo.

## 11. Archivos de esta pieza

| Archivo | Para qué |
|---------|----------|
| `infra/modelos-runpod/pod/arrancar-modelos.sh` | Dentro del pod: prepara y arranca los tres modelos. Idempotente |
| `infra/modelos-runpod/pod/salud-modelos.sh` | Dentro del pod: sonda los tres servicios con sus tokens |
| `infra/modelos-runpod/pod/modelos-pod_test.sh` | 37 pruebas de los dos anteriores, sin pod |
| `infra/modelos-runpod/vm/generar-secretos.sh` | En la VM: usuario, tokens, llave SSH y archivos 0600 |
| `infra/modelos-runpod/vm/puente-modelos.sh` | En la VM: puentes hacia la red de la API (instalar, aplicar, verificar, reparar, desinstalar) |
| `infra/modelos-runpod/vm/*.plantilla` y `*.fragmento` | Plantillas del override de compose y de las configuraciones `stream` |
| `infra/modelos-runpod/vm/*_test.sh` | Pruebas de los dos scripts de la VM |
| `infra/modelos-runpod/systemd/` | `vera-modelos-control.service`, `-guardian.service` y `.timer`, `-puente.service` y `.timer` |
| `infra/modelos-runpod/.env.example` | Todas las variables (las de `modelos-control` y las del puente), con valores de ejemplo no secretos |

## 12. Divergencias con el diseño aprobado y con el encargo

| Diseño aprobado o encargo | Aquí | Motivo |
|---------------------------|------|--------|
| Sidecar docker con el cliente `ssh` abre el túnel | Binario Go `modelos-control` como servicio del host abre el túnel | Decisión de Christian del 15 de noviembre. **Obliga a los puentes de la sección 0**, porque la API no ve el loopback del host |
| `modelos-control` escucha en la IP del gateway (encargo) | Escucha en loopback y un salto en la red del host llega hasta él | M1 solo acepta loopback (`config.go`, `direccion`) |
| Variables `VERA_MODELOS_*` y `modelos-control serve` (encargo) | `MODELOS_*` y `servir`, como parsea el binario de M1 | El código de M1 es el contrato real. Mapa en la sección 2 |
| Tokens al pod como variables de entorno al crearlo | Por stdin de `bash -s` en cada arranque, y archivo 0600 en el pod | Es lo que implementa M1. El pod ya existe cuando se ejecuta el script. Sin secretos en la API de RunPod |
| Tokens en un archivo de root | Archivos de dueño `vera-modelos` (0600) para `modelos-control`, y uno de root para la API | M1 lee los tokens por ruta y los valida con su propio usuario |
| Kokoro en CPU | Kokoro fp32 en CUDA con compuerta de proveedor | Decisión de Christian. Exige CUDA 13.0 en el driver y la rama `feat/kokoro-gpu` |
| Política (b): conmutar a `simulado` entre ventanas | Política (a): `local` permanente, 503 con el pod apagado | Decisión de Christian ("producción siempre usa RunPod, nunca simulado") |
| Workflows de GitHub para encender y apagar (con cron cada 15 min) | Botón y temporizador systemd en la VM cada 5 min | Decisión de Christian. No hay secretos de RunPod en GitHub |
| Acceso a la VM por llave de CI con comando forzado | Sin llave nueva de CI: instalación manual por ssh con aprobación | Alcance de este encargo. No se toca `authorized_keys` |
| `arrancar-pod.sh` en `infra/modelos/runpod/` | `infra/modelos-runpod/pod/arrancar-modelos.sh` | Propiedad de archivos del encargo |
| Puente: contenedor con `ssh -L` en la red de la API | Puentes nginx `stream` y el `ssh -L` lo abre `modelos-control` en el host | Consecuencia del binario en el host. Misma fragilidad ante la recreación de la API, ahora vigilada por un temporizador |

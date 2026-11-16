# Modelos de Vera en RunPod: instalación en la VM de Azure

Fecha: 15 de noviembre de 2026. Estado: **código listo y probado sin pod, nada aplicado**. No se creó ningún pod, no se tocó
Azure, la VM, GitHub ni RunPod. Este documento es la lista de cambios que Christian aprueba antes de ejecutar nada.

Marcas: **[V]** verificado en esta sesión (con cómo), **[I]** inferido, **[N]** no verificado y por comprobar.

Complementa el diseño aprobado (`docs/despliegue-modelos-runpod.md` de la rama de despliegue, aún no está en `dev`). Lo que
cambia respecto de ese diseño está en la sección 12.

## 0. Tres cosas antes de leer el resto

**1. El binario del host no llega a la API por sí solo, y se resolvió con un puente.** La API corre en el contenedor
`centinela-api`, en la red docker `centinela` (`infra/servidor/pasos/60-api.sh`, `networks: - centinela`) [V]. Dentro de ese
contenedor `127.0.0.1` es su propio loopback. Los clientes de voz y de transcripción solo aceptan `http://127.0.0.1`
(`internal/copilot/vozlocal/voz.go:29`, `internal/copilot/sttlocal/cliente.go:28`) [V]. Un `modelos-control` que escuche en
el `127.0.0.1` de la VM es inalcanzable desde ahí. Solución, sin tocar Go:

- `modelos-control` escucha en la **IP del gateway de la red docker** (nunca `0.0.0.0`).
- Un contenedor puente (`nginx:1.27.5-alpine` con el módulo `stream`, la misma imagen que ya usa `30-nginx.sh`) comparte el
  espacio de red de la API (`network_mode: service:api`) y reenvía su `127.0.0.1:{11434,8767,8766}` a esa IP.
- Verificado con docker local el 15 de noviembre [V]: un servicio del host ligado al gateway fue alcanzado desde el
  `127.0.0.1` de otro contenedor a través del puente, con `read_only`, `cap_drop: ALL` y usuario 101. El mismo experimento
  confirmó el riesgo: al recrear el contenedor de la API, el puente sigue "running" pero queda en el espacio de red viejo y
  la conexión se rechaza sin ningún aviso. Por eso existe `puente-modelos.sh verificar|reparar` y un temporizador.
- Se comprobó con `docker compose config` que `env_file` se suma y `environment` se pisa clave por clave al combinar
  `compose.api.yaml` con el override [V].
- Pendiente de decisión de M1: `VERA_MODELOS_ESCUCHA` debe poder configurarse. Si `modelos-control` solo supiera escuchar
  en loopback, el puente no lo alcanzaría.

**2. Dependencias previas.** (a) La rama `feat/kokoro-gpu` (variante GPU de Kokoro: `requirements-cuda.txt`, el nuevo
`descargar.py` y `VERA_VOZ_DISPOSITIVO` en `servidor.py`) debe estar fusionada y el commit que se fije en
`VERA_MODELOS_CODIGO_SHA` debe contenerla. Hoy esa rama es local, va 6 commits por delante de `origin/dev` y no tiene PR [V].
`arrancar-modelos.sh` aborta con un mensaje claro si falta [V, probado en la compuerta, no el camino completo]. (b) El binario
`cmd/modelos-control` (M1) debe estar en `dev`. (c) La API REST v1 de RunPod se retira hoy, 15 de noviembre de 2026 (diseño
aprobado, sección 0 [V en ese documento]): `modelos-control` debe hablar con la v2.

**3. Qué se prueba sin gastar y qué no.** Hay 76 pruebas automáticas (sección 6) que no necesitan pod. La instalación
real de Ollama, pip, las descargas, el arranque de los tres servidores y la VRAM **solo se pueden verificar con un pod**.

## 1. Arquitectura

```
 Usuario (app Vera) -> HTTPS -> nginx -> contenedor centinela-api (red docker "centinela")
                                           VERA_PROVEEDOR=local
                                           127.0.0.1:{11434,8767,8766}
                                                |   (mismo espacio de red)
                          contenedor centinela-modelos-puente (nginx stream, usuario 101, read_only)
                                                |   TCP hacia la IP del gateway de "centinela"
 VM centinela-vm (Debian 13) ------------------ v -------------------------------------------
   vera-modelos-control.service  (usuario vera-modelos, sin shell)
      escucha  GATEWAY:{11434,8767,8766}   y   127.0.0.1:8770 (control, con token)
      crea/apaga el pod por la API de RunPod, abre el túnel SSH (saliente), reconcilia
   vera-modelos-guardian.timer   cada 5 min: modelos-control reconcile (pods huérfanos o vencidos)
   vera-modelos-puente.timer     cada 2 min: puente-modelos.sh reparar (puente huérfano tras un deploy)
                                                |   ssh -L, saliente, hacia IP pública y puerto del pod
 RunPod, pod "vera-modelos" (RTX A5000, Secure, plantilla PyTorch, sin Docker, sin volumen de red)
   arrancar-modelos.sh  ->  Whisper small GPU :8767 (X-Vera-Token), Kokoro fp32 CUDA :8766 (X-Vera-Token),
                            Ollama + gemma4-e4b-local :11434. Todo en 127.0.0.1 del pod.
```

Quién enciende y apaga: el botón (control de `modelos-control`), el apagado por inactividad de 10 minutos y el guardián.
Tope duro de gasto: el saldo prepagado bajo de la cuenta de RunPod.

## 2. Contrato con `modelos-control` (M1)

Nombres acordados en el encargo y los que este código añade. Si M1 usa otros, cambia aquí y en `.env.example`.

| Pieza | Valor |
|-------|-------|
| Subcomandos | `modelos-control serve`, `reconcile`, `estado`, `--seco` |
| Secreto solo en el archivo de root | `RUNPOD_API_KEY` |
| Variables del encargo | `VERA_MODELOS_GPUS`, `_TARIFA_MAX`, `_INACTIVIDAD_MIN`, `_SESION_MAX_H`, `_SSH_CLAVE`, `_DIR_ESTADO`, `_SCRIPT_ARRANQUE` |
| Variables añadidas (propuestas) | `_TOKEN_CONTROL`, `_TOKEN_VOZ`, `_TOKEN_STT`, `_NUBE`, `_NOMBRE_POD`, `_PLANTILLA`, `_CODIGO_SHA`, `_OLLAMA_PARALELO`, `_SCRIPT_SALUD`, `_SSH_CLAVE_PUBLICA`, `_SSH_USUARIO`, `_ESCUCHA`, `_PUERTO_OLLAMA`, `_PUERTO_VOZ`, `_PUERTO_STT`, `_CONTROL_ESCUCHA` |
| Variables solo del puente (M1 las ignora) | `VERA_MODELOS_PUENTE_IMAGEN`, `VERA_MODELOS_RED_DOCKER` |
| Bloqueo compartido | `serve` y `reconcile` comparten el mismo usuario y `VERA_MODELOS_DIR_ESTADO`: deben tomar un `flock` allí |
| Lo que `modelos-control` hace con el pod | (1) crear con la plantilla, la llave pública y el nombre `vera-modelos...` (2) esperar SSH (3) entregar `tokens.env` y ejecutar el script de arranque (4) abrir el túnel `-L` (5) escuchar y reenviar |

Cómo habla `modelos-control` con el pod (contrato de `arrancar-modelos.sh`):

1. Entrega el archivo de tokens por stdin, nunca en argv:
   `ssh pod 'umask 077; mkdir -p /workspace/vera-estado; cat > /workspace/vera-estado/tokens.env' < tokens.env`
   El contenido son dos líneas, `WHISPER_TOKEN=<VERA_MODELOS_TOKEN_STT>` y `VERA_LAB_TOKEN=<VERA_MODELOS_TOKEN_VOZ>`.
   (Los servidores leen esos nombres: `infra/stt-local/servidor.py` lee `WHISPER_TOKEN` y `infra/voz-local/servidor.py` lee
   `VERA_LAB_TOKEN`, no `KOKORO_TOKEN` [V].)
2. Ejecuta el script **sin ligarlo a la sesión** y sin pasar secretos:
   `ssh pod 'VERA_CODIGO_SHA=<40 hex> OLLAMA_NUM_PARALLEL=2 bash -s' < arrancar-modelos.sh`
   (el script es autocontenido, también funciona copiado). Los servidores quedan lanzados con `setsid nohup` y sobreviven al ssh.
3. Lee el progreso: `ssh pod 'tail -n 1 /workspace/vera-estado/progreso.jsonl'` hasta ver `"estado": "listo"` o `"fallo"`.
4. Para el estado de salud: `ssh pod 'bash salud-modelos.sh --json'` (0 sano, 1 algún servicio caído, 2 tokens inválidos).

Formato de `progreso.jsonl` (una línea por evento, se trunca al empezar cada corrida, cada línea es un solo `printf` en modo append):

```json
{"epoch": 1763251200, "paso": "gguf", "estado": "en_curso", "codigo": 0, "detalle": ""}
```

- `estado`: `en_curso`, `ok`, `fallo` o `listo`. La **última línea siempre es `listo` o `fallo`** (hay una trampa `EXIT` que lo garantiza).
- `paso`: `inicio`, `entorno`, `compuerta_cuda`, `tokens`, `salud`, `codigo`, `apt`, `ollama_instalar`, `gguf`, `venvs`, `pesos`, `detener`,
  `whisper`, `kokoro`, `ollama`, `listo`.
- Una línea sin salto final es una escritura en curso: ignorarla.

Códigos de salida de `arrancar-modelos.sh` (y `codigo` de la línea `fallo`):

| Código | Significado | Qué hace `modelos-control` |
|--------|-------------|----------------------------|
| 0 | Listo (o reutilizado: los tres ya respondían) | Abrir túnel y servir |
| 1 | Fallo genérico (falta una herramienta, Ollama no se pudo fijar) | Reintentar una vez, luego terminar el pod |
| 2 | Entrada inválida (tokens, modo del archivo, variables, falta el código) | No reintentar: es un error de configuración |
| 3 | sha256 o tamaño del GGUF distinto del lockfile | No reintentar con el mismo commit |
| 4 | Un servicio no quedó sano a tiempo | Reintentar una vez |
| 13 | Ollama no cabe entero en VRAM (`size_vram < size`) | Terminar el pod, probar otra GPU |
| 14 | El driver del host no ofrece CUDA 13.0 | **Terminar el pod y crear otro** (otro host) |
| 15 | Kokoro abortó porque `CUDAExecutionProvider` no cargó | Igual que 14 |

Durante el arranque del pod, `modelos-control` debe **responder rápido** (503 o rechazo) en los tres puertos en lugar de
retener la conexión: el puente corta la conexión al upstream a los 3 s, pero una conexión aceptada y retenida espera hasta
600 s en Ollama y 120 s en voz y STT.

## 3. Lista de cambios que Christian aprueba antes de ejecutar

Todo es nuevo. **No se modifica ningún archivo existente de la VM.** El orden de la sección 4 es el de esta tabla.

| # | Qué | Dónde | Dueño y modo | Comando que lo crea |
|---|-----|-------|--------------|---------------------|
| C1 | Copia del repositorio de la pieza | `/opt/vera-modelos/` (`pod/`, `vm/`, `systemd/`, `.env.example`) | root:root 0755, scripts 0755 | `scp` y `sudo cp -rT` |
| C2 | Binario `modelos-control` (compilado de `cmd/modelos-control`) | `/usr/local/bin/modelos-control` | root:root 0755 | `sudo install` |
| C3 | Usuario de sistema `vera-modelos` | `/etc/passwd` | sin shell (`/usr/sbin/nologin`), sin home, **fuera del grupo docker** | `generar-secretos.sh` (`useradd --system`) |
| C4 | Archivo de entorno con los secretos y la configuración | `/etc/vera-modelos/modelos-control.env` | root:root **0600** | `generar-secretos.sh` |
| C5 | Tokens de la API (solo `VERA_STT_TOKEN` y `VERA_VOZ_TOKEN`) | `/opt/centinela/api/modelos.env` | root:root **0600** | `generar-secretos.sh` |
| C6 | Llave SSH del túnel (VM hacia pod) | `/etc/vera-modelos/ssh/tunel_ed25519` y `.pub` | privada vera-modelos **0600**, pública 0644 | `generar-secretos.sh` (`ssh-keygen -t ed25519`) |
| C7 | Cinco unidades systemd | `/etc/systemd/system/vera-modelos-{control,guardian,puente}.*` | root:root 0644 | `sudo install` |
| C8 | Carpeta de estado | `/var/lib/vera-modelos` | vera-modelos 0700 (la crea systemd) | `StateDirectory=` |
| C9 | Override de compose y configuración del puente | `/opt/centinela/infra-servidor/compose.z-modelos.yaml` y `modelos-puente/puente-modelos.conf` | root:root 0644 | `puente-modelos.sh instalar` |
| C10 | Contenedor `centinela-modelos-puente` y **recreación de `centinela-api`** con `VERA_PROVEEDOR=local` | docker | n/a | `puente-modelos.sh aplicar` |
| C11 | Activar `vera-modelos-control.service` y los dos temporizadores | systemd | n/a | `systemctl enable --now` |

- **Firewall y red de Azure: ninguna regla nueva.** El túnel SSH hacia el pod y las llamadas a la API de RunPod son **salientes**.
  Los sockets nuevos son: `GATEWAY:{11434,8766,8767}` (IP del bridge docker, solo alcanzable desde la propia VM y sus contenedores)
  y `127.0.0.1:8770` (control). Nada escucha en `0.0.0.0`. La NSG no se toca. El repositorio no configura firewall de host (solo la
  NSG de Azure) [V: `rg ufw|nft|iptables infra/servidor` sin resultados], así que el tráfico contenedor a gateway no está
  bloqueado [I].
- **Tiempo de indisponibilidad:** C10 recrea la API: unos segundos de 502 en nginx. Hacerlo fuera de horario de uso.
- **Lo que NO se hace:** no se edita `banderas.tsv`, `.env.*.example`, `compose.api.yaml`, `deploy-ci.sh` ni `authorized_keys`.
  No se crean usuarios en el grupo `docker`. No se abre ningún puerto.

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
command -v openssl ssh-keygen docker systemd-analyze
docker compose version
docker network inspect centinela --format '{{(index .IPAM.Config 0).Gateway}}'
sudo ss -ltn
```

`openssl` y `ssh-keygen` en la VM son **[N]** (el diseño aprobado ya lo marcaba): si falta alguno, `generar-secretos.sh` aborta con
código 69 antes de cambiar nada.

### Paso 3. Averiguar la IP del gateway

```bash
GATEWAY="$(sudo bash /opt/vera-modelos/vm/puente-modelos.sh gateway)"
echo "$GATEWAY"
```

Es la IP donde escuchará `modelos-control`. La red `centinela` no fija su subred (`30-nginx.sh`, `networks: centinela: name:
centinela`) [V], así que puede cambiar si alguien ejecuta `docker compose down` y vuelve a crearla. `puente-modelos.sh instalar`
vuelve a leerla y `verificar` lo delataría.

### Paso 4. Secretos (C3, C4, C5, C6): sin que pasen por el chat ni por el repositorio

```bash
sudo bash /opt/vera-modelos/vm/generar-secretos.sh --seco --escucha "$GATEWAY" --codigo-sha <COMMIT_DE_40_CARACTERES>
sudo bash /opt/vera-modelos/vm/generar-secretos.sh --escucha "$GATEWAY" --codigo-sha <COMMIT_DE_40_CARACTERES>
```

La primera línea es el ensayo: imprime lo que haría y no cambia nada. La segunda pide la clave de RunPod **en la terminal, sin
eco** (`read -rs`): se pega ahí, nunca en el chat, nunca como argumento. Los tres tokens se generan con `openssl rand -hex 32` y no
se imprimen. Se imprime solo la parte pública de la llave SSH, que no es secreta. Ver la sección 5.

### Paso 5. Instalar las unidades y comprobarlas (C7, C8)

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

Debe validar la configuración y mostrar el plan **sin imprimir secretos** (requisito para M1). Ejecuta el binario con el mismo
usuario y entorno que tendrá el servicio, sin dejar nada instalado. **[N]** la sintaxis exacta de `--seco` depende de M1.

### Paso 7. Instalar el puente sin aplicarlo (C9)

```bash
sudo bash /opt/vera-modelos/vm/puente-modelos.sh instalar --seco     # imprime los dos archivos
sudo bash /opt/vera-modelos/vm/puente-modelos.sh instalar            # los escribe, no toca contenedores
docker compose -f /opt/centinela/infra-servidor/compose.api.yaml -f /opt/centinela/infra-servidor/compose.z-modelos.yaml config   # otros compose*.yaml van también
```

Revisa la salida de `config`: `VERA_PROVEEDOR: local` en `api`, el servicio `puente-modelos` con `network_mode: service:api`, y
que **`RUNPOD_API_KEY` no aparece** en ningún lado.

### Paso 8. Encender `modelos-control` y aplicar (C10, C11)

Primero el servicio, después la API: así el puente tiene a quién reenviar (aunque el pod esté apagado).

```bash
sudo systemctl enable --now vera-modelos-control.service
sudo systemctl status vera-modelos-control.service --no-pager
sudo ss -ltn            # solo debe haber GATEWAY:{11434,8766,8767} y 127.0.0.1:8770 nuevos, nada en 0.0.0.0
sudo bash /opt/vera-modelos/vm/puente-modelos.sh aplicar --seco
sudo bash /opt/vera-modelos/vm/puente-modelos.sh aplicar         # recrea la API: unos segundos de 502
sudo bash /opt/vera-modelos/vm/puente-modelos.sh verificar       # RESULTADO=OK
sudo systemctl enable --now vera-modelos-guardian.timer vera-modelos-puente.timer
```

### Rollback completo, en este orden

```bash
sudo systemctl disable --now vera-modelos-puente.timer vera-modelos-guardian.timer vera-modelos-control.service
sudo bash /opt/vera-modelos/vm/puente-modelos.sh desinstalar     # quita el puente y recrea la API en simulado
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
  `umask 077` y `mv` atómico, y **nunca los imprime**. Están en dos archivos 0600 de root: el de control
  (`/etc/vera-modelos/modelos-control.env`) y el de la API (`/opt/centinela/api/modelos.env`, solo voz y STT, para que la clave de
  RunPod **no** entre al contenedor de la API). Son largos y **estáticos**: no cambian por arranque. Se verificó que ambos
  archivos coinciden tras generar y tras rotar [V, `vm/generar-secretos_test.sh`].
- **Clave de RunPod:** se escribe en la terminal de la VM al ejecutar el script (entrada sin eco). Si prefieres no escribirla, el script
  también la lee de stdin cuando no hay terminal: puedes enviarla por un pipe desde tu gestor local de contraseñas hacia
  `ssh -p "$PUERTO" centinela@"$IP" 'sudo bash /opt/vera-modelos/vm/generar-secretos.sh ...'` sin dejarla en el historial ni en argv.
  Comprueba primero que ese gestor esté instalado en tu máquina. Este agente no leyó ningún secreto.
- **Llave SSH (VM hacia pod):** `ssh-keygen -t ed25519 -N ''` en la VM, privada en `/etc/vera-modelos/ssh/` con dueño `vera-modelos`
  y modo 0600 (OpenSSH la rechaza si otros usuarios pueden leerla), pública 0644. La parte pública se imprime y `modelos-control`
  la entrega al pod al crearlo (`PUBLIC_KEY` en las plantillas de RunPod, **[N]** el mecanismo exacto). No hay llave de CI hacia la VM:
  no se toca `authorized_keys` ni `deploy-ci.sh`.
- **Rotación:** `sudo bash generar-secretos.sh --rotar` genera tokens nuevos, conserva la clave de RunPod y los valores no secretos,
  y avisa que hay que reiniciar `vera-modelos-control` y recrear la API (`puente-modelos.sh aplicar`). La llave SSH no se rota con ese
  comando: borra el par y ejecuta el script de nuevo.
- **Qué hacer ante una fuga:** terminar cualquier pod en la consola de RunPod, rotar la clave en RunPod y volver a ejecutar el
  script con `--clave-runpod`, y rotar los tokens con `--rotar`.
- **Del archivo de tokens al pod:** `modelos-control` lo entrega por stdin con `umask 077`. En el pod lo lee `arrancar-modelos.sh`
  con un lector propio que solo acepta dos claves, exige modo 600 y dueño igual al usuario que ejecuta, y **no ejecuta** el archivo
  [V, probado]. Los servidores reciben el token por entorno de su propio proceso y las sondas lo envían con `curl -K -` (stdin), por
  lo que **no aparece en `ps`** [V, la prueba usa un `curl` falso y comprueba que el token no está en argv].

## 6. Verificaciones seguras, sin pod y sin gasto

Todo corre en tu máquina o en la VM sin crear nada de pago:

```bash
shellcheck -x infra/modelos-runpod/pod/*.sh infra/modelos-runpod/vm/*.sh        # 0 hallazgos
bash infra/modelos-runpod/pod/modelos-pod_test.sh                                # 28 pruebas
bash infra/modelos-runpod/vm/puente-modelos_test.sh                              # 23 pruebas
bash infra/modelos-runpod/vm/generar-secretos_test.sh                            # 25 pruebas
bash infra/modelos-runpod/pod/arrancar-modelos.sh --seco                         # plan y variables efectivas
bash infra/modelos-runpod/pod/arrancar-modelos.sh --compuerta                    # solo CUDA, lo corres dentro de un pod
sudo bash /opt/vera-modelos/vm/generar-secretos.sh --seco
sudo bash /opt/vera-modelos/vm/puente-modelos.sh instalar --seco
sudo systemd-analyze verify /etc/systemd/system/vera-modelos-*.service
/usr/local/bin/modelos-control --seco                                            # (paso 6)
```

Qué cubren las pruebas [V]: la compuerta de CUDA (12.4 aborta con 14, 13.0 pasa, el formato nuevo `CUDA UMD Version` pasa, sin
`nvidia-smi` aborta con 14), la validación de tokens (modo, longitud, claves no permitidas), la reutilización cuando los tres ya
están sanos, el informe de salud (servidores HTTP falsos con tokens), que el token nunca viaja en argv, la generación y rotación de
secretos con modos 0600, y la detección y reparación de un puente huérfano. Qué **no** cubren: la instalación de Ollama, pip,
descargas, el arranque real de los servidores y la VRAM.

## 7. Primera prueba real controlada

**Antes de encender**
- Cuenta de RunPod con **saldo prepagado bajo** (5 USD según el diseño). Es el tope duro: si todo lo demás falla, el pod muere al
  llegar a 0 USD. Con 0,27 USD/h son unas 18 horas, así que no sustituye al guardián.
- Para la primera vez, baja los límites en `/etc/vera-modelos/modelos-control.env` y reinicia el servicio:
  `VERA_MODELOS_SESION_MAX_H=1` y `VERA_MODELOS_INACTIVIDAD_MIN=5`. Después los devuelves a 4 y 10.
- Confirma que `modelos-control` usa la API v2 de RunPod (la v1 se retiró hoy).
- Ten abierta la consola de RunPod para ver el pod y poder terminarlo a mano.

**Costo y límites** (tarifa de la A5000 Secure: 0,27 USD/h [V en el diseño aprobado, https://www.runpod.io/pricing del 12 de noviembre])

| Escenario | Costo |
|-----------|-------|
| Ensayo de 15 minutos | unos 0,07 USD |
| Primera prueba con el tope de 1 hora | 0,27 USD como máximo |
| Una sesión de 4 horas (la normal) | 1,08 USD |
| Techo por sesión con el tope de 0,35 USD/h | 1,40 USD en 4 horas |

El disco del pod (sin volumen de red) se factura solo mientras el pod existe. El 0,27 es la tarifa de la GPU, **[N]** si el disco
del contenedor suma algo en esta plantilla.

**Procedimiento**
1. `sudo systemctl status vera-modelos-control.service` activo y sin reinicios.
2. Pulsa el botón de encendido (el mecanismo que exponga `modelos-control`, con `VERA_MODELOS_TOKEN_CONTROL`).
3. Observa: `sudo journalctl -u vera-modelos-control -f` y el progreso (`tail -n 1 .../progreso.jsonl` por ssh). Tiempos esperados:
   en la campaña de la tesis el bootstrap sin volumen tardó **98 s en una 4090**, con 43 s de descarga del GGUF, y la primera carga del
   modelo unos 50 s [V en el diseño aprobado, solo 4090]. Para la A5000 y el aprovisionamiento del pod no hay medida [N]: espera de
   3 a 8 minutos hasta `listo` [I].
4. Con `listo`, comprueba el camino completo desde la API: `docker exec centinela-modelos-puente wget -qO- http://127.0.0.1:11434/api/version`
   (pasa por puente, `modelos-control`, túnel y pod). Después prueba un turno de texto, una transcripción y una síntesis desde la app o
   con los scripts de `docs/e2e-voz-local-2026-11-13.md`.
5. Mide y anota: tiempo hasta `listo`, tiempo del primer turno, VRAM (`nvidia-smi` en el pod) y que `size_vram == size`.
6. Apaga con el botón. Confirma en la consola de RunPod que el pod **ya no existe** y que `systemctl list-timers vera-modelos-*`
   sigue activo.
7. Deja el pod encendido sin tocarlo para comprobar el apagado automático (5 minutos si bajaste el límite) y que el guardián lo termina.
8. Devuelve `SESION_MAX_H=4` e `INACTIVIDAD_MIN=10`.

**Qué mirar si algo falla:** código 14 o 15 (otro host), 13 (VRAM), 2 (configuración de tokens). Si falló un servicio, el campo `detalle`
de la línea `fallo` trae las últimas 20 líneas de su log. Los servidores no registran sus tokens, pero revisa ese texto antes de pegarlo
en un chat.

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
- `/readyz` de la API no cambia (el CD y los despliegues siguen su camino normal). `GET /readyz/modelos` reportará los tres como caídos.
- Con `modelos-control` caído, el puente recibe rechazo de conexión y el resultado es el mismo.

Si el producto no tolera esos 503, la salida es de producto, no de infraestructura: un proveedor compuesto en Go que caiga a
`simulado` (decisión 10 del diseño aprobado, no se hace aquí).

**Regla operativa:** un despliegue del CD recrea `centinela-api` y deja huérfano el puente. `vera-modelos-puente.timer` lo repara en
2 minutos como máximo, o ejecútalo a mano: `sudo bash /opt/vera-modelos/vm/puente-modelos.sh reparar`.

## 9. Riesgos conocidos

1. **Puente huérfano tras un despliegue** (verificado a mano con docker local). Mitigado con `verificar`, `reparar` y el temporizador, pero
   hay hasta 2 minutos de rechazo de conexión. No se probó en la VM.
2. **La IP del gateway puede cambiar** si la red `centinela` se vuelve a crear. `modelos-control` fallaría al ligar el puerto. Revisa
   `puente-modelos.sh gateway` contra `VERA_MODELOS_ESCUCHA`.
3. **El puerto de Ollama del gateway no tiene autenticación y lo alcanza cualquier contenedor de la VM** (Postgres, Redis, simuladores,
   nginx). Aceptable en una VM de un solo propósito [I]. Mitigación posible, no hecha: reglas en `DOCKER-USER`.
4. **`MemoryDenyWriteExecute`, `SystemCallFilter=@system-service` y `RestrictAddressFamilies` sin `AF_NETLINK` pueden romper el binario de M1**
   (por ejemplo si invoca `ssh` como proceso hijo o enumera interfaces de red). Si el servicio no arranca, se relajan de a una y se
   documenta cuál fue **[N]**.
5. **Clave de RunPod con acceso total a la cuenta** (no hay claves restringidas para pods, diseño aprobado [V]). Mitigación: cuenta con
   prepago bajo. Está en el entorno del proceso `modelos-control`, legible solo por root y por el usuario `vera-modelos`.
6. **Confianza en el primer uso del host SSH del pod.** El `known_hosts` vive en la carpeta de estado y se acepta la huella la primera vez.
   Un atacante en la ruta durante esos segundos podría suplantar al pod. Riesgo aceptado en la demo (decisión 8 del diseño).
7. **Cada arranque descarga todo** (sin volumen de red): GGUF de unos 5 GB desde Hugging Face, Ollama desde GitHub u `ollama.com`, wheels
   desde PyPI, pesos de Kokoro desde GitHub. El GGUF y los pesos de Kokoro se verifican con sha256. Los pesos de Whisper los baja
   `huggingface_hub` con sus propias comprobaciones, **no con un sha256 nuestro**. El tarball del código por commit tampoco tiene hash propio.
   El instalador de Ollama (`install.sh`) se ejecuta como root, igual que en la tesis.
8. **Python del pod.** Los pines de `requirements*.txt` se verificaron contra wheels cp312. Con una plantilla que traiga otra versión,
   `pip --only-binary=:all:` puede fallar. El script prefiere `python3.12`, luego `3.11`, luego `python3`.
9. **`OLLAMA_KEEP_ALIVE=24h` del pod no manda**: cada petición del backend lleva `keep_alive: 30m` (`internal/copilot/ollama/proveedor.go`) [V]
   y lo sobrescribe. Con el apagado a los 10 minutos de inactividad no importa.
10. **Una sola GPU compartida.** Whisper, Kokoro y Ollama comparten los 24 GB. Whisper y Kokoro reservan su VRAM antes de que arranque
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
- **Que exista `pgrep`, `pkill`, `zstd` y `curl` en la plantilla** (la tesis exigía los tres primeros y usaba la plantilla `runpod-torch-v280`,
  no se comprobó que siga existiendo).
- **Que `/workspace` exista y sea escribible** en la plantilla elegida (la tesis lo usaba).
- **Que `openssl` y `ssh-keygen` estén en la VM**, la versión de `docker compose` de la VM y que el glob `compose*.yaml` ordene `z-modelos` al final.
- **El puente en la VM real:** solo se probó con docker local.
- **Que `systemd-analyze verify` sea limpio con el binario de M1 presente**, y que el sandbox no lo rompa.
- **Que `modelos-control --seco` exista con esa sintaxis** y que no imprima secretos (es de M1).
- **Que `PUBLIC_KEY` entregue la llave pública en la plantilla de RunPod** y que `ssh -L` funcione sobre el TCP directo del pod (el diseño ya lo
  marcaba [N]).
- **El cobro exacto del disco del contenedor** y la disponibilidad de stock Secure para la A5000.
- **Un `/readyz` unificado en el pod:** no se hizo a propósito. Exigiría un cuarto proceso y un cuarto puerto en el túnel.
  `salud-modelos.sh --json` por ssh y las sondas de `modelos-control` cubren lo mismo.

## 11. Archivos de esta pieza

| Archivo | Para qué |
|---------|----------|
| `infra/modelos-runpod/pod/arrancar-modelos.sh` | Dentro del pod: prepara y arranca los tres modelos. Idempotente |
| `infra/modelos-runpod/pod/salud-modelos.sh` | Dentro del pod: sonda los tres servicios con sus tokens |
| `infra/modelos-runpod/pod/modelos-pod_test.sh` | 28 pruebas de los dos anteriores, sin pod |
| `infra/modelos-runpod/vm/generar-secretos.sh` | En la VM: usuario, tokens, llave SSH y archivos 0600 |
| `infra/modelos-runpod/vm/puente-modelos.sh` | En la VM: puente hacia la red de la API (instalar, aplicar, verificar, reparar, desinstalar) |
| `infra/modelos-runpod/vm/*.plantilla` | Plantillas del override de compose y de la configuración `stream` |
| `infra/modelos-runpod/vm/*_test.sh` | Pruebas de los dos scripts de la VM |
| `infra/modelos-runpod/systemd/` | `vera-modelos-control.service`, `-guardian.service` y `.timer`, `-puente.service` y `.timer` |
| `infra/modelos-runpod/.env.example` | Todas las variables, con valores de ejemplo no secretos |

## 12. Divergencias con el diseño aprobado

| Diseño aprobado | Aquí | Motivo |
|-----------------|------|--------|
| Sidecar docker con el cliente `ssh` abre el túnel | Binario Go `modelos-control` como servicio del host abre el túnel | Decisión de Christian del 15 de noviembre. **Obliga al puente de la sección 0**, porque la API no ve el loopback del host |
| Tokens al pod como variables de entorno al crearlo | Archivo 0600 entregado por ssh en cada arranque | El pod ya existe cuando se ejecuta el script. Sin secretos en la API de RunPod |
| Kokoro en CPU | Kokoro fp32 en CUDA con compuerta de proveedor | Decisión de Christian. Exige CUDA 13.0 en el driver y la rama `feat/kokoro-gpu` |
| Política (b): conmutar a `simulado` entre ventanas | Política (a): `local` permanente, 503 con el pod apagado | Decisión de Christian ("producción siempre usa RunPod, nunca simulado") |
| Workflows de GitHub para encender y apagar (con cron cada 15 min) | Botón y temporizador systemd en la VM cada 5 min | Decisión de Christian. No hay secretos de RunPod en GitHub |
| Acceso a la VM por llave de CI con comando forzado | Sin llave nueva de CI: instalación manual por ssh con aprobación | Alcance de este encargo. No se toca `authorized_keys` |
| `arrancar-pod.sh` en `infra/modelos/runpod/` | `infra/modelos-runpod/pod/arrancar-modelos.sh` | Propiedad de archivos del encargo |
| Puente: contenedor con `ssh -L` en la red de la API | Puente: contenedor nginx `stream` que reenvía al gateway, y el `ssh -L` lo abre `modelos-control` en el host | Consecuencia del binario en el host. Misma fragilidad ante la recreación de la API, ahora vigilada por un temporizador |

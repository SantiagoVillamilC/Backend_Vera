# Producción: modelos de Vera en RunPod (estado y operación)

Última actualización: 16 de noviembre de 2026 (incluye las pruebas con el APK, los límites ampliados y dos incidentes). Marcas: [V] medido o verificado, [I] inferido. Documento vivo para humanos y agentes. No contiene secretos: solo dice DÓNDE viven. El procedimiento de instalación paso a paso está en `docs/despliegue-modelos-runpod-vm.md` y el diseño aprobado en `docs/despliegue-modelos-runpod.md`.

## 1. Qué hay en producción hoy

```
App (APK) → https://veraapp.tech → API (contenedor centinela-api, red docker "centinela")
                                       │  VERA_PROVEEDOR=local
                                       ▼ http://172.18.0.1:{11434,8766,8767}  (gateway de la red centinela)
                         modelos-control (binario en el HOST de la VM, usuario vera-modelos)
                                       │ túnel SSH (ssh -L, llave propia)
                                       ▼
                    Pod de RunPod (RTX A5000, Secure): Ollama (Gemma 4 E4B) + Whisper small + Kokoro
```

- Producción y demo son la MISMA instancia: la VM de Azure `centinela-vm` (grupo `centinela-rg`, Debian 13, 2 vCPU, 3,9 GB de RAM). `staging` y `main` despliegan a esa misma VM.
- La API corre siempre con `VERA_PROVEEDOR=local`. Con el pod apagado, los proxys de `modelos-control` responden 503 `{"error":"modelos_apagados"}` y la API devuelve 503 al cliente. NO hay respuestas simuladas en producción. Cada intento fallido gasta una cuota de turno. Valores por defecto: 30 de texto y 10 de voz por hora por usuario. Hoy producción corre con límites ampliados para pruebas y demo (ver sección 3).
- El pod NO está encendido de forma permanente: se enciende bajo demanda y se apaga solo a los 10 minutos sin peticiones. Hoy lo enciende un operador (script o `curl` al controlador). El botón de la app es fase 2.
- El APK actual (paquete `com.xisfo.vera`) solo usa Gemma (con function calling) y Whisper. La respuesta hablada por Kokoro requiere la rama `feat/voz-respuesta-kokoro` del repo de la app (`SantiagoVillamilC/Vera`, ver sección 9).

## 2. Acceso y archivos en la VM

- Acceso: `ssh centinela` (alias, puerto 22022, usuario `centinela`, `sudo` sin contraseña). También `az vm run-command` con `az` (suscripción Azure for Students). `rsync`, `jq`, `go` y `nginx` NO están en la VM.
- Red docker `centinela`: gateway `172.18.0.1`, subred `172.18.0.0/16`. Los clientes Go de la API aceptan esa IP privada solo si `VERA_MODELOS_HOST_PERMITIDO=172.18.0.1`.

| Ruta en la VM | Qué es | Dueño y modo |
|---|---|---|
| `/usr/local/bin/modelos-control` | binario estático (compilado en la máquina de desarrollo con `make build` y copiado por scp) | root 0755 |
| `/opt/vera-modelos/` | scripts de la VM y del pod (`pod/`, `vm/`, `systemd/`), copiados de `infra/modelos-runpod/` | root |
| `/etc/vera-modelos/modelos-control.env` | configuración y `RUNPOD_API_KEY` | root 0600 |
| `/etc/vera-modelos/tokens.env` | `VERA_STT_TOKEN` y `VERA_VOZ_TOKEN` (estáticos, 64 hex) | vera-modelos 0600 |
| `/etc/vera-modelos/control.token` | token del puerto de control `127.0.0.1:8790` | vera-modelos 0600 |
| `/etc/vera-modelos/ssh/tunel_ed25519` (y `.pub`) | llave SSH del túnel VM hacia pod | vera-modelos 0600 |
| `/opt/centinela/api/modelos.env` | los mismos dos tokens, para el contenedor de la API | root 0600 |
| `/opt/centinela/infra-servidor/compose.z-modelos.yaml` | override de compose: fija `VERA_PROVEEDOR=local`, URLs y tokens de la API. El CD une TODOS los `compose*.yaml`, así que sobrevive a los despliegues | root |
| `/opt/centinela/infra-servidor/compose.z-limites-vera.yaml` | override de compose con los límites de uso ampliados para pruebas y demo (sección 3). El CD lo une por el glob `compose*.yaml` | root |
| `/var/lib/vera-modelos/` | estado persistente de `modelos-control` y known_hosts por pod | vera-modelos |
| `/etc/systemd/system/vera-modelos-control.service`, `vera-modelos-guardian.service` y `.timer` | servicio principal y guardián cada 5 minutos (`modelos-control reconcile`) | root |

Variables de la API en producción (nombres, sin valores): `VERA_PROVEEDOR=local`, `VERA_MODELO=gemma4-e4b-local`, `VERA_OLLAMA_URL`, `VERA_STT_URL`, `VERA_VOZ_URL` (todas `http://172.18.0.1:<puerto>`), `VERA_MODELOS_HOST_PERMITIDO`, `VERA_STT_TOKEN`, `VERA_VOZ_TOKEN`.

## 3. Operación diaria

Controlador (en la VM, solo loopback, token Bearer de `/etc/vera-modelos/control.token`):
- `POST /encender` (asíncrono, idempotente), `POST /apagar`, `GET /estado`. Estados: `apagado`, `encendiendo`, `listo`, `apagando`, `error`, `sin_existencias`.
- Ejemplo (el token se lee dentro de la VM, nunca se trae): `ssh centinela 'sudo bash -c "curl -s -H \"Authorization: Bearer \$(cat /etc/vera-modelos/control.token)\" http://127.0.0.1:8790/estado"'`.
- Scripts locales de operación (en `~/.local/bin` de la máquina de Christian): `vera-modelos-encender` y `vera-modelos-apagar`. El segundo verifica de forma independiente contra la API de RunPod que no queden pods ni gasto por hora.
- Logs: `ssh centinela 'sudo journalctl -u vera-modelos-control -n 50 --no-pager'`.
- Salud de la API con los modelos: `docker exec centinela-api wget -qO- http://127.0.0.1:8080/readyz/modelos` (200 con el pod listo). `https://veraapp.tech/healthz` solo dice que la API vive.
- Límites del controlador: A5000 únicamente (`MODELOS_GPUS`), tope de tarifa 0,35 USD/h, inactividad 10 minutos, sesión máxima 4 h. Un solo pod llamado `vera-modelos`. Nunca cambia de GPU ni de nube por su cuenta.

Rollback de la API a simulado: `ssh centinela 'sudo bash /opt/vera-modelos/vm/red-api.sh desinstalar'`. Rollback completo: ver `docs/despliegue-modelos-runpod-vm.md`.

### Límites de uso ampliados (solo pruebas y demo)

[V] `compose.z-limites-vera.yaml` fija estas variables de la API. Los valores por defecto están entre paréntesis:

| Variable | Valor ampliado | Por defecto |
|---|---|---|
| `VERA_LIMITE_TEXTO_HORA` | 1000 | 30 |
| `VERA_LIMITE_TEXTO_DIA` | 10000 | 150 |
| `VERA_LIMITE_VOZ_HORA` | 500 | 10 |
| `VERA_TOKENS_DIARIOS` | 50000000 | 100000 |

- En producción real estos valores deben volver a algo razonable: ampliados, no frenan el gasto de un usuario ni de un bucle.
- Revertir: borrar el archivo en la VM y recrear la API con la lista completa de compose.
- Recrear la API (el orden y la lista completa importan, ver sección 7): `cd /opt/centinela/infra-servidor && sudo docker compose -f compose.api.yaml -f compose.certbot.yaml -f compose.postgres.yaml -f compose.redis.yaml -f compose.yaml -f compose.z-limites-vera.yaml -f compose.z-modelos.yaml up -d --force-recreate api`.
- Para dimensionar cuotas [V]: cada turno con herramientas manda unos 2900 a 4100 tokens de entrada, y un turno de voz cuesta 2 o 3 de la cuota de voz.

## 4. RunPod: hechos verificados

- Clave de API: NO está en este repo. En la máquina de desarrollo vive en el almacén `pass` separado (`PASSWORD_STORE_DIR=$HOME/.password-crush`, entrada `api/runpod`) y en la VM solo en `/etc/vera-modelos/modelos-control.env`. Nunca se imprime ni va por argv.
- API REST v2 (`https://api.runpod.io/v2`): la v1 (`rest.runpod.io/v1`) se retira el 15 de noviembre de 2026. En v2 la IP y el puerto ssh vienen en `ssh.direct.{host,port,username}` solo en `GET /v2/pods/{id}`, la tarifa en `cost`, y `CreatePodRequest` rechaza con 422 cualquier clave extra. Volúmenes: `GET`/`POST /v2/network-volumes`.
- La clave que RunPod inyecta DENTRO del pod da 403: un pod no puede terminarse a sí mismo. La terminación se hace siempre desde la VM o la máquina de desarrollo.
- RunPod no admite Docker ni Compose dentro de un pod: los modelos corren como procesos nativos.
- La disponibilidad cambia entre consultas. Los precios listados no garantizan existencias: el 15 de noviembre la A5000 y la A4000 aparecían listadas y `crear_pod` las rechazaba. Revalidar justo antes de crear. A5000 Secure a 0,27 USD/h (15 nov), 4090 Secure a 0,89 USD/h (fuera de los planes por decisión de Christian).
- Un pod recién creado tarda en aceptar ssh. El arranque en frío completo (creación, instalación, descargas, carga de modelos) midió 7,9 minutos el 16 de noviembre, una sola medición.
- Primera sesión real (16 nov): pod listo a los 8,1 minutos, 0,044 USD. Gemma con function calling llamó a la herramienta correcta en 9,9 s, Whisper transcribió audio en español y Kokoro devolvió un WAV de 24 kHz.
- Prueba con el APK (cuenta de Andrés Vargas, A5000 en Secure) [V]: texto «¿cuánto tengo disponible?» en 6,6 s. Turno de voz con function calling en 11,6 a 19,7 s, con 2 o 3 llamadas a Gemma. `/v1/vera/sintetizar` en 0,5 a 2,6 s, con WAV de 24 kHz. La cuota de voz por defecto (10 por hora) se agotó durante las pruebas, de ahí los límites ampliados de la sección 3.

## 5. Volúmenes de red (caché de pesos y paquetes)

Existen 4 volúmenes de 30 GB creados el 16 de noviembre, uno por cada centro de datos que lista la A5000 con soporte de almacenamiento. Un volumen solo se monta en pods de SU centro, y se monta en `/workspace`.

| Centro | Tipo | Id | Nombre |
|---|---|---|---|
| US-IL-1 | STANDARD | `t35aiij709` | `vera-modelos-cache-us-il-1` |
| US-CA-2 | HIGH_PERFORMANCE | `3ztqv80lvj` | `vera-modelos-cache-us-ca-2` |
| CA-MTL-1 | HIGH_PERFORMANCE | `a96l2w9mbs` | `vera-modelos-cache-ca-mtl-1` |
| EU-SE-1 | STANDARD | `5fr195v982` | `vera-modelos-cache-eu-se-1` |

- Cuestan mientras existan, haya pod o no. Precio por GB por verificar (cotización vieja: unos 0,07 USD/GB al mes). Cuando se sepa qué centro rinde mejor, borrar los demás con `DELETE /v2/network-volumes/{id}`. `modelos-control` NUNCA crea ni borra volúmenes.
- Soporte en `modelos-control` y en los scripts del pod (configuración `MODELOS_VOLUMENES=CENTRO=ID,...` y caché en `/workspace/vera-cache`): el PR #94 está fusionado en `dev` (`23aeda0`) [V]. Faltan las promociones `dev` a `staging` y `staging` a `main`.
- El binario `modelos-control` en la VM sigue siendo la versión SIN volúmenes (sha256 `07aedf1a073394262ecde14562013df34af7fdf39403b4cb0da4f54469050939`) [V]. Hasta actualizarlo, el pod arranca SIN volumen.

## 6. Costos y guardas

- Guardas de gasto en capas: tope de tarifa del controlador, un solo pod, apagado por inactividad (10 min), sesión máxima (4 h), guardián de systemd cada 5 minutos que termina pods huérfanos o vencidos, verificación independiente en `vera-modelos-apagar` (cero pods en cualquier estado, `currentSpendPerHr` en 0, volúmenes solo los esperados).
- Costo de una sesión corta: unos 0,05 a 0,15 USD. Costo mensual de referencia con 4 volúmenes: unos 8 a 9 USD (por verificar).
- Si el saldo de RunPod llega a cero, los volúmenes pueden borrarse. Mantener saldo.

## 7. Trampas conocidas

- NO recrear la API con un compose que no incluya `compose.z-modelos.yaml`: volvería a `simulado` en silencio.
- NO recrear la API sin `compose.z-limites-vera.yaml` si se quieren los límites ampliados: las cuotas vuelven en silencio a 30 de texto y 10 de voz por hora y la demo falla por cuota.
- Tras `demo-limpiar` y `demo-inyectar` falta la cuenta `retenido` del usuario demo y confirmar una transferencia da 503 (sección 8).
- Las promociones a `staging` y `main` las debe abrir una persona: un hook del entorno bloquea `pr_create` en repos públicos para los agentes.
- NO promover un cambio de `staging` o `main` esperando que no afecte a producción: ambas ramas despliegan a la VM de producción.
- `runpodctl exec` pierde la salida de scripts cortos y de más de unos 60 KB: usar ssh y rsync.
- La API cuenta cada intento fallido contra la cuota del usuario: encender el pod ANTES de probar con la app (unos 8 minutos en frío).
- Un contenedor de la API que se recrea pierde su espacio de red: los proxys escuchan en el gateway del puente de Docker, no en un namespace compartido, por eso no hay que reinstalar nada al recrear la API.
- Los commits y PRs de este trabajo deben ir SIN `Co-Authored-By` (la atribución está desactivada por decisión de Christian).

## 8. Diagnóstico de fallos conocidos

### 503 al confirmar una transferencia de la demo (cuenta `retenido`)

- Síntoma [V]: el backend responde 503 `AssistantUnavailable` (la app muestra «Vera no está disponible ahora») al confirmar una transferencia del usuario demo (Andrés Vargas).
- Causa [V]: `nucleo.posting` viola la llave foránea hacia `nucleo.cuenta` porque no existe la cuenta `pasivo:usuario:<uuid>:retenido`. La siembra solo creó `disponible`.
- Arreglo aplicado a mano [V]: en la base `vera_semilla_demo` del contenedor `postgres-demo` (usuario de base de datos `dueno`) se insertó una fila en `nucleo.cuenta` con `naturaleza=pasivo`, `clase=usuario`, `titular_id=<uuid>` y `moneda=COP`.
- Causa raíz en el script de siembra: en revisión en la rama `fix/semilla-demo-cuenta-retenido`. Hasta que se fusione, repetir la inserción después de cada `demo-limpiar` y `demo-inyectar`.
- Cómo diagnosticar un 503 sin detalle en los logs de la API [V]: `sudo docker logs postgres-demo` en la hora exacta. El error de la base de datos sí aparece ahí.

### 503 intermitente por «respuesta del proveedor IA inválida o incompleta»

- Observado una vez [V]: un turno de voz dio 503 (Gemma generó solo 28 tokens en 1,8 s) y el mismo audio reintentado funcionó. Causa de fondo sin determinar [I]: parece una respuesta truncada o vacía de Gemma, no un fallo de red.
- Las condiciones que producen ese error están en `internal/copilot/ollama/traduccion.go` (líneas 244 a 269): JSON inválido, `done` falso, `done_reason` distinto de `stop`, nombre de función vacío, argumentos inválidos, o ni texto ni llamada.
- Gemma devuelve además un campo `thinking` de hasta unos 175 tokens por respuesta [V].
- Capturar la respuesta cruda, en la VM: `sudo tcpdump -i br-15df1ff0de74 -A -s 0 -l "tcp port 11434"`. Borrar la captura después: contiene datos de la demo.

### Teléfono por adb inalámbrico

- El puerto de conexión cambia cada vez que se reactiva la depuración inalámbrica [V]. Emparejar con `adb pair IP:puertoDeEmparejamiento código` y luego `adb connect IP:puertoDeConexión`. El puerto de emparejamiento rechaza `adb connect`.
- Con Tailscale se puede usar la IP `100.x` del teléfono [V]. La app instalada es `com.xisfo.vera`.

## 9. Pendientes (a 16 de noviembre de 2026)

- Promover #94 (volúmenes) de `dev` a `staging` y de `staging` a `main` (lo abre una persona), actualizar el binario `modelos-control` de la VM y llenar la caché con un primer arranque en un centro con existencias. Los scripts locales `vera-modelos-encender` y `vera-modelos-apagar` (en `~/.local/bin/` de la máquina de desarrollo de Christian) se probaron con dobles, no contra RunPod real.
- Revertir los límites ampliados (sección 3) antes de un uso real.
- Fusionar `fix/semilla-demo-cuenta-retenido` para que la siembra cree la cuenta `retenido` (sección 8).
- Investigar el 503 intermitente de Gemma (sección 8): capturar la respuesta cruda cuando se repita.
- Respuesta hablada con Kokoro en la app: rama `feat/voz-respuesta-kokoro` del repo `SantiagoVillamilC/Vera` (5 commits sobre `dev`, publicada en origin, sin PR todavía). La app llama a `/v1/vera/sintetizar`, que rechaza textos de más de 400 caracteres, y cae a la voz del teléfono si falla. `main` de la app está exactamente en la release `v1.1.0`. Las ramas locales `demo/*` llevan un commit que OCULTA funciones en desarrollo borrando código de la UI: NO debe ir a `main`.
- Botón de encendido y apagado en la app, con un endpoint del backend que llame al controlador y permisos (solo cuentas administradoras deberían encender).
- Migrar los scripts de pod de la tesis (`modelos-gemma/carga/pod/`) a la API v2.
- Revalidar el volumen de red y el precio de cada GB, y decidir qué centros conservar.

## 10. Referencias

- PRs de Backend_Vera: #88 (Kokoro en GPU), #89 (clientes con IP privada), #90 (`modelos-control`), #91 (infraestructura), #92 (`dev` a `staging`) y #93 (`staging` a `main`). SHA de `dev` tras fusionar todo eso: `4897d09be9629cd65b91c6ee235eecc30c923a92`. Después, #94 (volúmenes de caché) fusionado en `dev`: `23aeda0`, pendiente de promoción.
- Código: `cmd/modelos-control`, `internal/modelos/...`, `internal/copilot/destinolocal`, `infra/modelos-runpod/`, `infra/voz-local/`.
- Demo: cuenta de Andrés Vargas (`+573205550142`) ya sembrada en producción. Detalle en el repositorio de la tesis, `proyecto-fin-de-grado/DEMO-PROD-ESTADO-2026-11-14.md`.

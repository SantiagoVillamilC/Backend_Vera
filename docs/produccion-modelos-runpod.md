# Producción: modelos de Vera en RunPod (estado y operación)

Última actualización: 16 de noviembre de 2026. Documento vivo para humanos y agentes. No contiene secretos: solo dice DÓNDE viven. El procedimiento de instalación paso a paso está en `docs/despliegue-modelos-runpod-vm.md` y el diseño aprobado en `docs/despliegue-modelos-runpod.md`.

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
- La API corre siempre con `VERA_PROVEEDOR=local`. Con el pod apagado, los proxys de `modelos-control` responden 503 `{"error":"modelos_apagados"}` y la API devuelve 503 al cliente. NO hay respuestas simuladas en producción. Cada intento fallido gasta una cuota de turno (30 de texto y 10 de voz por hora por usuario).
- El pod NO está encendido de forma permanente: se enciende bajo demanda y se apaga solo a los 10 minutos sin peticiones. Hoy lo enciende un operador (script o `curl` al controlador). El botón de la app es fase 2.
- El APK actual solo usa Gemma (con function calling) y Whisper. La respuesta hablada por Kokoro requiere la rama `feat/voz-respuesta-kokoro` del repo de la app (`SantiagoVillamilC/Vera`, ver sección 8).

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

## 4. RunPod: hechos verificados

- Clave de API: NO está en este repo. En la máquina de desarrollo vive en el almacén `pass` separado (`PASSWORD_STORE_DIR=$HOME/.password-crush`, entrada `api/runpod`) y en la VM solo en `/etc/vera-modelos/modelos-control.env`. Nunca se imprime ni va por argv.
- API REST v2 (`https://api.runpod.io/v2`): la v1 (`rest.runpod.io/v1`) se retira el 15 de noviembre de 2026. En v2 la IP y el puerto ssh vienen en `ssh.direct.{host,port,username}` solo en `GET /v2/pods/{id}`, la tarifa en `cost`, y `CreatePodRequest` rechaza con 422 cualquier clave extra. Volúmenes: `GET`/`POST /v2/network-volumes`.
- La clave que RunPod inyecta DENTRO del pod da 403: un pod no puede terminarse a sí mismo. La terminación se hace siempre desde la VM o la máquina de desarrollo.
- RunPod no admite Docker ni Compose dentro de un pod: los modelos corren como procesos nativos.
- La disponibilidad cambia entre consultas. Los precios listados no garantizan existencias: el 15 de noviembre la A5000 y la A4000 aparecían listadas y `crear_pod` las rechazaba. Revalidar justo antes de crear. A5000 Secure a 0,27 USD/h (15 nov), 4090 Secure a 0,89 USD/h (fuera de los planes por decisión de Christian).
- Un pod recién creado tarda en aceptar ssh. El arranque en frío completo (creación, instalación, descargas, carga de modelos) midió 7,9 minutos el 16 de noviembre, una sola medición.
- Primera sesión real (16 nov): pod listo a los 8,1 minutos, 0,044 USD. Gemma con function calling llamó a la herramienta correcta en 9,9 s, Whisper transcribió audio en español y Kokoro devolvió un WAV de 24 kHz.

## 5. Volúmenes de red (caché de pesos y paquetes)

Existen 4 volúmenes de 30 GB creados el 16 de noviembre, uno por cada centro de datos que lista la A5000 con soporte de almacenamiento. Un volumen solo se monta en pods de SU centro, y se monta en `/workspace`.

| Centro | Tipo | Id | Nombre |
|---|---|---|---|
| US-IL-1 | STANDARD | `t35aiij709` | `vera-modelos-cache-us-il-1` |
| US-CA-2 | HIGH_PERFORMANCE | `3ztqv80lvj` | `vera-modelos-cache-us-ca-2` |
| CA-MTL-1 | HIGH_PERFORMANCE | `a96l2w9mbs` | `vera-modelos-cache-ca-mtl-1` |
| EU-SE-1 | STANDARD | `5fr195v982` | `vera-modelos-cache-eu-se-1` |

- Cuestan mientras existan, haya pod o no. Precio por GB por verificar (cotización vieja: unos 0,07 USD/GB al mes). Cuando se sepa qué centro rinde mejor, borrar los demás con `DELETE /v2/network-volumes/{id}`. `modelos-control` NUNCA crea ni borra volúmenes.
- Soporte en `modelos-control` y en los scripts del pod: rama `feat/modelos-volumen-cache` (configuración `MODELOS_VOLUMENES=CENTRO=ID,...` y caché en `/workspace/vera-cache`). Hasta que esa rama llegue a `main` y el binario se actualice en la VM, el pod arranca SIN volumen.

## 6. Costos y guardas

- Guardas de gasto en capas: tope de tarifa del controlador, un solo pod, apagado por inactividad (10 min), sesión máxima (4 h), guardián de systemd cada 5 minutos que termina pods huérfanos o vencidos, verificación independiente en `vera-modelos-apagar` (cero pods en cualquier estado, `currentSpendPerHr` en 0, volúmenes solo los esperados).
- Costo de una sesión corta: unos 0,05 a 0,15 USD. Costo mensual de referencia con 4 volúmenes: unos 8 a 9 USD (por verificar).
- Si el saldo de RunPod llega a cero, los volúmenes pueden borrarse. Mantener saldo.

## 7. Trampas conocidas

- NO recrear la API con un compose que no incluya `compose.z-modelos.yaml`: volvería a `simulado` en silencio.
- NO promover un cambio de `staging` o `main` esperando que no afecte a producción: ambas ramas despliegan a la VM de producción.
- `runpodctl exec` pierde la salida de scripts cortos y de más de unos 60 KB: usar ssh y rsync.
- La API cuenta cada intento fallido contra la cuota del usuario: encender el pod ANTES de probar con la app (unos 8 minutos en frío).
- Un contenedor de la API que se recrea pierde su espacio de red: los proxys escuchan en el gateway del puente de Docker, no en un namespace compartido, por eso no hay que reinstalar nada al recrear la API.
- Los commits y PRs de este trabajo deben ir SIN `Co-Authored-By` (la atribución está desactivada por decisión de Christian).

## 8. Pendientes (a 16 de noviembre de 2026)

- Respuesta hablada con Kokoro en la app: rama `feat/voz-respuesta-kokoro` del repo `SantiagoVillamilC/Vera` (la app llama a `/v1/vera/sintetizar`, que rechaza textos de más de 400 caracteres, y cae a la voz del teléfono si falla).
- Soporte de volúmenes (sección 5) hasta producción y llenado de la caché con un primer arranque en un centro con existencias.
- Botón de encendido y apagado en la app, con un endpoint del backend que llame al controlador y permisos (solo cuentas administradoras deberían encender).
- Migrar los scripts de pod de la tesis (`modelos-gemma/carga/pod/`) a la API v2.
- Revalidar el volumen de red y el precio de cada GB, y decidir qué centros conservar.

## 9. Referencias

- PRs de Backend_Vera: #88 (Kokoro en GPU), #89 (clientes con IP privada), #90 (`modelos-control`), #91 (infraestructura), #92 (`dev` a `staging`) y #93 (`staging` a `main`). SHA de `dev` con todo fusionado: `4897d09be9629cd65b91c6ee235eecc30c923a92`.
- Código: `cmd/modelos-control`, `internal/modelos/...`, `internal/copilot/destinolocal`, `infra/modelos-runpod/`, `infra/voz-local/`.
- Demo: cuenta de Andrés Vargas (`+573205550142`) ya sembrada en producción. Detalle en el repositorio de la tesis, `proyecto-fin-de-grado/DEMO-PROD-ESTADO-2026-11-14.md`.

# Despliegue por digest, modelo fijado y vuelta atrás (R-05)

Este documento cubre dos despliegues. El de la API y los simuladores en la VM (`deploy-ci.sh`) y el de la
pila de modelos (`deploy-modelos.sh`), que se describe al final en la sección «Pila de modelos».

`infra/servidor/deploy-ci.sh` es el comando forzado de la llave de CI. Antes desplegaba por etiqueta
de rama (`:main`), no guardaba el entorno anterior y no tenía vuelta atrás. Ahora acepta un digest
inmutable y un tag de modelo, los valida antes de tocar nada, respalda el entorno y tiene rollback.

## Uso

```bash
# Despliegue normal por rama (lo que manda cd.yml)
deploy-ci.sh main

# Por digest inmutable y con el modelo del lockfile
deploy-ci.sh main --digest sha256:<64 hex> --modelo gemma-4-e2b-it-q4_k_m

# Vuelta atrás al último respaldo
deploy-ci.sh --rollback

# Prueba en seco sin docker
DEPLOY_SIMULAR=si DEPLOY_DIRECTORIO=/tmp/x deploy-ci.sh main --digest sha256:<64 hex>
```

Las variables de entorno equivalentes son `IMAGEN_DIGEST` y `MODELO_TAG`. La rama sigue siendo el
primer argumento y sigue validándose contra `dev`, `staging` y `main`. `--rollback` no la necesita.

## Qué valida y en qué orden

1. Rama permitida (`dev`, `staging`, `main`), código 64 si no.
2. Formato del digest: `sha256:` y 64 hexadecimales, código 65 si no. Sin simular, además
   `docker manifest inspect <registro>@<digest>`: un digest inexistente aborta con código 66 **antes**
   de tocar el entorno vigente.
3. Tag de modelo contra `modelos/lock.json`: solo se aceptan los nombres del lockfile, código 68 si
   no. Sin lockfile, código 67.
4. Solo entonces respalda y reescribe `IMAGEN_API` (`registro@digest` o `registro:rama`) y
   `MODELO_TAG` en los dos `.env`.

## Respaldo y rollback

Cada despliegue guarda una copia fechada (`AAAA-MM-DDTHH-MM-SSZ`) con `api.env` y
`simuladores.env` en `respaldos/`. `--rollback` restaura la copia más reciente, levanta la API con la
imagen anterior, espera `healthy` y corre el humo. Sin respaldos aborta con código 69.

Límites declarados:

- Las migraciones **no** se revierten: volver a una imagen anterior con la base ya migrada es una
  decisión consciente y el rollback solo restaura imagen y modelo. Si la migración incompatible ya
  corrió, la vuelta atrás exige restaurar la base por separado.
- El GGUF del modelo vive en el volume de pesos: volver a un tag anterior no lo redescarga si el
  archivo sigue ahí. Si se borró, el primer arranque lo baja de nuevo.
- El humo es `GET /readyz` (configurable con `DEPLOY_SMOKE_URL`). No sustituye una prueba funcional.

## Códigos de salida

| Código | Significado |
| --- | --- |
| 0 | Despliegue o rollback terminó, healthy y con humo OK |
| 64 | Rama o argumento no permitido |
| 65 | Digest con formato inválido |
| 66 | Digest inexistente en el registro |
| 67 | Falta el lockfile de modelos |
| 68 | Tag de modelo que no está en el lockfile |
| 69 | Rollback sin respaldos |

## Prueba en seco

```bash
bash -n infra/servidor/deploy-ci.sh
bash infra/servidor/deploy-ci_test.sh
```

`deploy-ci_test.sh` usa un directorio temporal, un lockfile mínimo y `DEPLOY_SIMULAR=si`. Cubre
digest y modelo válidos con respaldo, digest inválido sin tocar el entorno, modelo inválido, rollback
con respaldo que restaura imagen y modelo, rollback sin respaldo y rama inválida. No se corrió
shellcheck en esa VPS porque no está instalado, y la sintaxis se comprobó con `bash -n`.

## Pila de modelos

`deploy-ci.sh` solo valida el digest de la imagen de la API. La pila de modelos (`vera-llm`, `vera-voz` y
`vera-kokoro`) tiene su propio camino, en tres piezas.

| Pieza | Archivo | Qué hace |
| --- | --- | --- |
| Publicación | `.github/workflows/cd-modelos.yml` | Construye y publica las tres imágenes en GHCR y emite el digest de cada una |
| Declaración | `infra/modelos/imagenes.json` | Digests que se van a desplegar, versionados y promovidos por PR |
| Despliegue | `infra/modelos/deploy-modelos.sh` | Valida, respalda, despliega por digest y vuelve atrás si falla un healthcheck |

Es un script aparte y no una extensión de `deploy-ci.sh` por tres razones. La pila corre en el pod con GPU
y no en la VM de CI. La llave SSH de CI recibe únicamente un nombre de rama como comando forzado, así que
no puede transportar digests. Y los respaldos de modelos van en su propia carpeta, porque si cayeran en
`respaldos/` el siguiente `deploy-ci.sh --rollback` tomaría el más reciente, no encontraría `api.env` y
no restauraría nada.

### Publicación: `cd-modelos.yml`

- Se dispara por push a `main` o `staging` cuando cambia algo que entra en el build de una imagen
  (`infra/modelos/vera-llm/**`, `vera-voz/**`, `vera-kokoro/**`, `infra/voz-local/{descargar,servidor,requirements}`,
  `infra/stt-local/{servidor,requirements}` o el propio workflow) y por `workflow_dispatch`.
- Excluye `Dockerfile.cuda`. La variante CUDA de `vera-voz` **no se publica** y sigue sin construir ni
  ejecutar.
- Tampoco dispara con cambios en `imagenes.json`, en el script de despliegue ni en los compose. Si registrar
  un digest en `imagenes.json` reconstruyera las imágenes, saldría un digest distinto al declarado y el
  ciclo no cerraría.
- Tres jobs separados, uno por imagen, para que cada uno exponga su digest como salida. Un job final,
  `resumen`, valida que los tres sean `sha256:<64 hex>`, los imprime en el resumen de la corrida como
  tabla y como JSON, y los expone en la salida `digests`.
- Los tres jobs de build usan `GITHUB_TOKEN` con `packages: write` (heredado del nivel del workflow). El job
  `resumen` declara su propio `permissions: contents: read`, sin `packages: write`, porque solo lee salidas
  y escribe el resumen. Mismas actions fijadas por SHA que `cd.yml` y el nombre de
  imagen en minúsculas (`ghcr.io/santiagovillamilc/backend_vera/<imagen>`), porque GHCR rechaza mayúsculas.
- Etiquetas: la de rama y `sha-<commit>`. Se despliega por digest, no por etiqueta.
- **Sin pesos** en `vera-llm` ni en `vera-voz`: viven en el volume `pesos`. `vera-kokoro` es la excepción
  por diseño: lleva dentro unos 0,14 GB de pesos, descargados en el build con SHA256 y tamaño verificados
  por `infra/voz-local/descargar.py`.

Riesgo de disco declarado: la imagen local de `vera-llm` pesa unos 8,4 GB
(`docs/mediciones-locales-2026-11-13.md`) y construirla y subirla necesita varias veces ese espacio. No hay
en el repositorio una medición del disco libre de `ubuntu-latest`. El job de `vera-llm` borra las
herramientas preinstaladas que el build no usa (`dotnet`, Android, GHC, CodeQL y Boost), poda las imágenes
de Docker del runner e imprime `df -h /` antes y después del build, y tiene un tope de 60 minutos. Sin
caché de Actions, porque capas de ese tamaño agotarían la cuota. Si el job muere por falta de disco, el
margen real queda en ese log. Hasta la primera corrida real este paso está sin probar.

### Declaración: `imagenes.json`

Contiene el registro y un `digest` por imagen. Mientras un digest sea `null`, `deploy-modelos.sh` se
niega a desplegar (código 70). Es el «declarado» contra el que se valida: el digest sale de la corrida de
`cd-modelos.yml`, se registra aquí y se promueve por PR como cualquier otro cambio. No se añadieron campos a
`modelos/lock.json` porque `cmd/verificar-modelos` lo lee con una estructura fija.

### Despliegue: `deploy-modelos.sh`

```bash
# Despliega los digests declarados en imagenes.json
WHISPER_TOKEN=... KOKORO_TOKEN=... infra/modelos/deploy-modelos.sh

# Además fija el modelo del razonador, validado contra modelos/lock.json
WHISPER_TOKEN=... KOKORO_TOKEN=... infra/modelos/deploy-modelos.sh --modelo gemma-4-e4b-it-q4_k_m

# En un pod con GPU: suma el override que le da la GPU a vera-llm (sin esto corre en CPU)
VERA_GPU=si WHISPER_TOKEN=... KOKORO_TOKEN=... infra/modelos/deploy-modelos.sh

# Vuelta atrás manual al respaldo más reciente
WHISPER_TOKEN=... KOKORO_TOKEN=... infra/modelos/deploy-modelos.sh --rollback

# Prueba en seco, sin docker
DEPLOY_SIMULAR=si infra/modelos/deploy-modelos.sh
```

Los tokens llegan por entorno, igual que con el compose base, y no se guardan en el estado. El estado
(digests vigentes, respaldos e `historial.log`) vive en `infra/modelos/estado/`, que está en `.gitignore`.
Se cambia con `DEPLOY_MODELOS_ESTADO`.

Para usar las imágenes publicadas, el script monta `infra/modelos/compose.registro.yaml` encima de
`compose.yaml`. Ese override cambia el campo `image` de cada servicio, tomándolo de variables que el
script escribe en `estado/modelos.env`, y quita el `build` con `build: !reset null` (exige Compose 2.24 o
posterior, y en esta máquina hay Compose 5.7). Así un pull fallido se ve como fallo y `up` nunca construye
en lugar de descargar. No toca el compose base: `docker compose up --build` en local sigue funcionando
igual. No se combina con `compose.cuda.yaml`. Como `yaml.safe_load` no conoce la etiqueta `!reset`, este
archivo se validó con `docker compose config`.

Orden de validaciones, todas antes de tocar el entorno vigente:

1. Cada digest declarado existe y tiene formato `sha256:<64 hex>`. Sin declarar, código 70. Formato
   inválido, código 65.
2. `docker manifest inspect <registro>/<imagen>@<digest>` para cada imagen. Como se consulta bajo el nombre
   de su propia imagen, un digest de otra imagen (por ejemplo el de la API pegado en `vera-llm`) no
   existe y aborta con código 66.
3. `--modelo` contra `modelos/lock.json`: código 67 sin lockfile y 68 si el nombre no está o no es un
   modelo de Ollama del razonador. `modelos/lock.json` no trae un campo de tipo y lista también artefactos
   de voz, así que el criterio es el prefijo `gemma-4-` (`gemma-4-e2b-*` y `gemma-4-e4b-*`). `whisper-small`
   y `kokoro-*` están en el lockfile pero se rechazan con 68, porque no son modelos de Ollama y dejarían a
   `vera-llm` sin nada que cargar. Si el razonador pasa a otra familia, hay que ampliar el prefijo
   en el script.
4. Presencia de `WHISPER_TOKEN` y `KOKORO_TOKEN`, código 71.

Después, el despliegue:

1. Guarda el entorno vigente en `estado/respaldos-modelos/<fecha>/modelos.env` y anota en
   `historial.log` el digest anterior y el nuevo.
2. Escribe `modelos.env` de forma atómica. Si no se pasa `--modelo`, conserva el modelo del entorno
   vigente.
3. `docker compose pull` y, por cada imagen, comprueba con `docker image inspect` que lo descargado es
   exactamente `registro/imagen@digest`. Si no coincide, código 75.
4. `docker compose up -d --no-build` y espera a que los tres servicios queden `healthy`, con el healthcheck
   que ya define `compose.yaml`. El tope por servicio es de 360 segundos (`DEPLOY_MODELOS_ESPERA`), que
   cubre el healthcheck más largo, el de `vera-voz` con 120 s de arranque y 18 reintentos de 10 s. Un
   servicio `unhealthy` corta la espera de inmediato.
5. Si falla el pull, la verificación del digest o un healthcheck, restaura el respaldo, lo mueve a
   `respaldos-modelos/aplicados/`, vuelve a levantar la pila anterior y sale con el código del fallo
   original (72, 75 o 77) para que la corrida quede marcada como fallida.
6. Si llega `INT`, `TERM` o `HUP` (Ctrl+C, `kill` o cierre de la terminal SSH) mientras `modelos.env` ya
   fue reemplazado y la verificación no terminó, un `trap` hace lo mismo que el paso 5: restaura el
   respaldo, lo mueve a `aplicados/`, vuelve a levantar la pila anterior y sale con 128 más el número de la
   señal (130, 143 o 129). El historial registra `despliegue-interrumpido` y `rollback-por-senal-ok` o
   `rollback-por-senal-fallo`. Si era el primer despliegue no hay respaldo: solo avisa y hay que revisar los
   contenedores a mano. Bash ejecuta el `trap` cuando termina el comando en curso, por ejemplo un
   `compose pull` o `up`, no a mitad de él. `SIGKILL` no se puede atrapar y no restaura nada. Una señal
   antes de reemplazar `modelos.env` o después de terminar bien no cambia el estado.

`--rollback` hace lo mismo a mano con el respaldo más reciente que aún no se aplicó. Como cada respaldo
usado pasa a `aplicados/`, rollbacks consecutivos retroceden un paso cada vez. Sin respaldos, código 69.

Códigos de salida nuevos, además de los de `deploy-ci.sh` que se repiten con el mismo significado
(64 a 69):

| Código | Significado |
| --- | --- |
| 70 | Falta `imagenes.json`, tiene otra forma o un digest sigue en `null` |
| 71 | Falta `WHISPER_TOKEN` o `KOKORO_TOKEN` |
| 72 | Un healthcheck falló y el entorno anterior se restauró y quedó sano |
| 73 | Falló el despliegue y el entorno anterior tampoco quedó sano, hay que revisar a mano |
| 74 | Falló el primer despliegue: no había entorno anterior al que volver |
| 75 | Lo descargado no coincide con el digest declarado |
| 77 | `docker compose pull` falló |

Límites declarados:

- Los pesos no se versionan con la imagen. El GGUF de `vera-llm` y la caché de Whisper viven en el volume
  `pesos`, así que volver a una imagen anterior no revierte los pesos. `--modelo` fija el nombre del modelo
  y el lockfile fija su huella, pero el archivo en el volume no se restaura.
- La vuelta atrás automática solo protege contra un healthcheck fallido. Un servicio que queda sano pero
  responde mal no se detecta. Por ejemplo, `VERA_VOZ_TOKEN` de la API distinto de `KOKORO_TOKEN` deja a
  `vera-kokoro` sano y devolviendo 401 a la API (ver `docs/pila-modelos-pod.md`).
- Con `network_mode: host`, `vera-llm` no levanta si hay un Ollama escuchando en el puerto 11434 del host.
  El script no lo comprueba.
- **Sin `VERA_GPU=si`, `vera-llm` corre en CPU.** El pod con GPU necesita `gpus: all` para `vera-llm`.
  Con `VERA_GPU=si` el script suma `infra/modelos/compose.registro-gpu.yaml`, un override pequeño que solo
  declara `gpus: all` para `vera-llm`. No toca `vera-voz` (su variante CUDA no se publica, así que
  Whisper por digest sigue en CPU) ni `vera-kokoro`. `VERA_GPU` solo acepta `si` o `no`, otro valor
  aborta con 64, y con `si` el archivo debe existir o aborta con 70. El rollback manual usa la misma
  variable: hay que repetirla al ejecutar `--rollback`, o la pila anterior sube sin GPU. Validado solo con
  `docker compose config` y tokens ficticios (con el override, `gpus` aparece solo en `vera-llm`, y sin él
  no aparece en ningún servicio) y con el docker simulado de la prueba. **No probado** en un pod real: falta
  confirmar el runtime NVIDIA y que Ollama use la GPU.
- **No mezclar este camino con `pod-demo.sh` en el mismo pod.** Ambos usan el proyecto compose `vera-modelos`
  (`name:` en `compose.yaml`), así que cada uno vería y pisaría los contenedores y el volume `pesos` del
  otro. `pod-demo.sh` construye con `compose.cuda.yaml` y `deploy-modelos.sh` descarga por digest con
  `compose.registro.yaml`. Se elige un camino por pod, y para cambiar de uno a otro se baja primero la pila
  con el camino anterior.
- Los `trap` de señales no cubren `SIGKILL` ni la caída del host a mitad del despliegue. En ese caso
  `modelos.env` puede quedar con el digest nuevo y el respaldo sin aplicar, y la recuperación es
  `deploy-modelos.sh --rollback`.
- El despliegue no está conectado a CI. `cd-modelos.yml` publica y emite digests, y el despliegue lo
  ejecuta el operador. Conectarlo exigiría una llave y un host para el pod que hoy no existen en los
  secretos del repositorio.
- Si el repositorio de GHCR es privado, el host que despliega necesita `docker login ghcr.io` con un
  token de lectura de paquetes.
- El digest de `cd-modelos.yml` es el que devuelve `docker/build-push-action`. Con la procedencia activada
  por defecto se espera que sea el del índice de manifiestos, que `docker pull` y `docker manifest inspect`
  aceptan. No se comprobó contra el registro real.

### Prueba en seco de la pila de modelos

```bash
bash -n infra/modelos/deploy-modelos.sh
bash infra/modelos/deploy-modelos_test.sh
```

`deploy-modelos_test.sh` sustituye `docker` por un script que registra sus llamadas y puede fallar el pull,
devolver un digest distinto o reportar `unhealthy` para un digest elegido. No usa red, no toca Ollama ni el
puerto 11434 y no construye ni levanta nada. Cubre las 22 comprobaciones originales: primer despliegue,
segundo despliegue con respaldo e historial del digest anterior, rollback manual y su agotamiento,
healthcheck fallido con rollback automático (72), rollback que tampoco queda sano (73), primer despliegue
fallido (74), digest descargado distinto (75), pull fallido (77), digest inexistente en el registro (66),
digest sin declarar (70) o con formato inválido (65), modelo fuera del lockfile (68), falta de token (71),
rollback sin respaldos (69), argumento inválido (64) y el modo `--simular`. Añade: rechazo de
`whisper-small` y `kokoro-v1.0-int8-onnx` como `--modelo` (68), señales `INT`, `TERM` y `HUP` con el entorno
ya reemplazado (restauran el respaldo y salen con 130, 143 y 129), señal en el primer despliegue sin
respaldo, y `VERA_GPU` (sin él no se usa el override, con `si` sí, y un valor inválido aborta con 64).

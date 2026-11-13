# Operación de la demo

El destino `demo` es la única superficie donde el motor de avisos corre de punta a punta. La
matriz por entorno está en `infra/servidor/banderas.tsv` y el resto de las banderas se explica en
`docs/configuracion-entornos.md`. Acá está solo lo que hay que saber para operar esa superficie.

## Qué enciende la demo

`DESTINO=demo` en `infra/servidor/aprovisionar.env` hace que `infra/servidor/pasos/60-api.sh`
emita las banderas de la columna `demo`: `CLASIFICADOR=tfidf`, `LEXICO=v2`, `AVISOS_ACTIVO=true`,
`AVISOS_HORA=06:00`, `AVISOS_CUPO=5`, `VERA_PROVEEDOR=simulado` y `METRICAS_ACTIVO=false`. Esas
mismas banderas están exportadas a archivo en `.env.demo.example` para arrancar la API a mano.

`AVISOS_ACTIVO=true` es propio de `demo`. `local` y `produccion` lo dejan apagado: `local` porque
`infra/local/demostrar.sh` enciende el emisor con sus propios valores y `cargas/inteligencia/entorno.sh`
lo mantiene apagado para congelar el corte de medición, y `produccion` hasta que haya un piloto con
personas.

## Cuándo corre el motor

Con la bandera encendida hay dos disparos:

- **Todos los días a las 06:00 de Colombia.** `internal/platform/jobs/jobs.go` agenda
  `AvisosWorker` en River a la hora de `AVISOS_HORA`, y `cmd/api/main.go` lo registra solo con
  `AVISOS_ACTIVO=true`.
- **Una vez al arrancar.** `cmd/api` llama `lanzarAvisosAlArrancar` después de levantar los jobs.
  La corrida sale en una goroutine, registra cuántos avisos emitió y no aborta el arranque si el
  emisor falla o entra en pánico.

La segunda corrida existe porque una demo que arranca a media mañana no puede esperar hasta las
06:00 del día siguiente para mostrar tarjetas. Es idempotente: la deduplicación por clave y
período del servicio (`internal/inteligencia/avisos_servicio.go`) descarta el aviso que ya está
guardado, así que reiniciar la API las veces que haga falta no duplica un aviso ni cambia el texto
de uno existente. Lo que sí puede subir entre reinicios es el conteo, y no por duplicación: la
elegibilidad la da `inteligencia.movimiento_enriquecido` (`UsuariosConHistorial`), así que el
enriquecimiento incremental habilita usuarios que la corrida anterior todavía no veía. Recién
sembrada la base, sin enriquecimiento, la corrida al arrancar no emite nada.

## Cómo comprobar que emitió

Sin bandeja de avisos en la app, el aviso solo se ve por la API. La corrida al arrancar sale en una
goroutine que se lanza antes de que el servidor HTTP escuche, así que no hay compuerta de
preparación: espera en el log la línea `avisos emitidos al arrancar` antes de consultar. Una vez que
termina, `GET /v1/inteligencia/avisos` devuelve las tarjetas sin esperar las 06:00. La lectura va
autenticada con un access real y `X-Real-IP`:

```bash
out/bin/api   # con DATABASE_URL, APP_DATABASE_URL, REDIS_URL y las banderas de .env.demo.example
```

La bandeja pagina el mes en curso cuando no recibe `mes` (`internal/inteligencia/handler.go`), y la
siembra de `semilla-inteligencia` cubre de enero a octubre de 2026 (diez meses), así que fuera de esa
ventana hay que pedir un mes sembrado, por ejemplo `?mes=2026-10`:

```bash
token="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))[0]["token"])' out/local/tokens.json)"
curl -s -H "Authorization: Bearer $token" -H 'X-Real-IP: 10.20.0.1' \
  'http://127.0.0.1:8080/v1/inteligencia/avisos?mes=2026-10'
```

En el log de la API aparece `avisos emitidos al arrancar` con el conteo. Si falla, aparece
`la emisión de avisos al arrancar falló` con el error, y la API sigue atendiendo. `GET
/v1/inteligencia/resumen` reporta `avisos_emitidos`, que es el conteo del emisor.

## Demo local completa

`make demo` ejecuta `infra/local/demostrar.sh`, que siembra la base desechable, enriquece, emite
avisos con `cmd/avisos` y arranca la API con `AVISOS_ACTIVO=true` y `METRICAS_ACTIVO=true`. Esa
corrida no depende del disparo de arranque porque emite antes, con `cmd/avisos`, y sirve para
revisar el escenario de `docs/escenario-demo.md`.

Para levantar solo la base y la API:

```bash
bash infra/local/levantar-base.sh --semilla
source cargas/inteligencia/entorno.sh
export AVISOS_ACTIVO=true
out/bin/api
```

`cargas/inteligencia/entorno.sh` exporta `AVISOS_ACTIVO=false`, así que hay que encenderlo a mano
después de sourcearlo. Al terminar, `bash infra/local/levantar-base.sh --limpiar` borra los
contenedores.

## Runbook por sesión

Encendido, en orden:

```bash
bash infra/local/levantar-base.sh --semilla          # base desechable con datos
set -a && source cargas/inteligencia/entorno.sh && set +a
export AVISOS_ACTIVO=true AVISOS_CUPO=5              # el entorno los deja apagados a propósito
out/bin/enriquecer lote 5000                         # siembra el enriquecimiento
out/bin/api                                          # primer plano, 127.0.0.1:8080
```

`make demo` hace lo mismo emitiendo con `cmd/avisos` antes de arrancar; el orden de arriba es el
del disparo al arrancar de PR-05.

Lista de verificación de cinco comandos:

```bash
curl -fsS http://127.0.0.1:8080/readyz
docker exec vera-demo-pg psql -U dueno -d vera_semilla_m8 -Atc 'select count(*) from inteligencia.movimiento_enriquecido'
bash infra/local/levantar-base.sh --ayuda >/dev/null && echo base-ok
grep -c 'avisos emitidos al arrancar' out/local/api.log 2>/dev/null || true
bash infra/servidor/verificar-banderas.sh local
```

Apagado: `Ctrl+C` en la API y `bash infra/local/levantar-base.sh --limpiar`.

## Arranque en frío, consumo y gasto

- Arranque en frío medido en esta VPS: la base quedó lista en torno a 1 minuto (build, migraciones y
  clave del rol), con Postgres aceptando consultas en unos 40 segundos y Redis antes. La siembra
  suma un par de minutos. No se midió el arranque de Ollama ni Whisper: esta VPS no los tiene.
- Contenedores con `--memory 512m --cpus 0.5` (pg) y `--memory 256m --cpus 0.2` (redis). La API y
  la siembra corren en el host. La suma queda bajo 2 CPU y 2 GB.
- Gasto: la demo no toca nube. `VERA_PROVEEDOR=simulado` no necesita red ni clave; `local` usa
  Ollama en loopback; `gemini` exigiría clave y cuota. El umbral para pruebas con proveedor real
  es cero en esta sesión y lo aprueba Christian Rubio. Las acciones de dinero quedan bloqueadas
  por el control de acciones, no por el proveedor.

## Pod de GPU

Runbook para la sesión de prueba y la presentación con el razonador E4B, Whisper y Kokoro en un pod
con GPU. La pila y sus variables están en `docs/pila-modelos-pod.md`, los costos y el umbral en
`docs/costos-pod-demo.md`, y el comando de encendido es `infra/modelos/pod-demo.sh`.

**Estado: NO PROBADO.** Hoy no hay pods de RunPod. Lo único comprobado es el script
con `bash -n`, shellcheck y `infra/modelos/pod-demo_test.sh` (`docker`, `curl` y `nvidia-smi`
simulados, sin tocar el docker real), y el compose con `docker compose config` (sin
`build` ni `up`). Todo paso marcado "no probado" se confirma la primera vez que haya pod, y lo que
falle se anota aquí.

### El día anterior

1. Revalidar precio y stock de la 4090 Secure. La 4090 suele estar en stock bajo (handoff, sección 10)
   y la tabla de `docs/costos-pod-demo.md` es de la cotización del 13 de noviembre de 2026.
2. Reservar o crear el pod 4090 Secure con volumen de 30 GB. Confirmar que el volumen es de **red**
   del proveedor y no el disco local del pod, porque solo así persiste al terminar el pod
   (`docs/pila-modelos-pod.md`). No probado.
3. Decidir el plan B antes de necesitarlo: **L4 o A40** (0,44 y 0,35 USD/h según
   `docs/costos-pod-demo.md`). Si la 4090 no tiene stock, se usa una de las dos. Si cambia la GPU, se
   anota el cambio y se revalida que E4B quepa en su memoria, cosa que no está medida aquí.
4. Tener a mano `WHISPER_TOKEN` y `KOKORO_TOKEN` (mínimo 32 caracteres cada uno, desde Infisical) y
   confirmar que el GGUF de E4B está registrado en Ollama dentro del volumen. El compose no lo registra
   por sí solo: `OLLAMA_MODELO` solo nombra el modelo que se espera. No probado.
5. Confirmar que el pod permite correr Docker. Un pod suele ser ya un contenedor, así que esto
   puede no estar disponible, y si no lo está hay que cambiar el enfoque antes de la sesión. No probado.

### Encender y precalentar

Con el pod ya creado y el repositorio dentro:

```bash
export WHISPER_TOKEN=... KOKORO_TOKEN=... OLLAMA_MODELO=gemma-4-e4b-it-q4_k_m   # desde Infisical, sin escribirlos en archivos
bash infra/modelos/pod-demo.sh encender
```

El subcomando levanta el compose con el override CUDA, espera en orden `vera-llm`, `vera-voz` y
`vera-kokoro`, manda una petición mínima al razonador para cargar el GGUF en la GPU y muestra la
salud. Falla con un mensaje claro si falta `WHISPER_TOKEN`, `KOKORO_TOKEN` u `OLLAMA_MODELO`, o si un
token tiene menos de 32 caracteres. No abre la sesión por sí solo: la sesión empieza cuando el script
termina en `pila lista`. No probado en un pod: la construcción de la imagen CUDA, la carga del modelo
en la GPU y el tiempo del primer arranque no están medidos. Lo único medido es la carga en frío de E2B
en una GPU local de 4 GB, de unos 34 segundos (`docs/mediciones-locales-2026-11-13.md`), que no sirve
como estimación para E4B en la 4090.

Durante la sesión, `bash infra/modelos/pod-demo.sh verificar` repite la comprobación y falla si algo
no responde.

### Alerta de presupuesto

**NO PROBADA hasta tener la cuenta.** Es lo que hay que configurar el día que exista, no algo ya hecho.

- Responsable: quien lidera la demo. Umbral: el de `docs/costos-pod-demo.md` (4,82 USD al mes, propuesto).
- En la cuenta del proveedor, activar la alerta de gasto o saldo que ofrezca y fijarla en el umbral.
  El nombre exacto de la opción y si existe no están verificados.
- Si la cuenta no ofrece alerta, el respaldo es no cargar más saldo que el umbral y revisar el gasto
  a mano al terminar cada sesión. Tampoco está comprobado que la cuenta funcione por saldo.
- La alerta avisa, no apaga nada. Lo que corta el gasto es terminar el pod.

### Apagar

1. Terminar la sesión y guardar los artefactos que se quieran conservar fuera del pod.
2. `bash infra/modelos/pod-demo.sh apagar` baja los contenedores y conserva el volumen.
3. En la consola del proveedor, **Terminate** y no Stop: la regla de `docs/pila-modelos-pod.md` es
   terminar el pod entre sesiones para no pagar GPU ociosa. Qué cobra un pod en Stop no se evaluó,
   por eso no se usa.
4. Comprobar en la consola que no queda ningún pod activo. El volumen de 30 GB sigue cobrando aparte
   (2,10 USD al mes según `docs/costos-pod-demo.md`) hasta que se borre.

Lo que se pierde al terminar: las imágenes construidas (el siguiente encendido las reconstruye, y
Kokoro vuelve a bajar sus 0,14 GB) y el estado en memoria. Los pesos de E4B y Whisper no se vuelven a
descargar si el volumen persiste.

## Si algo no arranca

1. Postgres no acepta consultas: `docker logs vera-demo-pg`, `--limpiar` y volver a levantar.
   Los puertos 55437 y 6380 tienen que estar libres.
2. `catálogo curado no disponible` en el log: la cascada sigue sin el escalón 4 y la demo funciona
   con menos categorías curadas.
3. Ollama o Whisper no responden: la demo sigue sin voz y sin transcripción; usa
   `VERA_PROVEEDOR=simulado` y deja el audio como lo que falta, sin improvisarlo.
4. Avisos sin tarjetas: confirma `AVISOS_ACTIVO=true`, `AVISOS_CUPO=5` y la siembra. El log
   `avisos emitidos al arrancar` dice cuántos se guardaron.

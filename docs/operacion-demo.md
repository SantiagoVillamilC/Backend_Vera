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
docker exec vera_sandbox_pg psql -U dueno -d vera_semilla_m8 -Atc 'select count(*) from inteligencia.movimiento_enriquecido'
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

## Si algo no arranca

1. Postgres no acepta consultas: `docker logs vera_sandbox_pg`, `--limpiar` y volver a levantar.
   Los puertos 55437 y 6380 tienen que estar libres.
2. `catálogo curado no disponible` en el log: la cascada sigue sin el escalón 4 y la demo funciona
   con menos categorías curadas.
3. Ollama o Whisper no responden: la demo sigue sin voz y sin transcripción; usa
   `VERA_PROVEEDOR=simulado` y deja el audio como lo que falta, sin improvisarlo.
4. Avisos sin tarjetas: confirma `AVISOS_ACTIVO=true`, `AVISOS_CUPO=5` y la siembra. El log
   `avisos emitidos al arrancar` dice cuántos se guardaron.

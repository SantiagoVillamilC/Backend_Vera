# Banderas por entorno de la API (CF-A0-02)

La API de Vera se despliega en tres destinos y cada uno enciende cosas distintas. Este documento
fija cuáles, quién responde por cada una y cómo se comprueba que el contenedor que corre las tiene
de verdad.

La fuente de verdad es `infra/servidor/banderas.tsv`: una fila por bandera y una columna por
destino (`local`, `demo`, `produccion`). Lo demás sale de ahí.

- `infra/servidor/pasos/60-api.sh` lee la columna del destino y la escribe en el `environment:` del
  `compose.api.yaml` que genera.
- `.env.local.example`, `.env.demo.example` y `.env.produccion.example` son esas tres columnas
  exportadas a archivo, para arrancar la API a mano sin reconstruir el compose.
- `.env.example` documenta el valor por defecto de cada bandera, el que aplica la aplicación cuando
  la variable no está definida.
- `cmd/api/config_banderas_test.go` falla si la matriz, `.env.example`, las plantillas y lo que
  `LoadConfig` resuelve dejan de coincidir.

## Matriz

| Variable | local | demo | produccion | Responsable | Qué enciende |
| --- | --- | --- | --- | --- | --- |
| `CLASIFICADOR` | `tfidf` | `tfidf` | `apagado` | Ciencia de datos | Escalón 6 de la cascada. `tfidf` carga el artefacto embebido. |
| `LEXICO` | `v2` | `v2` | `apagado` | Ciencia de datos | Escalón 5 de la cascada. `v2` es el único léxico embebido. |
| `AVISOS_ACTIVO` | `false` | `true` | `false` | Producto | Motor de avisos de consumo: el trabajo diario de River y la corrida al arrancar. |
| `AVISOS_HORA` | `06:00` | `06:00` | `06:00` | Producto | Hora de Colombia, formato `HH:MM` de 24 horas. |
| `AVISOS_CUPO` | `3` | `5` | `3` | Producto | Tope de avisos por usuario y mes, entero positivo. |
| `VERA_PROVEEDOR` | `simulado` | `simulado` | `simulado` | Backend IA | Proveedor del asistente. `gemini` exige `GEMINI_API_KEY` y `VERA_MODELO`. |
| `VERA_MODELO` | vacío | vacío | vacío | Backend IA | Modelo de Gemini. No hay valor fijado en el código. |
| `METRICAS_ACTIVO` | `false` | `false` | `false` | Operación | Superficie `/metrics`. |
| `ENRIQUECIMIENTO_INTERVAL` | `5m` | `5m` | `5m` | Backend IA | Periodo del lote incremental de enriquecimiento. |

Dos escalones no tienen bandera y por eso van en la matriz con la primera columna sin nombre de
variable:

| Escalón | Valor | Por qué |
| --- | --- | --- |
| 3, MCC | siempre | `escalonesDelLote` lo evalúa en toda la rama de QR a comercio. No hay interruptor. |
| 4, catálogo | por datos | Se enciende con filas en `inteligencia.catalogo_marca`. Sin filas la copia no cambia por él. |

## Cupo de avisos

`AVISOS_CUPO` es cuántos avisos puede recibir un usuario en un mes. Lo leen los dos emisores,
`cmd/api/config.go` para la API y `cmd/avisos/main.go` para la corrida suelta, y llega al servicio
por `inteligencia.ConCupoDeAvisos`, que cambia solo el tope de la configuración de avisos. Sin la
variable, ambos toman `avisos.CupoMensualPorDefecto`. El resumen del mes informa la misma cifra,
así que subir el cupo cambia cuántos avisos ve el usuario y lo que la API le promete en el resumen.

El emisor ordena las tarjetas del mes por prioridad y recorta por severidad dentro de la misma
prioridad. Las prioridades son pago (`1`), ingreso (`2`), cobro (`3`), presupuesto (`4`) y categoría
(`5`). Con `3`, que es el valor de producto, el aviso de presupuesto nunca entra: los tres primeros
puestos se los llevan pago, ingreso y cobro, y la categoría tampoco entra.

- `3` conserva el comportamiento actual y es el valor de `local` y de `produccion`.
- `5` deja caber los cinco avisos y es solo para `demo`, donde la demostración tiene que poder
  mostrarlos si los datos los disparan. El cupo es un techo y no una garantía. Medición del
  12 de noviembre de 2026 sobre una siembra de 300 usuarios con semilla `20261020` y 9.300
  movimientos enriquecidos: 51 avisos a 50 usuarios, 49 de caída de ingreso y 2 de cobro nuevo,
  con un máximo de 2 por usuario. La emisión es idéntica con `AVISOS_CUPO=3` y con
  `AVISOS_CUPO=5`, porque ningún usuario llegó a tres disparos, y en esa corrida los otros tres
  tipos no aparecen: el `5` de la demo les deja lugar y no los inventa. Esa corrida se reprodujo
  con `go run ./cmd/semilla-inteligencia`, `go run ./cmd/enriquecer lote`, `go run ./cmd/avisos`
  con el mismo `-ahora` y esta consulta para el desglose por tipo:
  `select tipo, count(*) from inteligencia.aviso group by tipo order by tipo;`.
  Quedan `out/evidencia-pr02/semilla.json`, `avisos-cupo3.json` y `avisos-cupo5.json` (ignorados
  por git), que prueban el conteo total; el desglose por tipo sale de la consulta, no de un
  archivo escrito a mano.

Subirlo no cambia la calidad de un aviso, solo cuántos caben en el mes. Cualquier otro valor
positivo es válido, y cero, negativo o no numérico detienen el arranque de la API y la corrida de
`cmd/avisos` con un error de `AVISOS_CUPO`. `infra/local/demostrar.sh` fija `AVISOS_CUPO=5` a
mano para que la demo, que emite con `cmd/avisos`, deje caber los cinco avisos: el script no lee
`banderas.tsv`, así que la columna `demo` no es lo que arranca la demo.

Decisiones que la matriz deja escritas, para que no se rediscutan por accidente:

- **`AVISOS_ACTIVO=true` solo en `demo`.** `demo` es la única superficie donde el motor de avisos
  corre de punta a punta: emite a las 06:00 de Colombia y una vez al arrancar, y como la bandeja de
  avisos de la app todavía no existe, el aviso solo se ve por la API. En `local` sigue apagado
  porque `infra/local/demostrar.sh` enciende el emisor con sus propios valores y
  `cargas/inteligencia/entorno.sh` lo deja apagado para congelar el corte de medición. En
  `produccion` sigue apagado hasta que haya un piloto con personas (`README.md`), y encenderlo ahí
  es una decisión de producto pendiente, no un cambio de infraestructura.
- **`METRICAS_ACTIVO` sigue apagado en los tres destinos.** `/metrics` exige `METRICAS_TOKEN` de
  32 bytes o más y la API no arranca sin él, así que el compose no enciende la superficie. Las
  corridas de medición sí la encienden con su propio token (`cargas/inteligencia/entorno.sh`,
  `infra/local/demostrar.sh`).
- **`VERA_PROVEEDOR=simulado` en los tres destinos** porque no hay credencial de Gemini en el
  repositorio. Cambiarlo a `gemini` sin `GEMINI_API_KEY` y `VERA_MODELO` deja la API sin arrancar,
  a propósito.
- **`local` y `demo` solo difieren en `AVISOS_CUPO` y `AVISOS_ACTIVO`.** `local` y `produccion`
  llevan `3`, que es el valor de producto, y `demo` lleva `5` para que quepan los cinco avisos.
  `produccion` no coincide: además del cupo, apaga `CLASIFICADOR`, `LEXICO` y `AVISOS_ACTIVO`, que
  en `local` y `demo` van en `tfidf`, `v2` y encendido solo en `demo`. Cuando deje de ser cierto,
  se cambia la matriz y no el paso.

## Cuándo corre el motor de avisos

Con `AVISOS_ACTIVO=true` la API agenda `AvisosWorker` en River a la hora de `AVISOS_HORA` (06:00
de Colombia, formato `HH:MM` de 24 horas) y, además, `cmd/api` emite una vez al arrancar, después
de levantar los jobs (`lanzarAvisosAlArrancar`, `cmd/api/wiring_avisos.go`). La corrida al arrancar
existe porque una demo o un despliegue que arranca a media mañana no debe esperar hasta las 06:00
del día siguiente para mostrar sus tarjetas.

Esa corrida no duplica nada: la deduplicación por clave y período de
`internal/inteligencia/avisos_servicio.go` descarta el aviso que ya está guardado, así que
repetirla es inocuo y vale reiniciar la API las veces que haga falta. El conteo puede subir entre
reinicios por el enriquecimiento incremental, que habilita usuarios que antes no entraban, nunca por
duplicación. La corrida sale en una goroutine que se lanza antes de que el servidor HTTP escuche, así
que no hay compuerta de preparación y una consulta inmediata puede leer antes de que termine: hay que
esperar en el log la línea `avisos emitidos al arrancar`. Si el emisor falla o entra en pánico, la API
lo registra en el log y sigue atendiendo: una corrida perdida no tumba el arranque. Con la bandera
apagada no se agenda el job ni se lanza la goroutine.

Sin bandeja en la app, el aviso solo se ve por la API: `GET /v1/inteligencia/avisos` devuelve las
tarjetas del mes pedido, o del mes en curso cuando no recibe `mes`, y `GET /v1/inteligencia/resumen`
las cuenta.

El banco de medición de M8 (`cargas/inteligencia/entorno.sh`) no es el destino `local`: fija sus
propios valores para congelar el corte (`ENRIQUECIMIENTO_INTERVAL=24h`, `METRICAS_ACTIVO=true` con
su propio token) y **no exporta `LEXICO`**, así que un API arrancado con ese entorno deja el escalón
5 apagado aunque la columna `local` diga `v2`. Si una corrida necesita el escalón 5, se exporta
`LEXICO=v2` a mano antes de arrancar. La columna `local` describe el compose de `60-api.sh`, no el
banco de medición.

`VERA_VOZ_URL` y `VERA_VOZ_TOKEN` quedan fuera de la matriz: encienden un servicio de voz opcional
que no depende del destino y que la API toma en `cmd/api/main.go`, no en `LoadConfig`. Su
operación está en `docs/stt-tts-operacion.md` y su plantilla en `.env.example`.

## Elegir el destino

El paso `60-api.sh` toma el destino de `DESTINO`, que se declara en `infra/servidor/aprovisionar.env`
(el mismo archivo que ya usa `50-certbot.sh` para `CORREO_CERTBOT`):

```env
DESTINO=produccion
```

Si `DESTINO` falta, el paso aborta: elegir `local` por omisión en una VM que comparte el comando SSH
de staging y main encendería los escalones 5 y 6 sin que nadie lo declare. El único consumidor que
declara el destino por su cuenta es `probar-en-docker.sh`, que pasa `DESTINO=local` al contenedor
desechable donde prueba la idempotencia. `verificar-banderas.sh` también falla con código 3 cuando no
encuentra destino, así que un servidor sin declararlo se nota en la verificación y no se queda
callado.

Cambiar de destino regenera `compose.api.yaml`, lo que recrea el contenedor de la API en el
siguiente `up -d`.

## Verificación

Contra el contenedor que está corriendo:

```bash
make verificar-banderas
# o, con el destino explícito:
./infra/servidor/verificar-banderas.sh produccion
```

El script compara cada bandera de la columna contra `docker inspect` de `centinela-api` e imprime
una línea por variable. También reporta como `DIFF` las variables de la familia de banderas que el
contenedor todavía trae y la matriz no declara, que es una bandera vieja de un despliegue anterior;
las variables del sistema (`PATH`, `HOME` y compañía) no son de la familia y no se reportan. Antes
de comparar mira `.State.Status`: un contenedor detenido o en crash loop conserva su `Config.Env`
intacto, así que sin ese control el script devolvería 0 sin que haya API corriendo. Códigos de
salida:

| Código | Significado |
| --- | --- |
| 0 | Todas las banderas del contenedor coinciden con la matriz y no sobran banderas. |
| 2 | Alguna bandera falta, tiene otro valor o sobra. Vuelve a correr `pasos/60-api.sh` para recrear el contenedor. |
| 3 | No se pudo verificar: sin destino, sin docker, sin el contenedor o con el contenedor fuera de `running`. |

La comparación también se puede probar en seco, sin docker, inyectando la lista de entorno y
simulando el estado:

```bash
ENTORNO_CONTENEDOR="$(printf '%s\n' CLASIFICADOR=tfidf AVISOS_ACTIVO=false)" \
  ESTADO_CONTENEDOR=running ./infra/servidor/verificar-banderas.sh local
./infra/servidor/verificar-banderas_test.sh
```

La prueba cubre los cuatro casos del contrato: coincide (0), falta una (2), sobra una (2) y error (3).
No se corrió shellcheck (no está instalado en esta VPS): la sintaxis se comprobó con `bash -n`.

Sin docker a mano, dos comprobaciones parciales:

```bash
# Qué emitiría el paso para un destino, sin tocar el servidor:
source infra/servidor/lib/comun.sh
banderas_del_destino infra/servidor/banderas.tsv demo

# Que el paso siga siendo idempotente en un contenedor desechable:
infra/servidor/probar-en-docker.sh
```

Para ver el `compose.api.yaml` generado hace falta correr el paso en la VM (o sobre una copia del
árbol con las rutas de `/opt` redirigidas), porque escribe en `/opt/centinela/api`.

Además, `go test ./cmd/api/` cubre la coherencia entre la matriz, `.env.example`, las plantillas y
`LoadConfig` sin necesitar docker ni la VM.

## Criterio pendiente

La verificación real de `make verificar-banderas` sobre la VM de demo y la de producción queda
pendiente: en esta sesión no hubo acceso a ninguna de las dos. Lo que sí está comprobado es la
generación del compose para los tres destinos y los códigos de salida del script con un `docker`
simulado.

## Nota de alcance

`infra/servidor/README.md` tiene cambios ajenos sin commitear en esta rama, así que no se tocó. La
documentación del destino y de las banderas vive aquí.

# Escenario de demo de CF-A2-03

## Ejecución

Requiere Go 1.27 y una base ya migrada, exclusiva de demo. No crea credenciales ni
una sesión de aplicación: `DEMO_USUARIO` identifica al propietario de los movimientos.
Para entrar desde la app se debe preparar además su cuenta con el flujo de demo existente.

```bash
export DATABASE_URL='postgres://dueno:CLAVE@127.0.0.1:55437/vera_semilla_demo?sslmode=disable'
export DEMO_USUARIO='11111111-1111-4111-8111-111111111111'
export DEMO_FECHA='2026-11-19'
export AVISOS_CUPO='5'
bash infra/local/sembrar-escenario-demo.sh
bash infra/local/sembrar-escenario-demo.sh
```

El cupo de la demo es 5 y los dos guiones lo fijan por entorno. Los cinco hechos caen en dos
períodos: octubre agrupa `caida_ingreso` y `cobro_nuevo`, y noviembre agrupa `pago_no_alcanza`,
`presupuesto_anticipado` y `categoria_sobre_media`. Con el cupo de producto 3 noviembre se
recorta por prioridad y se pierden avisos, así que la demo necesita 5 para enseñar las cinco
familias. El comando lee `AVISOS_CUPO` igual que la API.

La fecha es un reloj **simulado**, siempre el día 19, para evaluar el pago de ENEL del
día 20. Si se omite, se usa el 19 del mes actual en Bogotá. No equivale a la fecha real
del servidor: al consultar la demo fuera de ese periodo algunos avisos podrían no
aparecer como vigentes. Usa una fecha del mes en que vayas a demostrarla.

El comando rechaza hosts distintos de `127.0.0.1`, puertos distintos de `55437`, bases
sin prefijo `vera_semilla_` y parámetros de conexión que puedan sustituir el destino.
Usa el propietario de la base desechable, porque el reinicio necesita eliminar datos.

## Datos y reglas

Se escriben 56 asientos balanceados (112 apuntes) para un único usuario. Los importes
se convierten de pesos a centavos enteros; se conserva el signo del ledger: el abono
a la cuenta pasiva del usuario es negativo.

- Cinco pagos históricos de ENEL por $50.000, los días 20.
- Cinco pagos de INTERNET HOGAR por $89.900, los días 25.
- Gastos en alimentos, transporte, restaurantes, salud y entretenimiento durante
  seis meses; el comercio `ZXQ987654` produce una abstención.
- STREAMING por $24.900 el 28 del mes anterior y el 12 del actual. Son dos cargos
  dentro de 35 días: el detector exige dos, aunque el borrador decía «primera vez».
- Ingreso histórico de $200.000 y de $138.000 en el mes anterior y el actual, más el
  reembolso de $100.000 en el mes actual. La caída es del 31 %: la regla usa desigualdad
  estricta, así que exactamente 30 % no dispara.
- Saldo inicial de $4.000 y saldo final de $7.100 antes del pago de ENEL.
- Compra de tecnología por $120.000 el día 7 del mes actual. Con ella el gasto
  acumulado del mes en curso queda en $198.500, por encima de la mediana de los meses
  completos, que es $189.900, y `presupuesto_anticipado` dispara con el multiplicador
  1.0. El reembolso mantiene el saldo positivo y por debajo de los $139.900 de ENEL e
  INTERNET HOGAR, así que `pago_no_alcanza` sigue disparando.
- Cuatro entradas pequeñas el mismo mes, para encender los escalones que faltaban al censo:
  una compra QR de $500 con MCC 5411 (`mcc`), una marca de $600 registrada en
  `inteligencia.catalogo_marca` (`catalogo`), una corrección de $700 en la memoria privada
  del usuario (`memoria`) y un pago de $800 con $80 de GMF contra `pasivo:gmf_por_pagar`
  (`contraparte_interna`).

Se reutilizan el enriquecimiento, el léxico v2 y los detectores de producción.
No se insertan avisos prefabricados ni se modifica la cascada. El censo por origen sale
con seis escalones distintos: `catalogo=1`, `contraparte_interna=1`, `lexico=39`, `mcc=1`,
`memoria=1` y `sin_categoria=13`. Las cuatro entradas nuevas de la sección anterior son las
que encienden los escalones que faltaban; el séptimo (`tfidf`) se sostiene con la prueba de
integración de A1-03 (`d114d0c`) porque el escenario no enciende el clasificador. El
criterio de CF-A2-03 de al menos cinco orígenes **se cumple** y la tarjeta queda completada.

## Idempotencia y reinicio

El manifiesto `public.demo_escenario` conserva fecha, asientos e identificadores por
usuario. Lo crea la migración `bd/migraciones/0034_create_demo_escenario.sql`: el comando
no crea esquema en tiempo de ejecución. Un candado por usuario cubre siembra,
enriquecimiento, avisos y evidencia.
La segunda corrida reutiliza esos identificadores y la fecha, incluso si cambia el mes.
El enriquecimiento se limita a esos asientos y no mueve el cursor global.

```bash
REINICIAR=si bash infra/local/sembrar-escenario-demo.sh
```

Reiniciar elimina los asientos del manifiesto y la copia/avisos del usuario exclusivo,
y recrea el escenario. Renueva los identificadores de `journal_entry`; los de
transferencia se derivan del usuario, del mes y del índice (`uuid.NewSHA1`), así que
dentro del mismo mes se repiten. Rechaza usuarios con asientos ajenos. No toca otros
usuarios, la siembra masiva, el cursor global, los modelos ni datos de producción.

El cursor global no se mueve en ninguna corrida: `ProcesarEscenario` enriquece solo los
asientos indicados (`internal/inteligencia/escenario.go:12-14`), de modo que la prueba
del cursor vale sobre una base solo migrada, sin enriquecimiento previo.

## Evidencia

Cada corrida imprime JSON y escribe `DEMO-ESCENARIO-STATE.json` (o la ruta `ESTADO`).
Contiene usuario, fecha, IDs, conteos por categoría/origen/tipo de aviso y series maduras
recurrentes. Conserva el JSON de las dos corridas para pegarlo en la tarjeta.

Resultados esperados con cupo 5: 56 movimientos, 7 series maduras y cinco avisos:
`caida_ingreso=1`, `categoria_sobre_media=1`, `cobro_nuevo=1`, `pago_no_alcanza=1`,
`presupuesto_anticipado=1`. Categorías: alimentos 7, transporte 6, restaurantes 6, salud 6,
entretenimiento 7, compras 2, comisiones 9 y sin categoría 13. Los 8 ingresos son clasificados
por el léxico existente como comisiones; no se cambia esa regla desde la siembra. Las cuatro
entradas nuevas reparten una categoría cada una y la de entretenimiento empuja la categoría
sobre la media, que es el quinto aviso.

Prueba reproducible contra una base local de demo ya migrada:

```bash
SEMILLA_DATABASE_URL='postgres://<usuario>:<clave>@127.0.0.1:55437/vera_semilla_<nombre>?sslmode=disable' \
  go test -race -tags=integration ./cmd/semilla-escenario
```

La prueba lee `SEMILLA_DATABASE_URL` y no `DATABASE_URL`, porque esta última la comparten las demás
pruebas de integración y apunta a la base principal, que el guard rechaza a propósito. Sin la
variable la prueba hace `t.Skip`. En CI (`.github/workflows/ci.yml`, job `integration`) el mismo
servicio Postgres publica además el puerto 55437, el paso `Create and migrate scenario seed database`
crea y migra `vera_semilla_ci` por ese puerto y el paso de skips inesperados falla el job si la
variable falta y la prueba se omite.

La prueba comprueba dos corridas idénticas, reinicio con los mismos conteos y cursor
global intacto. Fija `AVISOS_CUPO=5` por sí misma, exige `presupuesto_anticipado=1` y
compara el censo exacto de categorías y orígenes con el de esta página, así que otra
repartición no pasa en verde. Solo debe ejecutarse sobre una base desechable.

## Inyectar y limpiar la demo en producción

Dos guiones dejan o quitan la cuenta de demo en la base `vera_semilla_demo` que usa la API
desplegada: `infra/local/demo-inyectar.sh` y `infra/local/demo-limpiar.sh`. Ambos exigen el DSN
del dueño por el túnel y rechazan cualquier otro destino con una expresión estricta
(`127.0.0.1:55437/vera_semilla_*`). Ninguno imprime el DSN con clave ni la contraseña de la cuenta.

### Túnel y respaldo previo

```bash
ssh -N -L 55437:127.0.0.1:55437 centinela
```

Antes de inyectar o limpiar conviene un respaldo de la base. El volcado se genera dentro del
contenedor y se guarda en el portátil. El rol `-U dueno` y el nombre `postgres-demo` son los que
usa el despliegue, pero el comando no se probó contra ese contenedor: si la autenticación falla,
ajusta el rol.

```bash
ssh centinela 'sudo docker exec postgres-demo pg_dump -U dueno -Fc vera_semilla_demo' \
  > "respaldo-vera_semilla_demo-$(date +%Y%m%d-%H%M).dump"
```

### Orden de uso

1. Respaldo y túnel abierto.
2. Simulación de la limpieza, que solo hace SELECT y muestra qué se borraría con conteos por tabla.
3. Inyección. Si el usuario tiene asientos fuera del manifiesto, la inyección llama sola a la
   limpieza con `CONFIRMAR=si`.
4. Limpieza real solo cuando haya que retirar la cuenta.

```bash
export DATABASE_URL='postgres://dueno:CLAVE@127.0.0.1:55437/vera_semilla_demo?sslmode=disable'

bash infra/local/demo-limpiar.sh                      # simulación
API_URL='https://api.ejemplo.test' bash infra/local/demo-inyectar.sh
CONFIRMAR=si bash infra/local/demo-limpiar.sh         # borra de verdad
```

La inyección compila los dos comandos, siembra con `REINICIAR=si` si ya hay manifiesto (o sin él si
no lo hay), espera a que el lote de enriquecimiento de la API pase por los asientos nuevos, vuelve a
sembrar sin reiniciar y crea la cuenta con `login-demo`. La espera existe porque el lote corre cada
5 minutos con un cursor global: los asientos nuevos quedan por delante del cursor y, en el siguiente
ciclo, el lote los reescribe con tfidf y la fila del escalón catálogo pasa a `sin_categoria`. Sembrar
de nuevo sin reiniciar deja el censo determinista. Es idempotente: dos ejecuciones seguidas dejan el
mismo resultado.

### Variables

| Variable | Guion | Por defecto |
|----------|-------|-------------|
| `DATABASE_URL` | ambos | obligatoria, DSN del dueño por el túnel |
| `DEMO_USUARIO` | ambos | `11111111-1111-4111-8111-111111111111` |
| `DEMO_FECHA` | inyectar | día 19 del mes actual en Bogotá (`YYYY-MM-19`) |
| `DEMO_CELULAR` | inyectar | `+573205550142` |
| `DEMO_CONTRASENA` | inyectar | `Vera-Demo-2026` |
| `DEMO_NOMBRE` | inyectar | `Andres Vargas` |
| `ESPERAR_LOTE` | inyectar | `si`. Con `no` no espera ni siembra por segunda vez |
| `ESPERA_SEGUNDOS` | inyectar | `720` (12 minutos) |
| `ESTADO` | inyectar | `out/demo/escenario-<uuid>.json`, un archivo por usuario |
| `API_URL` | inyectar | vacía. Si está, hace login y consulta saldo, resumen y avisos |
| `CONFIRMAR` | limpiar | `no`. Sin `si` solo simula |

Si el lote no alcanza los asientos en 12 minutos, la inyección falla con un mensaje que lo explica.
Eso pasa si la API está apagada o si en esa base no corre un lote. En el segundo caso se vuelve a
ejecutar con `ESPERAR_LOTE=no`. Con `API_URL` se imprimen el tipo, el período y el `accion_codigo`
de cada aviso, mes por mes, porque los avisos del escenario caen en dos períodos.

El cupo del escenario es `AVISOS_CUPO=5` y solo afecta a la siembra. La API de producción usa
cupo 3 y no se toca.

### Qué borra la limpieza

Sin `CONFIRMAR=si` solo imprime los conteos. Con `CONFIRMAR=si` borra en una sola transacción, con
conteos antes y después:

- `asistente`: `llamada_funcion`, `solicitud_texto`, `mensaje`, `accion_pendiente`, `conversacion`.
- `inteligencia`: `aviso_entrega`, `aviso`, `movimiento_enriquecido`, `memoria_correccion`,
  `voto_destino` y la marca `TIENDA-CATALOGO-DEMO` de `catalogo_marca` (origen `escenario`). La marca
  es global, así que solo se retira si no queda otro usuario en `public.demo_escenario`.
- `nucleo`: `posting`, `journal_entry`, `transferencia`, `deposito` y las cuentas cuyo titular es el
  usuario. De los asientos se borran todos los apuntes, incluido el de `pasivo:gmf_por_pagar`.
- `auth`: `transiciones_credencial`, `codigos_respaldo`, `tokens_restablecimiento`, `desafios`,
  `sesiones`, `credenciales`, `roles_de_parte`, `empresas`, `kyc_estados`, `personas` y `partes`.
- `public`: `demo_escenario` y `users`.

Defensas: rechaza a Camila (`8a665137-c89b-5afe-974a-91ad20f974f4`) y a Valentina Rios
(`8407bb2a-cab9-4d42-a111-3a7b64466571`), rechaza a cualquier usuario que no sea el de demo por
defecto ni tenga fila en `public.demo_escenario`, y aborta sin escribir si un asiento del usuario
toca cuentas de otro titular o si hay eventos pendientes suyos en una outbox. Antes de borrar toma
el candado del escenario y la fila única del cursor, para que un lote en curso termine primero.

### Qué NO cubre

- `auth.eventos_autenticacion` es append-only por trigger, así que sus filas quedan como huérfanas
  inofensivas (no tiene clave foránea hacia `auth.partes`).
- `auth.consentimientos` y `auth.kyc_verificaciones` son inmutables y apuntan a la parte. Si el
  usuario tiene filas ahí, la limpieza conserva `auth.partes` y `auth.personas` y borra el resto.
- Las dos outbox (`nucleo.outbox` y `auth.outbox`), `nucleo.descuadre`,
  `inteligencia.promocion_destino` (conocimiento compartido sin autor), los trabajos de `river` y el
  catálogo que la API mantiene en memoria.
- Una recarga PSE confirmada toca la custodia compartida. Al borrarla, la siguiente conciliación
  puede registrar un `descuadre`.
- Un lote de la API que ya estaba escribiendo cuando empezó la limpieza puede dejar filas de
  inteligencia del usuario. La limpieza lo avisa al final y basta con repetirla.
- La contraseña de la cuenta viaja como argumento `-contrasena` de `login-demo`, así que es visible
  en la lista de procesos del portátil mientras corre. Usa una contraseña de demo, nunca una real.
- La base no se respalda sola: el respaldo previo es responsabilidad de quien ejecuta.

### Varias cuentas

Vera solo tiene una cuenta por usuario. Para mostrar «varias cuentas» se usan varios usuarios de
demo, cada uno con su `DEMO_USUARIO`, su `DEMO_CELULAR` y su archivo `ESTADO`. La inyección rechaza
un celular que ya pertenezca a otro usuario.

```bash
DEMO_USUARIO=22222222-2222-4222-8222-222222222222 DEMO_CELULAR=+573205550143 \
  DEMO_NOMBRE='Laura Mejia' bash infra/local/demo-inyectar.sh
```

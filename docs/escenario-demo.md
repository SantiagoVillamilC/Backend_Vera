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

El cupo de la demo es 5 y los dos guiones lo fijan por entorno. No es lo que hace aparecer
el aviso de presupuesto: los cuatro hechos caen en dos períodos de dos, octubre agrupa
`caida_ingreso` y `cobro_nuevo` y noviembre agrupa `pago_no_alcanza` y
`presupuesto_anticipado`, así que con el cupo de producto 3 los cuatro entrarían igual. Se
fija 5 para dejar holgura y no depender del recorte por prioridad. El comando lee
`AVISOS_CUPO` igual que la API.

La fecha es un reloj **simulado**, siempre el día 19, para evaluar el pago de ENEL del
día 20. Si se omite, se usa el 19 del mes actual en Bogotá. No equivale a la fecha real
del servidor: al consultar la demo fuera de ese periodo algunos avisos podrían no
aparecer como vigentes. Usa una fecha del mes en que vayas a demostrarla.

El comando rechaza hosts distintos de `127.0.0.1`, puertos distintos de `55437`, bases
sin prefijo `vera_semilla_` y parámetros de conexión que puedan sustituir el destino.
Usa el propietario de la base desechable, porque el reinicio necesita eliminar datos.

## Datos y reglas

Se escriben 52 asientos balanceados (104 apuntes) para un único usuario. Los importes
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
- Saldo inicial de $4.000 y saldo final de $9.700 antes del pago de ENEL.
- Compra de tecnología por $120.000 el día 7 del mes actual. Con ella el gasto
  acumulado del mes en curso queda en $195.900, por encima de la mediana de los meses
  completos, que es $189.900, y `presupuesto_anticipado` dispara con el multiplicador
  1.0. El reembolso mantiene el saldo positivo y por debajo de los $139.900 de ENEL e
  INTERNET HOGAR, así que `pago_no_alcanza` sigue disparando.

Se reutilizan el enriquecimiento, el léxico v2 y los detectores de producción.
No se insertan avisos prefabricados ni se modifica la cascada. El conteo de orígenes
actual es `lexico` (escalón 5) y `sin_categoria`. El criterio de CF-A2-03 de al menos
cinco escalones distintos por origen **no se cumple** con este escenario, y la causa no es
la cascada: el lote ya escribe los siete orígenes (CF-A1-03, `d114d0c`). Lo que falta son
las entradas del escenario que los disparan: un MCC en el QR, una marca del catálogo, una
corrección en memoria privada y una contraparte interna. Mientras no se agreguen, la
tarjeta queda en `parcial` por este criterio y el censo de siete orígenes se sostiene con
la prueba de integración de A1-03, no con la demo.

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

Resultados esperados con cupo 5: 52 movimientos, 7 series maduras y cuatro avisos:
`caida_ingreso=1`, `cobro_nuevo=1`, `pago_no_alcanza=1`, `presupuesto_anticipado=1`.
Categorías: alimentos 6, transporte 6, restaurantes 6, salud 6, entretenimiento 6,
compras 1, comisiones 8 y sin categoría 13. Los 8 ingresos son clasificados por el léxico
existente como comisiones; no se cambia esa regla desde la siembra. La compra queda en
`compras` y no mueve el aviso de categoría, porque esa categoría no tiene meses
completos con gasto.

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

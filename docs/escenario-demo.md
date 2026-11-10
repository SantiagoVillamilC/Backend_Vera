# Escenario de demo de CF-A2-03

## Ejecución

Requiere Go 1.27 y una base ya migrada, exclusiva de demo. No crea credenciales ni
una sesión de aplicación: `DEMO_USUARIO` identifica al propietario de los movimientos.
Para entrar desde la app se debe preparar además su cuenta con el flujo de demo existente.

```bash
export DATABASE_URL='postgres://dueno:CLAVE@127.0.0.1:55437/vera_semilla_demo?sslmode=disable'
export DEMO_USUARIO='11111111-1111-4111-8111-111111111111'
export DEMO_FECHA='2026-11-19'
bash infra/local/sembrar-escenario-demo.sh
bash infra/local/sembrar-escenario-demo.sh
```

La fecha es un reloj **simulado**, siempre el día 19, para evaluar el pago de ENEL del
día 20. Si se omite, se usa el 19 del mes actual en Bogotá. No equivale a la fecha real
del servidor: al consultar la demo fuera de ese periodo algunos avisos podrían no
aparecer como vigentes. Usa una fecha del mes en que vayas a demostrarla.

El comando rechaza hosts distintos de `127.0.0.1`, puertos distintos de `55437`, bases
sin prefijo `vera_semilla_` y parámetros de conexión que puedan sustituir el destino.
Usa el propietario de la base desechable, porque el reinicio necesita eliminar datos.

## Datos y reglas

Se escriben 50 asientos balanceados (100 apuntes) para un único usuario. Los importes
se convierten de pesos a centavos enteros; se conserva el signo del ledger: el abono
a la cuenta pasiva del usuario es negativo.

- Cinco pagos históricos de ENEL por $50.000, los días 20.
- Cinco pagos de INTERNET HOGAR por $89.900, los días 25.
- Gastos en alimentos, transporte, restaurantes, salud y entretenimiento durante
  seis meses; el comercio `ZXQ987654` produce una abstención.
- STREAMING por $24.900 el 28 del mes anterior y el 12 del actual. Son dos cargos
  dentro de 35 días: el detector exige dos, aunque el borrador decía «primera vez».
- Ingreso histórico de $200.000 y de $138.000 en el mes anterior y el actual. La caída
  es del 31 %: la regla usa desigualdad estricta, así que exactamente 30 % no dispara.
- Saldo inicial de $4.000 y saldo final de $29.700 antes del pago de ENEL.

Se reutilizan el enriquecimiento, el léxico v2 y los detectores de producción.
No se insertan avisos prefabricados ni se modifica la cascada. El conteo de orígenes
actual es `lexico` y `sin_categoria`; los seis orígenes siguen fuera de esta aceptación.

## Idempotencia y reinicio

El manifiesto `public.demo_escenario` conserva fecha, asientos e identificadores por
usuario. Un candado por usuario cubre siembra, enriquecimiento, avisos y evidencia.
La segunda corrida reutiliza esos identificadores y la fecha, incluso si cambia el mes.
El enriquecimiento se limita a esos asientos y no mueve el cursor global.

```bash
REINICIAR=si bash infra/local/sembrar-escenario-demo.sh
```

Reiniciar elimina los asientos del manifiesto y la copia/avisos del usuario exclusivo,
y recrea el escenario. Rechaza usuarios con asientos ajenos. No toca otros usuarios,
la siembra masiva, el cursor global, los modelos ni datos de producción.

## Evidencia

Cada corrida imprime JSON y escribe `DEMO-ESCENARIO-STATE.json` (o la ruta `ESTADO`).
Contiene usuario, fecha, IDs, conteos por categoría/origen/tipo de aviso y series maduras
recurrentes. Conserva el JSON de las dos corridas para pegarlo en la tarjeta.

Resultados esperados: 50 movimientos, 7 series maduras y tres avisos:
`caida_ingreso=1`, `cobro_nuevo=1`, `pago_no_alcanza=1`.
Categorías: alimentos 6, transporte 6, restaurantes 6, salud 6, entretenimiento 6,
comisiones 7 y sin categoría 13. Los 7 ingresos son clasificados por el léxico
existente como comisiones; no se cambia esa regla desde la siembra.

Prueba reproducible contra una base local de demo ya migrada:

```bash
go test -race -tags=integration ./cmd/semilla-escenario
```

La prueba comprueba dos corridas idénticas, reinicio con los mismos conteos y cursor
global intacto. Solo debe ejecutarse sobre una base desechable.

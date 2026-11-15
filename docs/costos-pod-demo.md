# Costos del pod de GPU para la demo

Tabla de costos de la tarjeta CF-A5-05. Las cifras de las tablas por escenario salen del análisis de
costos que resume el handoff `handoff-vera-2026-11-13.md` (sección 6, en el tablero de la tesis).
La sección "Precios vigentes y costo real (15 de noviembre de 2026)" agrega la oferta leída ese día y
el costo real de la campaña de capacidad, con su fuente.

## Fecha y vigencia de la cotización

- Cotización tomada del handoff del **13 de noviembre de 2026**. El handoff no registra una fecha de
  captura más precisa ni la página de precios de la que salió.
- **Hay que revalidar precio y stock antes de reservar.** El handoff dice que la 4090 suele estar en
  stock bajo, pero el 15 de noviembre de 2026 la Secure mostró stock alto. Las tarifas por hora pueden
  cambiar sin aviso. Si cambian, esta tabla deja de valer y se rehace con la
  tarifa nueva.
- El handoff dice que hoy no hay pods de RunPod (sección 6). Las cifras de las tablas de abajo no se
  han comprobado contra una factura. Solo el costo real de la campaña del 15 de noviembre de 2026
  (4,14 USD) viene de un saldo medido.

## Precios vigentes y costo real (15 de noviembre de 2026)

**La cotización de 0,34 USD/h Secure de este documento quedó desactualizada.** El 15 de noviembre de
2026 se leyó la oferta de RunPod con una consulta GraphQL de solo lectura (dato del encargo de
documentación, la consulta no se conserva en este repositorio) y se contrastó con la campaña de
capacidad (informe de la tesis `modelos-gemma/resultados-capacidad-2026-11-15.md`, sección 6).

| Oferta, RTX 4090 de 24 GB | Tarifa | Disponibilidad |
|---------------------------|--------|----------------|
| Secure | **0,89 USD/h** | Stock alto |
| Community | 0,34 USD/h | Crear un pod Community falló dos veces con `no instances currently available`, sin costo |

La tarifa de 0,34 USD/h que aparece en las tablas de abajo coincide con la de la oferta Community
actual [I], que ese día no tenía instancias (el handoff la rotulaba Secure, y esa rotulación no se pudo
reproducir). Con la tarifa Secure vigente, todas las cifras por hora de este documento se
multiplican por 0,89 en lugar de 0,34 (unas 2,6 veces).

Costo real de la campaña [V]:

| Concepto | Valor |
|----------|-------|
| Pod | RTX 4090 Secure, creado 06:32 UTC y terminado 11:11 UTC |
| Duración | 4,65 h |
| Gasto | **4,14 USD** (4,65 h x 0,89 USD/h), saldo de 11,455 a 7,315 USD |
| Seis horas habrían costado | 5,34 USD (6 h x 0,89 USD/h), por encima del tope de 5 USD |

El saldo de 7,315 USD y la hora de terminación son datos del encargo, no se leyeron de un archivo del
informe. Una campaña de medición no es una sesión de demo: el costo por hora de una sesión de demo en
Secure es el de la fila anterior.

## Responsable y umbral de gasto

| Campo | Valor |
|-------|-------|
| Responsable del umbral | Quien lidera la demo |
| Umbral de gasto mensual | **4,82 USD**, el total de dos sesiones de 4 h más el volumen de 30 GB |
| Qué pasa al superarlo | Se apaga el pod, se revisa el motivo y no se reserva otra sesión sin aprobación de quien lidera la demo |

**El umbral de 4,82 USD no aplica a una campaña de medición.** Para la campaña del 15 de noviembre de
2026 se usó un tope de presupuesto de 5 USD autorizado por Christian, y el gasto real fue de 4,14 USD.
Además, 4,82 USD se calculó con 0,34 USD/h, y con 0,89 USD/h dos sesiones de 4 h solas ya cuestan
7,12 USD (deducción: 8 h x 0,89). Si se quiere mantener un umbral para las sesiones de demo, hay que
recalcularlo con la tarifa vigente y que lo ratifique quien lidera la demo.

El umbral es el total de la recomendación del handoff (dos sesiones de 4 h en una 4090 Secure y un
volumen de 30 GB en el mismo mes). Queda **propuesto, a la espera de que quien lidera la demo lo
ratifique**. Es distinto del umbral de cero que `docs/operacion-demo.md` fija para pruebas con
proveedor real de IA: la demo con `VERA_PROVEEDOR=simulado` no gasta, y el pod sí.

## Tabla por escenario

Todas las cifras son USD y **las de GPU usan la tarifa de 0,34 USD/h del 13 de noviembre, que ya no es
la de la oferta Secure** (ver la sección de precios vigentes). Las que dicen "Cifra del handoff" están escritas en él (sección 6). Las que
dicen "Deducción" son aritmética mía sobre esas cifras y el handoff no las trae. La tarifa que usa esta
tabla para la 4090 es 0,34 USD/h (vigente el 13 de noviembre, hoy solo en Community).

| Escenario | GPU | Horas | Costo | Nota |
|-----------|-----|-------|-------|------|
| Una sesión de 4 h | 4090 Secure | 4 | 1,36 USD | Solo GPU, sin volumen |
| Dos sesiones de 4 h | 4090 Secure | 8 | 2,72 USD | Solo GPU, sin volumen |
| Volumen de 30 GB | no aplica | no aplica | 2,10 USD/mes | Se paga aunque no haya pod |
| Dos sesiones más volumen (recomendado) | 4090 Secure | 8 | **4,82 USD** | Es el umbral de arriba, valor aproximado según el handoff |
| Seis sesiones al mes | no indicada en el handoff | no indicadas en el handoff | 10,26 USD/mes | Cifra del handoff. Deducción: 6 x 4 h x 0,34 + 2,10 = 10,26, que supone 4090 Secure y 4 h por sesión, dos supuestos que el handoff no dice |
| 24/7 | no indicada en el handoff | todo el mes | **250,30 USD/mes** | Cifra del handoff, que dice no usarlo |
| Ventana de 86,7 h, 4090 Secure | 4090 Secure | 86,7 | 31,58 USD/mes | Tarifa 0,34 USD/h |
| Ventana de 86,7 h, A40 | A40 | 86,7 | 32,45 USD/mes | Tarifa 0,35 USD/h |
| Ventana de 86,7 h, L4 | L4 | 86,7 | 40,25 USD/mes | Tarifa 0,44 USD/h |
| Ventana de 86,7 h, A100 | A100 | 86,7 | 122,61 USD/mes | Tarifa 1,39 USD/h |
| Scale-to-zero | no indicada en el handoff | 3,33 | 3,23 USD/mes | Cifra del handoff. Deducción: 3,33 x 0,34 + 2,10 = 3,23, que supone 4090 Secure |

Cómo leer la tabla:

- Las cuatro cifras de la ventana de 86,7 h están en el handoff en el orden 4090, A40, L4 y A100. El
  handoff no dice qué cubre la ventana, pero las cuentas cierran con la tarifa por hora multiplicada
  por 86,7 más los 2,10 USD del volumen (por ejemplo, 0,34 por 86,7 más 2,10 da 31,58). Es una
  comprobación aritmética sobre cifras del handoff, no un dato nuevo.
- Las cuentas de seis sesiones y de scale-to-zero también cierran con la tarifa de la 4090 más el
  volumen, pero es una deducción: el handoff no dice qué GPU ni cuántas horas por sesión usan. Tampoco
  desglosa de dónde sale la ventana de 86,7 h ni el cálculo del scale-to-zero. Las cifras se citan
  tal cual.
- En prosa la lectura es esta: la recomendación cuesta unos 5 USD al mes, seis sesiones unos 10, y
  dejar el pod encendido todo el mes unos 250, que es la razón por la que no se hace.

## Qué escenario se recomienda

Según el handoff: un pod 4090 Secure con E4B, dos sesiones de 4 h, volumen de 30 GB y `Terminate`
entre sesiones, con plan B en L4 o A40 por si la 4090 no tiene stock. El handoff también dice no usar el
24/7.

Esa recomendación se calculó con 0,34 USD/h. Con la tarifa Secure vigente (0,89 USD/h) hay que
recalcularla antes de reservar. Además, el pod no puede usar Docker ni Compose (ver
`docs/pila-modelos-pod.md`), así que la pila corre como procesos nativos.

## Lo que esta tabla no cubre

- Tráfico de red, almacenamiento extra, impuestos o cargos de la cuenta: el handoff no los cuantifica.
- Cualquier sesión que se alargue más de las 4 h previstas. El costo por hora extra es la tarifa
  de la GPU elegida, así que una sesión olvidada de la 4090 Secure suma 0,89 USD cada hora (la tarifa vigente el 15 de
  noviembre de 2026, no los 0,34 USD de la cotización original).
- La alerta de presupuesto que vigila el umbral: está descrita en `docs/operacion-demo.md` (sección
  "Pod de GPU"). La alerta de la cuenta sigue sin probarse. Lo que sí cortó el gasto en la campaña fue
  una guardia local con temporizador de systemd, una vigía local y el tope de presupuesto en la
  creación del pod (dato del encargo).

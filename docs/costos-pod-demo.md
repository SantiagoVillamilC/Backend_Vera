# Costos del pod de GPU para la demo

Tabla de costos de la tarjeta CF-A5-05. Todas las cifras salen del análisis de costos que resume
el handoff `handoff-vera-2026-11-13.md` (sección 6, en el tablero de la tesis). No hay otra fuente
de precios en este repositorio y aquí no se agregó ninguna cifra propia.

## Fecha y vigencia de la cotización

- Cotización tomada del handoff del **13 de noviembre de 2026**. El handoff no registra una fecha de
  captura más precisa ni la página de precios de la que salió.
- **Hay que revalidar precio y stock antes de reservar.** La 4090 suele estar en stock bajo, y las
  tarifas por hora pueden cambiar sin aviso. Si cambian, esta tabla deja de valer y se rehace con la
  tarifa nueva.
- El handoff dice que hoy no hay pods de RunPod (sección 6). Ninguna cifra de esta página se ha
  comprobado contra una factura.

## Responsable y umbral de gasto

| Campo | Valor |
|-------|-------|
| Responsable del umbral | Quien lidera la demo |
| Umbral de gasto mensual | **4,82 USD**, el total de dos sesiones de 4 h más el volumen de 30 GB |
| Qué pasa al superarlo | Se apaga el pod, se revisa el motivo y no se reserva otra sesión sin aprobación de quien lidera la demo |

El umbral es el total de la recomendación del handoff (dos sesiones de 4 h en una 4090 Secure y un
volumen de 30 GB en el mismo mes). Queda **propuesto, a la espera de que quien lidera la demo lo
ratifique**. Es distinto del umbral de cero que `docs/operacion-demo.md` fija para pruebas con
proveedor real de IA: la demo con `VERA_PROVEEDOR=simulado` no gasta, y el pod sí.

## Tabla por escenario

Todas las cifras son USD. Las que dicen "Cifra del handoff" están escritas en él (sección 6). Las que
dicen "Deducción" son aritmética mía sobre esas cifras y el handoff no las trae. La tarifa de la 4090
Secure es 0,34 USD/h.

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

## Lo que esta tabla no cubre

- Tráfico de red, almacenamiento extra, impuestos o cargos de la cuenta: el handoff no los cuantifica.
- Cualquier sesión que se alargue más de las 4 h previstas. El costo por hora extra es la tarifa
  de la GPU elegida, así que una sesión olvidada de la 4090 suma 0,34 USD cada hora.
- La alerta de presupuesto que vigila el umbral: está descrita en `docs/operacion-demo.md` (sección
  "Pod de GPU") y **no está probada**, porque aún no hay pod.

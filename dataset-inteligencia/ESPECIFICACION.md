# Especificación del dataset de inteligencia

Corpus sintético para evaluar dos retos de la billetera: (1) la categorización de comercios
desconocidos por aprendizaje colectivo con umbral K=2 y (2) los avisos de hábitos de consumo.
Sin este corpus no hay contra qué medir ni K=2 ni los avisos. Versión del generador: 1.4.0.

Todo sale de `random.Random(semilla)` y de `uuid5` sobre un espacio de nombres fijo. No hay `uuid4`
ni hora del sistema. Misma semilla y mismos parámetros dan archivos idénticos byte a byte.

## 1. Uso y salida

```
python3 generar.py --filas N [--semilla S] [--variante A|B] [--usuarios U] [--tasa-correccion 0.35]
                   [--ruido-nombre 0.30] [--tasa-etiqueta-erronea 0.08] [--fecha-fin 2026-10-21]
                   [--colusion-comercios N] [--colusion-tamanos 2,3] [--salida RUTA]
```

- `--filas` es el número exacto de filas de `entrada/movimientos.csv`, sin contar el encabezado.
  Los escenarios fijos cuentan dentro de ese total. Si N es menor que el mínimo (hoy 391), falla con
  un mensaje que dice el mínimo.
- `--filas` escala solo la población de fondo. `--usuarios` fija cuántos usuarios de fondo hay
  (por defecto `max(5, filas_de_fondo // 40)`, recortado a las filas disponibles).
- `--fecha-fin` va de 2026-10-20 a 2026-12-31 y por defecto vale 2026-10-21. El mínimo es el 20 de
  octubre porque el perfil 4 necesita datos hasta ese día. Ninguna fila de `entrada/` (movimientos ni
  correcciones) es posterior a `--fecha-fin`: el máximo `ocurrido_en` es menor o igual que
  `<fecha-fin>T23:59:59Z`. Semilla por defecto 2026.
- `--colusion-comercios N` (entero mayor o igual a 0, por defecto 0) es el número de comercios envenenados
  por colusión POR CADA tamaño de grupo. `--colusion-tamanos` (por defecto `2,3`) es la lista de tamaños,
  separados por comas, y solo admite 2 y 3 sin repetir. Con N igual a 0 la salida es idéntica byte a byte a
  la anterior a estas opciones, salvo `manifiesto.json`, que solo suma los dos parámetros nuevos. Detalle en
  la sección 11b.
- Una carpeta por corrida: `<salida>/<semilla>-<filas>-<variante>/` con `manifiesto.json`,
  `entrada/` y `verdad/`. La salida por defecto es `dataset-inteligencia/salida/`, ignorada por git.

`manifiesto.json` guarda los parámetros, la versión del generador, el conteo de filas de cada
archivo y el sha256 de cada archivo.

## 2. Qué ve el sistema y qué es la verdad

`entrada/` es lo que el sistema vería. `verdad/` es lo único que sirve para evaluar y nunca debe
cargarse al sistema. Ningún encabezado de `entrada/` aparece en `verdad/`, salvo las llaves de
unión `movimiento_id`, `usuario_id`, `correccion_id` y `llave_destino`. Hay una excepción
documentada: `ocurrido_en` también aparece en `verdad/casos_vista.csv`, porque ese archivo
describe lo que se ve en un instante concreto (ver Supuestos).

### entrada/movimientos.csv

| Columna | Valor y fuente |
|---------|----------------|
| `movimiento_id` | Entero creciente desde 1, en orden de `ocurrido_en` (empates por orden de creación) |
| `usuario_id` | UUID v5 de `semilla` y la clave del usuario |
| `transferencia_id` | UUID v5 del grupo de la transferencia. Vacío en depósitos. El GMF y la devolución comparten el de su envío |
| `ocurrido_en` | UTC ISO 8601 con Z. Las horas se definen en Colombia (UTC-5, sin horario de verano) y se convierten |
| `direccion` | `entrada` o `salida` |
| `monto_centavos` | Entero positivo, pesos por 100 |
| `moneda` | Siempre `COP` |
| `tipo_asiento` | `deposito`, `retencion`, `transferencia_interna`, `despacho_riel`, `liquidacion_confirmada`, `compensacion` (ver sección 6) |
| `descripcion` | Texto del movimiento (`Pago`, `Salario`, `Claro internet`, `GMF 4x1000`, etc.) |
| `tipo_llave` | `documento`, `celular`, `correo`, `alfanumerico`, `comercio`. Vacío si no hay llave |
| `llave_destino` | Llave de destino. Vacía en depósitos y GMF |
| `nombre_comercio_crudo` | Según la variante (sección 8) |
| `mcc` | Según la variante (sección 8) |
| `canal` | `llave` o `qr` |

### entrada/correcciones.csv

`correccion_id` (UUID v5), `usuario_id`, `llave_destino`, `nombre_puesto`, `categoria_puesta`,
`ocurrido_en`. El `nombre_puesto` es el nombre CRUDO que tecleó el usuario, con variantes de
mayúsculas, minúsculas, mixtos, guiones bajos, guiones y espacios, más tildes, abreviaturas y
tipeo según `--ruido-nombre`. El archivo va ordenado por `ocurrido_en` y cada corrección es
posterior al primer pago de ese usuario a esa llave.

### verdad/

| Archivo | Columnas | Contenido |
|---------|----------|-----------|
| `empresas.csv` | `empresa_id, razon_social, ficticia` | Solo 5 empresas reales (ver sección 5). Todas las demás son inventadas y `ficticia=true` |
| `comercios.csv` | `llave_destino, clase_llave, empresa_id, nombre_canonico, categoria, mcc_categoria, es_comercio, nombre_revela_naturaleza, conjunto` | Una fila por llave de destino (comercios y personas). Estado vigente al final del periodo |
| `etiquetas.csv` | `movimiento_id, categoria_real, llave_destino, es_persona` | Una fila por movimiento |
| `correcciones_verdad.csv` | `correccion_id, es_correcta, tipo_ruido, nombre_puesto_canonico` | Una fila por corrección |
| `avisos_esperados.csv` | `usuario_id, tipo, fecha_evaluacion, debe_disparar_con_mediana, debe_disparar_con_media, detalle` | Avisos que deben o no deben dispararse |
| `nombres_historial.csv` | `llave_destino, nombre_canonico, categoria, vigente_desde, vigente_hasta` | Historial de nombre y categoría de cada comercio |
| `casos_vista.csv` | `caso_id, usuario_id, movimiento_id, ocurrido_en, nombre_esperado, categoria_esperada, origen_esperado` | Lo que debe ver cada usuario en los casos 2 y 3 |
| `series.csv` | `serie_id, usuario_id, llave_destino, es_recurrente, cadencia_dias, motivo, detalle` | Etiquetas de recurrencia: 8 series de los escenarios fijos y las series de la población de fondo (ver sección 10) |

Detalles de cada tabla:

- `etiquetas.csv`: los pagos a personas llevan `categoria_real=sin_categoria` y `es_persona=true`.
  El GMF es un movimiento propio con `categoria_real=comisiones_costos_financieros` y `llave_destino`
  vacía. Los depósitos llevan su categoría de ingreso. La categoría de un pago a un comercio que
  cambió de categoría es la vigente en la fecha del pago.
- `correcciones_verdad.csv`: `es_correcta=true` si `categoria_puesta` coincide con la categoría real
  de la llave en la fecha de la corrección. `tipo_ruido` es `ninguno`, `mayusculas`, `tildes`,
  `abreviatura`, `tipeo`, `etiqueta_erronea` o `colusion`. El ruido de formato (minúsculas, mixto,
  guion bajo, guion, espacios) se registra como `mayusculas`. Si una corrección tiene categoría
  errónea, el tipo es `etiqueta_erronea` aunque el nombre también tenga ruido.
- `nombre_revela_naturaleza` es `true` cuando el nombre del comercio se construyó con la plantilla
  "tipo de negocio más nombre propio" (por ejemplo `DROGUERIA AURORA SUBA`, `TIENDAS D1 SUBA`) y
  `false` cuando es una marca o nombre propio sin pistas (por ejemplo `KALIMBA SUBA`, `D1 CHAPINERO`).
  La etiqueta sale de CÓMO el generador construyó el comercio y nunca de comparar el texto contra la
  lista de palabras clave de ningún detector. Las personas llevan `false`. Regla de construcción
  (sección 5): cada empresa ficticia nueva usa la plantilla que lleve menos sucursales hasta ese
  momento, así que el catálogo queda cerca de la mitad y la mitad (en las corridas de 391, 1.200,
  3.000 y 20.000 filas dio 51 %, 51 %, 51 % y 50 % de `true`).
- `conjunto` es `entrenamiento` o `prueba` y solo lo llevan los comercios (`es_comercio=true`). Las
  personas llevan el valor vacío. El 30 % de los comercios va a `prueba`, elegido de forma
  determinista por semilla (un generador derivado de `random.Random(semilla)`, no por posición) y
  estratificado por `nombre_revela_naturaleza`: el total de prueba (30 % del total, redondeado) se
  reparte entre los dos estratos por el método del mayor resto y dentro de cada estrato se escoge al
  azar. Así la proporción de `true` en prueba y en entrenamiento difiere en menos de 5 puntos. Con
  3.000 filas (130 comercios) quedan 39 en prueba (30 %) y la diferencia de balance es de 0,7 puntos
  con la semilla 2026 (0,6 con la semilla 1 y 1,3 con la semilla 7). La partición es igual en las variantes A y B.
- `nombre_puesto_canonico` es `canonizar(nombre_puesto)` y permite contar K=2 sobre la clave
  normalizada. Los nombres con typos o abreviaturas (`SUPERMERC DON PEPE`) dan claves distintas,
  que es justo lo que mide la agrupación por similitud.
- `nombres_historial.csv`: fechas locales de Colombia, ambos extremos inclusivos, sin solapes. Todo
  comercio tiene al menos una fila. Los del caso 3 (cambio de nombre) tienen dos. Las personas no
  aparecen. `vigente_hasta` vacío significa que sigue vigente.
- `casos_vista.csv`: solo para los casos nombrados 2 y 3. `nombre_esperado` va canonizado (la
  interfaz lo muestra con `mostrar`). `origen_esperado` es `privado`, `global`, `crudo` o `curado`
  (este último se reserva para nombres de un catálogo curado y no se usa en esta versión).
- `series.csv`: `es_recurrente` sale de la intención con la que el generador creó la serie, nunca de
  un umbral numérico (ver sección 10). `motivo` describe cómo se generó: `regular`, `dia_variable`,
  `monto_variable_servicio_publico`, `compra_habitual` o `pago_suelto`. `cadencia_dias` va vacía si la
  serie no es recurrente.

## 3. Catálogo de categorías y MCC

14 categorías más `sin_categoria`: `salario`, `transferencia_recibida`, `reembolso`,
`otros_ingresos`, `alimentos_mercado`, `restaurantes_domicilios`, `transporte`,
`vivienda_servicios`, `salud`, `educacion`, `entretenimiento`, `compras`,
`comisiones_costos_financieros`, `otros_gastos`, `sin_categoria`.

El MCC es un código ISO 18245 que el usuario NO edita. El mapeo de categoría a lista de códigos es
una decisión del equipo (la fuente de los códigos es el estándar ISO 18245, el mapeo no la tiene):

| Categoría | MCC |
|-----------|-----|
| alimentos_mercado | 5411, 5499 |
| restaurantes_domicilios | 5812, 5813, 5814 |
| transporte | 4111, 4121, 4131, 5541, 5542 |
| vivienda_servicios | 4814, 4899, 4900 |
| salud | 5912, 8011, 8062, 8099 |
| educacion | 8211, 8220, 8299, 5942 |
| entretenimiento | 7832, 7922, 5815, 5816, 5817, 5818 |
| compras | 5311, 5310, 5651, 5691, 5732, 5964 |

Las demás categorías no tienen MCC. Los comercios solo existen en las 8 categorías con MCC. El MCC
de una fila sale siempre de la categoría real, escogiendo de la lista por los 3 últimos dígitos de
la llave. Nunca se atribuye un MCC real a una marca real.

## 4. Llaves

Se genera SOLO la intersección entre la norma del Banco de la República y los patrones del código,
para poder cargar el dataset al sistema real:

| Tipo | Patrón generado |
|------|-----------------|
| comercio | `^00[0-9]{8}$` |
| celular | `^3[0-9]{9}$` |
| alfanumerico | `^@[A-Za-z0-9]{5,20}$` |
| documento | `^[0-9]{6,10}$` |
| correo | `^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}$` con hasta 30 caracteres antes de la @ y 61 después |

### Hallazgo: norma frente a código (no se ejercita en los datos)

Los patrones del código están en `Backend_Centinela/internal/nucleo/directorio.go:34` a `:38`. La
columna de la norma sale del encargo de esta tarea, no se contrastó aquí con el texto de la norma.

| Tipo | Norma | Código | Efecto |
|------|-------|--------|--------|
| alfanumerico | 5 a 20 caracteres | `{1,64}` (línea 37) | El código acepta llaves que la norma rechaza |
| documento | hasta 18 alfanuméricos | `^[0-9]{6,10}$` (línea 34) | El código rechaza documentos válidos para la norma |
| comercio | `^00[0-9]{8}$` | `^[A-Za-z0-9]{6,20}$` (línea 38) | El código acepta casi cualquier cosa |

La llave de comercio de ejemplo `TIENDAD1BOG01` NO cumple la norma y no aparece en ningún archivo.
El código sí la aceptaría (13 caracteres alfanuméricos), por eso el código solo no sirve para
validar llaves de comercio.

## 5. Empresas, comercios y personas

- Empresas reales, y solo estas: D1, Éxito, Ara, Alkosto y Olímpica, cadenas de retail con fuente
  pública (sitio oficial de cada cadena). `razon_social` es el nombre comercial público, no la razón
  social legal, para no inventarla. Cuatro van en `alimentos_mercado` y Alkosto en `compras` (decisión
  del equipo, el MCC sale de la categoría).
- Todas las demás empresas son inventadas (`ficticia=true`) y se construyen con una de dos plantillas.
  Plantilla "tipo de negocio más nombre propio" (`Droguería Aurora S.A.S.`, `nombre_revela_naturaleza=true`)
  o plantilla "marca inventada" hecha de un prefijo y un sufijo sin significado (`Kalimba S.A.S.`,
  `nombre_revela_naturaleza=false`). Un parecido con un negocio real sería casual.
- En las cadenas reales cada sucursal declara su plantilla: `TIENDAS D1 SUBA`, `TIENDAS ARA ENGATIVÁ` y
  `SUPERTIENDAS OLÍMPICA IBAGUÉ` anteponen una palabra de naturaleza (`true`), el resto son marca más zona
  (`false`).
- Una empresa tiene varias llaves de comercio, una por sucursal, con nombre por sucursal (por
  ejemplo `D1 CHAPINERO`, `TIENDAS D1 SUBA`).
- Personas: nombres inventados con llave de celular, correo, alfanumérica o documento. Su
  `categoria` es `sin_categoria` y `es_comercio=false`.
- La llave propia de cada usuario (celular) nunca es la contraparte de sus envíos.

## 6. Movimientos

Cada fila afecta el saldo. El saldo es la suma de entradas menos la suma de salidas.

| tipo_asiento | Uso |
|--------------|-----|
| `deposito` | Entrada de dinero (salario, ingreso, saldo inicial) |
| `liquidacion_confirmada` | Pago confirmado a un comercio |
| `transferencia_interna` | Envío a una persona con llave de celular o correo |
| `despacho_riel` | Envío a una persona con llave alfanumérica o documento, y envío indeterminado |
| `retencion` | Envío retenido que luego falla, y filas de GMF |
| `compensacion` | Devolución del envío no completado |

GMF: 4 por mil sobre el monto completo de un envío que pasa del monto exento por envío (100.000
pesos). Es una fila propia del mismo `transferencia_id`, con descripción `GMF 4x1000`, sin llave ni
nombre. En un grupo con devolución, la compensación devuelve el envío más el GMF, y el grupo suma
cero. Quien consuma los datos debe netear por `transferencia_id`.

## 7. Escenarios fijos

Las cifras son exactas, en pesos (el CSV va por 100). Cubren abril a septiembre de 2026 más octubre
hasta `--fecha-fin` (por defecto el 21 de octubre). Ninguna fila ni aviso de un escenario es posterior a
`--fecha-fin`: el perfil 4 llega al 20 de octubre, que es el motivo por el que el corte mínimo es esa fecha.

| Perfil | Contenido | Aviso esperado |
|--------|-----------|----------------|
| 1, ingreso estable | Restaurantes 300.000 al mes (3 pagos de 100.000), julio 2.400.000 en un solo pago (boda), octubre 600.000 al corte | `categoria_sobre_media` y `presupuesto_anticipado`: mediana true, media false (la media de 6 meses es 650.000, inflada por julio) |
| 2, ingreso que cae | Ingreso 1.000.000 y gasto 800.000 al mes, agosto con solo 600.000. Internet de 89.900 el día 5. Salario del 30 de septiembre a las 21:30 hora Colombia, que en UTC cae el 1 de octubre 02:30 | `caida_ingreso` el 2026-08-31, `pago_no_alcanza` el 2026-10-03, y `presupuesto_anticipado` y `categoria_sobre_media` (compras) el 2026-10-02, todos true |
| 3, ingreso irregular | Ingreso entre 400.000 y 1.200.000. Claro internet 89.900 el día 5 de abril a julio, el día 9 en agosto y 94.900 el día 5 de septiembre | `cobro_nuevo`, `pago_no_alcanza` y `caida_ingreso` con false. La serie es una sola |
| 4, presupuesto anticipado | Promedio de 1.200.000 al mes de abril a septiembre, todo en un mismo supermercado, octubre acumula 1.200.000 hasta el día 19 y 1.300.000 el día 20 | `presupuesto_anticipado` y `categoria_sobre_media` el 2026-10-20 (mediana 1.250.000 y media dan true) |
| 5, historial corto | Usuario creado el 15 de agosto con un gasto de octubre que lo dispararía con historial | `categoria_sobre_media` y `presupuesto_anticipado` false, detalle `historial insuficiente` |

Regla de referencia de los avisos de gasto (la verifica `tests/test_avisos.py` contra los datos):

- Gasto: salidas por mes local de Colombia, sin GMF ni envíos a personas.
- Meses previos: meses completos (que empiezan en o después del primer movimiento del usuario) anteriores al
  mes de `fecha_evaluacion`. Con menos de 3 no se evalúa y no dispara.
- `presupuesto_anticipado`: el gasto total acumulado del mes hasta `fecha_evaluacion` supera la mediana
  (columna `con_mediana`) o la media (columna `con_media`) del gasto mensual de los meses previos.
- `categoria_sobre_media`: lo mismo por categoría, con 3 o más meses previos con gasto en ella. Una
  categoría sin historial (por ejemplo el GMF del perfil 2) no se evalúa.
- `fecha_evaluacion` es una fecha local de Colombia, evaluada al final del día. Todo salario de fin de mes del
  perfil 2 (21:30 hora Colombia) cae al día siguiente en UTC.
- Solo los perfiles nombrados y los casos de movimiento traen avisos. Los usuarios de fondo y los de los
  casos de multitud no están etiquetados y no deben puntuarse.

Perfil 2, saldo calculado: el salario llega el último día del mes, hay un saldo inicial de 800.000
el 1 de abril, y el 2 de octubre una compra cuyo monto se CALCULA para dejar el saldo en 36.000 pesos
(el monto más su GMF). El saldo proyectado al 5 de octubre queda por debajo de 89.900 y el aviso
dispara. El pago de internet del 5 de octubre no se genera: es lo que el aviso anticipa. El perfil 2
no tiene filas después del 2 de octubre y su aviso `pago_no_alcanza` se evalúa el 2026-10-03, una fecha
anterior a `--fecha-fin`, así que el aviso mira hacia adelante sin salirse de la ventana.

Casos de movimiento (usuario aparte):

- Envío de 200.000 el 5 de septiembre con GMF de 800 que falla. Se devuelve el 6. El grupo suma cero.
- Envío de 150.000 el 30 de septiembre a las 18:00 hora Colombia (23:00 UTC), GMF de 600, queda
  indeterminado y se devuelve el 1 de octubre.
- La contraparte nunca es el mismo usuario.

El 8 % de los pagos a comercio llega con nombre vacío (variante A), en todos los perfiles.

## 8. Variantes del nombre crudo

**Variante A (la realidad de hoy):** canal siempre `llave`, `mcc` siempre vacío. En pagos a comercio
`nombre_comercio_crudo` es la razón social enmascarada, el texto genérico `Pago a comercio` (25 %), o
queda vacío (8 %). En pagos a personas es el nombre enmascarado.

Enmascarado: se conservan letras en los extremos de cada palabra y el medio se reemplaza por X. Para
palabras de 6 o más letras se conservan 2 al inicio y 1 al final, para 5 letras 1 y 2, para 3 y 4
letras 1 y 1, y las de 1 o 2 letras no cambian. Ejemplo: `Andrea Pérez` da `AnXXXa PXXez`.

**Variante B (QR):** una fracción de los pagos a comercio (parámetro interno `fraccion_qr`, 60 %)
llega por canal `qr` con `nombre_comercio_crudo` de máximo 25 caracteres (campo 59 del QR, con
ruido: mayúsculas sin tildes, abreviaturas, ciudad pegada, truncamiento) y `mcc` de 4 dígitos
derivado de la categoría real. El nombre del QR es el que el comercio tenía en la fecha del pago, según
`nombres_historial.csv` (antes del 15 de julio `TIENDA DOÑA LUCY` aparece como `MINIMERCADO LA ESQUINA`).
El resto sigue como en A.

La verdad (comercios, etiquetas, correcciones, historial, casos, series y avisos) es idéntica entre A
y B para la misma semilla. La presentación de cada fila usa su propio generador de azar.

## 9. Normalización de nombres: canonizar y mostrar

`canonizar(nombre)` es una función pura del paquete `generador` (módulo `nombres`). Reglas en este
orden:

1. Pasar a mayúsculas.
2. Reemplazar cualquier secuencia de espacios, guion bajo o guion por un solo guion.
3. Quitar guiones al inicio y al final.
4. Quitar tildes, salvo la Ñ.
5. Eliminar los signos `'`, `.` y `&`.
6. Recortar a 40 caracteres.

Es idempotente: `canonizar(canonizar(x)) == canonizar(x)`. Para garantizarlo en casos de borde
(por ejemplo `A -. B`, donde quitar el punto deja un guion doble) las reglas se repiten hasta que el
resultado deja de cambiar. Ejemplos: `SUPERMERCADO_don_pepe`, `supermercado don pepe` y
`SUPERMERCADO-DON-PEPE` dan `SUPERMERCADO-DON-PEPE`. `McDonald's` da `MCDONALDS`.

`mostrar(nombre_canonico)` reemplaza guiones por espacios y es solo para la interfaz: no se guarda.

`verdad/comercios.csv` y `verdad/nombres_historial.csv` guardan `nombre_canonico` ya canonizado.

## 10. Identidad de un comercio y recurrencia

Jerarquía de identidad, de mayor a menor confianza:

1. La llave de comercio Bre-B de 10 dígitos que empieza en `00`, o el Merchant ID del QR (subcampo
   05 del tag 26).
2. La cuenta interna, si el comercio es usuario de la billetera.
3. El nombre canónico normalizado, como llave débil: puede compartirse entre comercios distintos.

El MCC nunca es identidad. Es un código de categoría de 4 dígitos que agrupa muchos comercios, así
que solo se registra como atributo de categoría.

Para decidir si una serie de pagos de un usuario es recurrente, la verdad de este dataset asume:

- La identidad es el discriminante duro: los pagos deben ir a la misma identidad (nivel 1 o 2).
- El monto es un discriminante medio: estable o con variación pequeña.
- La fecha es un discriminante blando: tolera corrimientos de unos pocos días.

El generador NO implementa un detector de recurrencia. Solo etiqueta la verdad en `series.csv`.

### Regla de etiquetado: la intención, no un umbral

`es_recurrente` sale de la INTENCIÓN con la que el generador creó la serie. Nunca se calcula
comparando contra un umbral numérico, porque ese umbral es un parámetro del detector que se va a
calibrar y la verdad no puede depender de él.

- **Recurrente por diseño:** suscripciones y facturas mensuales con día variable (más o menos 2 a 5
  días) y alzas de precio como eventos puntuales. Incluye servicios públicos (luz, agua) con monto que
  varía hasta más o menos 30 % entre meses: son recurrentes de verdad aunque el monto cambie bastante.
- **No recurrente por diseño:** compras habituales en el mismo comercio (por ejemplo un supermercado
  cada semana con montos aleatorios) y pagos sueltos como el caso de telefonía de 20.000, 25.000 y
  50.000 en días distintos.
- `motivo` describe cómo se generó la serie y no es el resultado de una comparación:

| motivo | Cómo se generó | es_recurrente |
|--------|----------------|---------------|
| `regular` | Mismo monto y día fijo (con un corrimiento o un alza puntual si el caso lo pide) | true |
| `dia_variable` | Mismo monto, día que se corre unos pocos días | true |
| `monto_variable_servicio_publico` | Factura mensual de servicio público con monto que cambia mes a mes | true |
| `compra_habitual` | Compras repetidas en el mismo comercio con montos aleatorios | false |
| `pago_suelto` | Pagos sueltos a la misma llave en días y montos distintos | false |

| Serie | es_recurrente | motivo |
|-------|---------------|--------|
| Internet del perfil 2 (89.900, día 5) | true | regular |
| Claro del perfil 3 (día 5, un mes día 9, alza de 5,6 % en septiembre) | true | regular, con el alza en `detalle` |
| Arriendo mensual de 1.200.000 a una llave de persona | true | regular |
| Telefonía: 20.000 el 10 de enero, 25.000 el 20 de febrero y 50.000 el 20 de marzo a la misma llave | false | pago_suelto |
| Telefonía de 59.900 siempre alrededor del día 20 (más o menos 2 días) | true | dia_variable |
| Luz el día 12 con monto entre 109.000 y 142.000 (cambios de hasta 23 % entre meses consecutivos) | true | monto_variable_servicio_publico |
| Agua el día 12 con monto casi igual | true | monto_variable_servicio_publico |
| Supermercado cada 5 a 9 días con 20 montos distintos, sin patrón repetido (20 compras) | false | compra_habitual |

En la variante B los pagos de telefonía van por QR con MCC 4814. Esa es una pista de categoría y no de
identidad: el pago suelto comparte llave con el plan que paga cerca del día 20, y lo que los distingue
es el patrón de monto y fecha.

### Series de la población de fondo

Con 8 series no se puede calibrar ni validar un detector (precisión, recall y F1 serían ruido). Por eso
la población de fondo genera series etiquetadas que escalan con `--filas`. Los 8 escenarios fijos de la
tabla anterior no cambian. Las series de fondo se reconocen porque su `detalle` empieza con
`Serie de fondo`.

- Presupuesto: hasta el 40 % de las filas de fondo se dedica a series. Se crean series hasta agotarlo y el
  resto de las filas sigue siendo gasto aleatorio. El total de filas sigue siendo exacto.
- Cobertura: las series se reparten entre la primera mitad de los usuarios de fondo (redondeada hacia
  arriba), así que al menos el 40 % de esos usuarios tiene una o más series. Una prueba lo verifica.
- Mezcla: cada ciclo de 10 series trae 5 recurrentes y 5 no recurrentes por diseño, en orden aleatorio:

| Tipo de serie | es_recurrente | motivo | Cómo se genera |
|---------------|---------------|--------|----------------|
| Suscripción | true | regular | Mismo monto (de 19.900 a 59.900) en un día fijo del mes, de 4 a 7 pagos |
| Suscripción con alza | true | regular | Igual, con un alza puntual de 5 % a 12 % desde un cobro intermedio (dos montos distintos) |
| Telefonía | true | dia_variable | Mismo monto, día base de 8 a 22 con corrimientos de más o menos 2 a 4 días |
| Servicio público | true | monto_variable_servicio_publico | Día fijo, monto que cambia cada mes entre -22 % y +29 % respecto al anterior |
| Arriendo | true | dia_variable | Pago a una llave de persona, mismo monto de 400.000 a 1.200.000, día variable |
| Compra habitual | false | compra_habitual | Entre 8 y 12 compras en el mismo supermercado cada 5 a 9 días, cada una con un monto distinto |
| Pago suelto | false | pago_suelto | Tres pagos al mismo comercio con 18 a 70 días de separación y montos distintos |
| Casualidad | false | pago_suelto | Dos pagos del mismo monto con 28 a 31 días de diferencia. Parecen recurrentes pero no lo son. El `detalle` dice `por casualidad` |

- Una pareja usuario y llave pertenece a una sola serie. Mientras existe, el gasto aleatorio de ese usuario
  no paga a esa llave, así que los pagos de la serie son exactamente los etiquetados.
- Los pagos de la serie son de a lo sumo 100.000 pesos salvo arriendos y algunos servicios públicos, que
  llevan sus filas de GMF.
- Las series de varios usuarios al mismo comercio arrancan en meses distintos, para conservar el
  escalonamiento en el tiempo (sección 11).
- La verdad sigue siendo la intención de diseño: ni la proporción de recurrentes ni ningún motivo se
  calculan comparando contra un umbral.

Con `--filas 3000` quedan 164 series (84 recurrentes, 51 %): 35 `regular`, 32 `dia_variable`, 17
`monto_variable_servicio_publico`, 32 `compra_habitual` y 48 `pago_suelto`. Con 1.200 filas quedan 56 y con
6.000 filas 345.

## 11. Casos de multitud

1. **Consistencia con K>=2:** una panadería ficticia con dos sucursales. Tres usuarios corrigen
   una sucursal y dos la otra, con nombres equivalentes tras `canonizar` y la categoría correcta.
2. **Arrendadora:** la llave de celular de una arrendadora. Dos usuarios la etiquetan igual como
   `vivienda_servicios`, alcanza 2, pero `es_comercio=false` y `es_persona=true`.
3. **Colusión:** dos usuarios etiquetan mal el mismo comercio con la misma categoría
   (`tipo_ruido=colusion`) antes de que lo hagan los usuarios honestos.
4. **Nombres parecidos:** `RUTA VELOZ` (transporte) y `RUTA VELOZ COMIDAS` (restaurantes) son
   comercios y empresas distintos. Se usan nombres inventados porque Uber no es una de las 5 empresas
   reales permitidas.
5. **GMF:** las filas de GMF no tienen llave y no se pueden categorizar por usuario.

Los pagos de usuarios distintos a un mismo comercio van separados por semanas o meses dentro de la
ventana. Las correcciones aleatorias de la población de fondo siguen `--tasa-correccion`,
`--ruido-nombre` y `--tasa-etiqueta-erronea`.

### 11b. Población de colusión (`--colusion-comercios`)

El caso 3 de arriba es un único comercio con dos colusores. Para medir el envenenamiento según K en
{1, 2, 3} con significancia hace falta una población. Con `--colusion-comercios N` y `--colusion-tamanos`
se crean, por cada tamaño T de la lista, N comercios nuevos con este guion:

- Comercio nuevo con empresa ficticia, `nombre_base`, categoría con MCC (se reparten en rotación entre las
  8 categorías de comercio), `es_comercio=true`, fila en `comercios.csv` y en `nombres_historial.csv`,
  conjunto de entrenamiento o prueba por la regla de partición existente, y `nombre_revela_naturaleza`
  alternando la plantilla de construcción como el resto del catálogo. No entra a la población de fondo ni a
  las correcciones aleatorias, para conservar el guion.
- T usuarios nuevos coludidos pagan al comercio y lo corrigen con el MISMO nombre y la MISMA categoría
  equivocada (`tipo_ruido=colusion`, `es_correcta=false`). Sus pagos caen entre el 5 de abril y mediados de
  junio, y entre un colusor y el siguiente pasan de 1 a 5 días.
- Entre 21 y 45 días después del último colusor, 3 usuarios honestos distintos y nuevos pagan y corrigen con
  la categoría real (`tipo_ruido` `ninguno` o `mayusculas`, `es_correcta=true`), separados entre 5 y 20 días.
  El nombre canoniza igual que el del comercio. La verdad final es la categoría real.
- Consecuencia: un K menor o igual a T se envenena (los colusores llegan a K votos primero) y un K mayor que T
  no (los honestos llegan a K primero, porque son 3).
- Los colusores y los honestos son usuarios sintéticos nuevos. Cada uno aporta un depósito de saldo inicial
  y un pago de menos de 100.000 pesos, sin GMF. Esas filas cuentan en el total exacto de `--filas` y también
  dentro del mínimo de filas. Con `--filas` menor que ese mínimo la corrida falla con el mensaje habitual.
- La verdad queda en `verdad/correcciones_verdad.csv` (`tipo_ruido=colusion`) y en `verdad/comercios.csv`.
  `entrada/` no suma columnas ni trae verdad. El tamaño del grupo se lee contando las correcciones
  `colusion` de la llave.
- Se usa un `random.Random(f"{semilla}-colusion")` separado de los flujos principales. Con N distinto de 0
  cambian las filas de fondo (hay menos) y las llaves posteriores, pero con N igual a 0 no cambia nada.

### Caso 2: persistencia privada

Un comercio sin nombre crudo (vacío en ambas variantes). El usuario A lo corrige primero como
`PEPE SUPERMERCADO`. Meses después B y C coinciden en `Supermercado Don Pepe` (K=2 sobre el nombre).
Luego D paga en ese comercio. En `casos_vista.csv`:

- Un usuario que pagó antes de K=2 y no corrigió vio el nombre crudo: `crudo`, nombre vacío y
  `sin_categoria`.
- A ve siempre su nombre privado, incluso después de K=2: `privado`. La autoasignación gana.
- D ve el consolidado `SUPERMERCADO-DON-PEPE`: `global`.

### Caso 3: cambio de nombre del negocio

- `cambio_nombre`: un comercio consolidado como `MINIMERCADO-LA-ESQUINA` cambia el 2026-07-15 a
  `TIENDA-DOÑA-LUCY`, misma llave. Los usuarios que pagan después corrigen al nombre nuevo.
- `cambio_nombre_categoria`: igual, el 2026-08-10, de `CAFETERIA-EL-ROBLE` (restaurantes) a
  `LIBRERIA-EL-ROBLE` (educación). Cambia también la categoría.
- `ruido_sin_cambio`: un comercio consolidado como `FERRETERIA-LOS-ANDES` donde el desacuerdo
  posterior es solo ruido (un tipeo, una abreviatura, una categoría errónea aislada) y ningún par de
  usuarios coincide en un nombre o categoría distintos. Sirve para distinguir deriva real de ruido.

En los casos de cambio, antes de que se alcance K=2 sobre el nombre nuevo, quien paga ve el nombre
global anterior. Después, ve el nuevo.

## 12. Supuestos

Decisiones tomadas ante ambigüedades. Se eligió la opción más simple.

1. **Encabezados repetidos.** `tipo_llave` y `mcc` de `comercios.csv` se llaman `clase_llave` y
   `mcc_categoria`, porque la regla de no compartir encabezados con `entrada/` prima sobre la
   lista de columnas.
2. **`ocurrido_en` en `casos_vista.csv`.** Se conserva el nombre pedido y se trata como llave de
   unión adicional, solo en ese archivo. Una prueba lo verifica.
3. **Razón social de las empresas reales.** Es el nombre comercial público (no la razón legal).
4. **Claro.** Aparece solo como texto de `descripcion` (`Claro internet`), porque la especificación
   lo pide. El destinatario es la empresa ficticia `Redes Hogar Andinas S.A.S.`, sin atribuir MCC ni
   empresa a la marca real.
5. **Uber.** No se usa el nombre real. El par de nombres parecidos es `Ruta Veloz` y `Ruta Veloz
   Comidas`.
6. **Perfil 4 y `--fecha-fin`.** El perfil 4 necesita datos hasta el 20 de octubre, así que el corte
   mínimo es 2026-10-20 y el valor por defecto es 2026-10-21. Así ninguna fila de `entrada/` supera
   `--fecha-fin`. Un corte anterior se rechaza.
7. **Perfil 2.** El saldo inicial de 800.000, el salario al final del mes y la compra calculada del
   2 de octubre son mecanismos del generador para cumplir ingreso de 1.000.000, gasto de 800.000 y un
   saldo proyectado de 36.000 pesos. La compra no es parte de los 800.000 mensuales.
8. **GMF.** El monto exento es por envío (100.000 pesos) y el 4 por mil se aplica al monto completo.
   Es una simplificación del gravamen real.
9. **Tipos de asiento.** El reparto de la sección 6 es una decisión del equipo para que cada fila
   afecte el saldo y los seis tipos queden cubiertos.
10. **Fechas de evaluación.** Los avisos de los perfiles 1, 3 y 5 se evalúan en `--fecha-fin`
    (recortada al 31 de octubre en los perfiles 1 y 5). El de `pago_no_alcanza` del perfil 2 se evalúa
    el 2026-10-03 y los otros dos del perfil 2 el 2026-10-02. Todas las fechas de evaluación son
    iguales o anteriores a `--fecha-fin`.
11. **Población de fondo.** Los saldos de los usuarios de fondo no se garantizan no negativos. Solo
    los perfiles 2 y 3 lo garantizan. Las personas de fondo no reciben correcciones, salvo la
    arrendadora de los casos.
12. **Telefonía de enero a marzo.** El caso negativo de telefonía usa fechas de enero a marzo de
    2026, anteriores al periodo base de abril, como se pidió. Por eso `nombres_historial.csv` arranca
    el 2026-01-01.
13. **Proporción de nombres vacíos en B.** Primero se decide el canal QR y luego el vacío. Por eso en
    la variante B el vacío afecta solo al resto de pagos, alrededor de 3 % del total.
14. **Caso 2 y QR.** Los pagos del comercio sin nombre salen sin nombre ni MCC y por canal `llave`
    en ambas variantes. Los pagos de telefonía de las series van siempre por QR en B (si
    `fraccion_qr` es mayor que 0) para que se vea el MCC 4814.
15. **Partición de prueba.** El 30 % de comercios en prueba se define con la columna explícita `conjunto`
    (sección 2), estratificada por `nombre_revela_naturaleza` y elegida por semilla. Reemplaza el criterio
    anterior de posiciones 0, 1 y 2 de cada 10 por `llave_destino`. La partición cubre comercios y no
    personas, y es independiente de la variante.
16. **`es_correcta`.** Se evalúa solo sobre la categoría, no sobre el nombre.
17. **Nombre esperado.** En `casos_vista.csv` va canonizado. Para el caso `crudo` es vacío porque el
    comercio no trae nombre, y la categoría esperada es `sin_categoria`.
18. **`series.csv` no es exhaustivo.** Etiqueta las 8 series con nombre propio de los
    escenarios fijos (internet del perfil 2, Claro, arriendo, telefonía, luz, agua y supermercado) y las series
    de la población de fondo (sección 10). Los demás pagos de fondo no se etiquetan.
19. **Pesos y tolerancias de recurrencia.** Los pesos (identidad dura, monto medio, fecha blanda) y
    cualquier tolerancia de monto o de días son propuestas del detector sin fuente verificada. Se
    calibran con este dataset y la verdad no depende de ellas, porque `es_recurrente` sale de la
    intención de diseño.
20. **Prosa y estilo.** Español neutro con tuteo. No se usan rayas largas ni medias ni punto y coma en
    prosa.
21. **Colusión sintética.** La población de la sección 11b es una construcción de diseño. No hay fuente de tasa
    real de colusión: el número de comercios, el tamaño del grupo, los plazos entre colusores y honestos y el
    hecho de que siempre haya 3 honestos son decisiones del equipo para poder medir el envenenamiento según
    K. No estiman la prevalencia de fraude en producción.
22. **Manifiesto con N igual a 0.** `manifiesto.json` registra siempre `colusion_comercios` y
    `colusion_tamanos`, también con N igual a 0. Es el único archivo que cambia respecto de la versión previa
    en ese caso, y su sección `archivos` (conteos y sha256 de los CSV) queda igual.

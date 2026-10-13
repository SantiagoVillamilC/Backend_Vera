# Plan del módulo de análisis

Fecha: 6 de octubre de 2026 (actualizado al cierre de los tres experimentos). Estado: plan aprobado por Christian en los tres niveles. El generador (`generador/`) ya existe. Este documento planifica el módulo `analisis/`, que lee las carpetas que produce `generar.py` y calcula los números de la sección 5.1 de la tesis.

Estado de los experimentos, con la suite en 577 pruebas:

- Experimento B (recurrencia): cerrado.
- Experimento A (convergencia por multitud): cerrado en código y documentado en la bitácora de convergencia (`bitacora-tecnica/2026-10-06-convergencia-multitud-k.md`), con las extensiones A2 (agrupar por similitud), A3 (representante frente a `mas_votado`) y la calibración del umbral de similitud en su sección Actualizaciones.
- Experimento C (avisos): cerrado como prueba de aceptación de 13 filas, documentada en `bitacora-tecnica/2026-10-06-avisos-presupuesto-aceptacion.md`. No es una medición.

| Experimento | Medido (sobre corpus sintético) | Probado en código (por construcción o por prueba) |
|---|---|---|
| B, recurrencia | Precisión, recall y F1 frente a la línea base citable, óptimo restringido y bootstrap pareado, con semilla de entrenamiento y semillas de validación separadas | Coherencia de la regla con el modelo de recurrencia del generador, separación de cargadores de verdad y de detector |
| A, convergencia | Cobertura, riesgo y errores visibles de nombre y categoría según K y tasa de corrección | Invariancia al barajar correcciones simultáneas, la corrección privada que gana en la vista propia, la razón fija de honestos frente a colusores, el acierto total (referencia secundaria, fuera del titular), el envenenamiento por colusión (ocurre cuando K es menor o igual a T), la promoción indebida de personas (filtro perfecto por construcción) y los días hasta consolidar (los 56 días de T=2 salen del diseño del generador) |
| A2, agrupar por similitud | Cobertura y errores visibles de nombre con y sin agrupar, por umbral y K | Control idéntico a A, fusiones falsas medidas por llave, determinismo |
| A3, representante | Errores visibles de nombre con `primero` frente a `mas_votado` | El ancla del grupo no cambia con la política, así que promoción y relevo no dependen de ella |
| A4, deriva | Envenenamiento temprano y tardío, errores visibles y retraso por cambio real, con `regla_deriva` estricta y reciente | La deriva solo releva en el empate y por recencia, un ataque tardío de T=2 no empata a los 3 honestos, y el escenario tardío no altera la población temprana |
| Similitud, umbral | Precisión y recall por umbral y semilla, piso de falsos positivos, negativo difícil | Regla de etiquetado por llave, exclusión de ambiguos, monotonía del recall, respeto de la precisión mínima, determinismo |
| C, avisos | Resultado de R2 en las 13 filas, falso positivo de R1 en la semilla 11, volumen de avisos de los 106 usuarios de fondo (sin verdad, no es tasa) | Aprobación de R1 en 13 de 13 (la regla se leyó de las mismas etiquetas), que la mediana gane a la media (diseñado así), que la lógica de avisos no importe la verdad |

## Objetivo

Producir resultados con la etiqueta "Medido" para los dos retos de la tesis:

1. Convergencia por multitud de nombre, categoría y empresa de comercios desconocidos (K=2).
2. Hábitos de consumo: recurrencia de pagos y avisos de presupuesto.

## Reglas que no se negocian

- La verdad de `verdad/` nunca la ve un detector. Dos cargadores separados y una prueba estática que lo verifica.
- Ninguna etiqueta de verdad sale de un umbral que luego se calibre. `es_recurrente` sale de la intención de diseño del generador.
- El reloj se inyecta: todo detector recibe `ahora` como parámetro y solo mira filas anteriores.
- Todos los índices (pesos, tolerancias, cadencias, ocurrencias mínimas, umbrales, K) viven en un archivo de configuración y se pueden barrer.
- La identidad de un comercio es la llave de comercio (o el Merchant ID del QR), luego la cuenta interna, luego el nombre canónico como llave débil. El MCC es una categoría y nunca es identidad.
- Calibrar sobre datos propios ajusta los índices al modelo de recurrencia del generador, no a la realidad. Se reporta como "óptimo sobre el corpus sintético", con una semilla de entrenamiento y al menos tres semillas de validación.

## Tablas objetivo de 5.1 (todo lo que no llene una celda sale del plan)

| Reto | Se varía | Columnas |
|---|---|---|
| 1. Multitud | K en {1, 2, 3} y tasa de corrección en {0,1, 0,35, 0,6} | Acierto de nombre y de categoría, días hasta consolidar, envenenamiento exitoso, personas promovidas |
| 2a. Recurrencia | Entrenamiento frente a validación (3 semillas), con y sin feedback simulado | Precisión, recall y F1, ocurrencias necesarias para detectar |
| 2b. Avisos | Mediana frente a media, series verdaderas (oráculo) frente a series detectadas | Avisos acertados y falsos por usuario y mes |

## Experimento B: recurrencia (primero)

Estado: cerrado el 6 de octubre de 2026 con línea base citable (regla de práctica de lyndonkl), óptimo restringido al rango de práctica (tolerancias de 2, 3 y 4 días, `n_min` 3) y bootstrap pareado de la diferencia de F1. El óptimo libre se reporta al lado y no es el titular. Detalle en `analisis/SUPUESTOS.md` y en `resultados/recurrencia/comparacion-*/`.

Detector por usuario e identidad. Índice `R = N x (w_monto x Monto + w_fecha x Fecha)`:

- `N = min(1, ocurrencias / n_min)`, sin intervalo calculable con menos de 2 ocurrencias.
- `Monto`: fracción de pagos dentro de una tolerancia relativa de la mediana de montos.
- `Fecha`: fracción de intervalos dentro de la tolerancia de la cadencia más cercana a la mediana de intervalos (cadencias candidatas 7, 14 y 30 días).
- Recurrente si `R` supera el umbral.

Valores iniciales de ejemplo (sin fuente verificada, se calibran): `w_monto` 0,6, `w_fecha` 0,4, tolerancia de monto 10 %, tolerancia de días 4 para 30 días, `n_min` 3, umbral 0,5.

Pruebas con ejemplos calculados a mano: Claro (89.900 los días 5 de abril a julio, el 9 de agosto y 94.900 el 5 de septiembre) da `R` de 0,92 y el caso Movistar irregular (20.000, 25.000 y 50.000 los días 10 de enero, 20 de febrero y 20 de marzo) da 0,40.

Calibración: barrido de cuadrícula sobre la semilla de entrenamiento y validación en semillas separadas. Métricas: precisión, recall, F1 y ocurrencias necesarias para detectar.

## Experimento A: convergencia por multitud

- Reproduce `correcciones.csv` en orden cronológico (desempate por `correccion_id`). Cuenta usuarios distintos por nombre canónico y por categoría. Promueve a global al llegar a K usuarios distintos.
- La corrección privada gana siempre en la vista del propio usuario.
- Agrupación por similitud de nombres (typos y abreviaturas) con método y umbral parametrizables.
- Regla de deriva (propuesta sin fuente, medida en A4): con `regla_deriva` igual a `reciente`, un empate en el apoyo lo decide la recencia, releva el candidato cuyo voto que completa K es posterior a la promoción del incumbente. Se mide con el ataque posterior a la consolidación de `--colusion-tardia-comercios M`.
- Métricas: acierto frente a `nombres_historial.csv` vigente en cada fecha, días hasta consolidar, cambios espurios, retraso tras un cambio real, envenenamiento por colusión según K, promoción indebida de personas, aprobación de `casos_vista.csv`.
- Prueba de invariancia: barajar correcciones simultáneas no cambia el resultado.
- Primera pasada sin clasificador: se mide el porcentaje de comercios de prueba que quedan en `Sin categoría` tras las reglas, y de esos cuántos tienen `nombre_revela_naturaleza` verdadero. Ese número decide si TF-IDF, MiniLM o un LLM por API se justifican.

## Experimento C: avisos de presupuesto

Depende de B (el aviso "no te alcanzará" necesita saber qué es recurrente, cuándo cae y por cuánto). Se evalúa dos veces, con las series verdaderas (oráculo) y con las detectadas por B, para no confundir el error de C con el de B. Casos: categoría sobre su media o su mediana, pago que no alcanza, presupuesto anticipado, cobro nuevo y ausencia de avisos falsos en el perfil irregular. Se compara contra `verdad/avisos_esperados.csv`.

## Entrega a la tesis

Cada experimento produce una entrada de aportes técnicos con sus cuatro partes. Las tres están escritas en `aportes-tecnicos/borrador-inteligencia/` (CR-031 recurrencia, CR-032 convergencia y CR-033 avisos) y falta integrarlas al documento de equipo. La de C se rotula como prueba de aceptación y no como medición, con sus hallazgos "Medido" fila por fila. Limitaciones a 1.4: datos sintéticos, usuario simulado, calibración circular. A 7 Recomendaciones: la regla de deriva, el feedback real y la integración a producción.

Limitaciones nuevas que deben declararse:

- Razón fija de 3 honestos frente a T colusores en el generador, de modo que el envenenamiento se mide a una sola proporción.
- Ruido de tipeo independiente entre usuarios y abreviatura determinista. La medición es pesimista para abreviaturas (todos abrevian igual y K=2 las promueve) y optimista para tipeos (nadie repite el mismo error), y los usuarios reales podrían repetir un tipeo.
- La clave de nombre canonizada, compartida con el generador, deshace de forma trivial las mayúsculas y las tildes.
- La cobertura depende de la densidad de pagadores que fija el generador.
- No hay corrección por comparaciones múltiples con n=151 en `prueba_sin_colusion`.
- El filtro por tipo de llave que evita promover personas es perfecto por construcción.
- El atacante no es adaptativo.
- La guarda de 0,25 de `caida_ingreso` queda al borde de la variabilidad del perfil irregular.
- Todas las fuentes de C son productos de EE. UU. con umbrales en dólares, que no se trasladan a pesos ni a Bre-B sin otra medición.
- Los envíos a personas quedan fuera del gasto, así que el arriendo pagado a una llave de persona no cuenta como gasto.
- Las etiquetas de C están escritas a mano, por lo que R1 aprueba por consistencia y no como evidencia independiente.

## Pendiente (en este orden)

A, B y C ya están cerrados y sus tres entradas de aportes técnicos están escritas. La similitud de nombres se midió (A2, A3 y calibración), la rejilla restringida de B se extendió al umbral 0,8 y 0,9 (el óptimo restringido es una meseta de dos configuraciones) y la regla de deriva se implementó y se midió con el escenario de ataque posterior (A4). Lo que queda es lo siguiente:

1. Integrar las CR-031, CR-032 y CR-033 al documento de equipo (aportes-christian-borrador).
2. [hecho en A4] Regla de deriva con un escenario de ataque posterior a la consolidación. El generador gana `--colusion-tardia-comercios M` y la configuración gana `regla_deriva`. Medida a tasa 0,35: sigue los cambios reales y corrige el envenenamiento temprano, pero deja al ataque tardío de T=3 sobrescribir un valor ya consolidado. Detalle en la bitácora de convergencia.
3. [hecho el 12 de octubre de 2026] Población de usuarios de gasto con verdad por diseño para C (`--gasto-usuarios`, diez diseños, verdad por intención), prorrateo del acumulado como variante medida (`prorrateo_acumulado`) y tope de avisos por usuario y mes con prioridad (`tope_avisos_mes`). Pruebas en `tests/test_gasto_poblacion.py`, `tests/test_analisis_avisos.py` y `tests/test_analisis_avisos_evaluacion.py`.
4. Exploratorio: usuario simulado de feedback (recordatorio con "Sí, es mensual", "No, fue único", "No me avises más", barrido de la tasa de respuesta en 0,1, 0,25 y 0,5).
5. [TF-IDF hecho el 12 de octubre de 2026] Comparación de clasificadores. El TF-IDF local está en `analisis/clasificador_tfidf.py` y midió 0,718 de exactitud sobre la prueba, 0,95 cuando el nombre revela la naturaleza, 0,735 en ficticias y 0,60 en reales (detalle en la bitácora de convergencia). Faltan MiniLM y el LLM por API, que puede reconocer marcas reales, por eso hay que usar la columna `ficticia`. También es exploratoria la agrupación de sucursales en empresas (con lista de palabras genéricas).

El prior por categoría tras un solo pago solo funciona en la variante B (con MCC).

## Fuentes y huecos

- Con fuente abierta: Plaid agrupa por descripción, monto y cadencia (blog de Plaid).
- No verificado: que Plaid exija 3 ocurrencias para una serie madura. Viene de un resumen de buscador que no se abrió.
- Sin fuente verificada: los pesos, las tolerancias, las bandas de días, el umbral, la regla de deriva y el prior por categoría.

# Supuestos del módulo de análisis (detector de recurrencia)

Decisiones que la solicitud dejaba abiertas o que había que precisar para poder implementarla. Cada
una se puede cambiar sin tocar el generador. Las pruebas de `tests/test_analisis_*.py` fijan el
comportamiento actual.

## Qué es un pago (`analisis/pagos.py`)

1. Un pago es una fila con `direccion` igual a `salida` y `tipo_asiento` en `liquidacion_confirmada`
   (comercio), `transferencia_interna` o `despacho_riel` (personas). Se descartan `deposito`,
   `retencion` (incluye el GMF, que no trae llave, y el envío retenido que luego falla) y
   `compensacion` (la devolución, que además es una entrada).
2. Un envío devuelto no es un pago que se completó. Si el `transferencia_id` de una fila tiene una
   `compensacion`, toda la fila de ese grupo queda fuera. En la corrida 2026 de 3.000 filas hay 2 grupos así.
   Quien consume los datos debe netear por `transferencia_id` (sección 6 de la especificación) y este
   criterio es el mismo.
3. La solicitud pide "con `llave_destino` no vacía" y también una identidad degradada sin llave. Se
   concilian así: una fila sin llave solo entra si su nombre crudo sirve de identidad. En los datos
   del generador todo pago saliente trae llave, de modo que esa rama no se ejercita fuera de las pruebas.

## Identidad

4. La identidad es `("llave", llave_destino)`. Sin llave es `("nombre", nombre_canonico)`. La
   etiqueta evita que un nombre choque con una llave que tenga el mismo texto.
5. El nombre canónico se calcula con las reglas de la sección 9 de la especificación, reimplementadas en
   `analisis/pagos.py` para que el análisis no dependa del generador. Una prueba comprueba que coinciden
   con `canonizar`.
6. Un nombre vacío o el genérico `Pago a comercio` no es identidad: esas filas se descartan en lugar
   de mezclar comercios distintos bajo un mismo nombre.
7. El MCC y el canal no intervienen nunca. Con la misma corrida en variante A y B el resultado es
   idéntico (verificado con la semilla 2026: 84 TP, 0 FP, 80 TN y 0 FN en las dos variantes con la mejor configuración).

## Cálculo del índice (`analisis/recurrencia.py`)

8. Los intervalos se miden entre fechas calendario de Colombia (UTC-5), no entre instantes UTC. Un pago
   a las 02:30 UTC del día 1 pertenece al día 30 en Colombia.
9. Las tolerancias son inclusivas (`<=`) en monto y en días.
10. Todo el cálculo usa `fractions.Fraction`. En coma flotante el caso Movistar da 0,39999999999999997 y
    quedaría por debajo de un umbral de 0,4 que en realidad alcanza. Los decimales de la configuración
    se convierten con su representación escrita (0,1 es 1/10). `R` se guarda redondeado a 9 decimales.
11. Si la mediana de intervalos queda exactamente a la misma distancia de dos cadencias candidatas
    (por ejemplo 10,5 entre 7 y 14), gana la cadencia menor.
12. `cadencias_dias` y `tolerancias_dias` se emparejan por posición. Un intervalo de 0 días (dos pagos
    el mismo día local) cuenta como intervalo y casi nunca cae dentro de una tolerancia.
13. Con menos de 2 ocurrencias `R` vale 0 y la pareja no es recurrente, incluso con umbral 0.
14. `ahora` debe llevar zona horaria. Se rechaza un `datetime` sin zona. Cuenta todo pago con
    `ocurrido_en <= ahora`.
15. `n_min` debe ser un entero mayor o igual que 1. `tol_monto` está en (0, 1]. Los pesos suman 1 con tolerancia 1e-9.

## Evaluación (`analisis/evaluacion.py`)

16. `ahora` es el fin del día de `fecha_fin` del manifiesto (`23:59:59Z`). Con medianoche se perdería el último día de datos.
17. F1 se calcula como 2TP / (2TP + FP + FN) y vale 0 si el denominador es 0. Precisión y recall
    indefinidos se reportan como `n/a`.
18. Por motivo, `compra_habitual` y `pago_suelto` no tienen positivos reales: su precisión, recall y F1
    son `n/a` y solo se informa la tasa de falsos positivos. En los motivos positivos la precisión se
    mide dentro del subconjunto del motivo, así que no es comparable con la global.
19. Métrica secundaria: parejas con 2 o más pagos que no están en `series.csv`, tratadas como no
    recurrentes. Es una SUPOSICIÓN (supuesto 18 de la especificación: `series.csv` no es exhaustivo) y la tasa es
    una cota superior. En la corrida 2026 los dos falsos positivos secundarios de la mejor configuración
    son pagos de 100.000 a un restaurante (categoría real `restaurantes_domicilios` en todos). Uno es el
    usuario del perfil 1 (22 pagos desde abril, incluida la boda de julio) y el otro el del perfil 5
    (11 pagos desde el 15 de agosto). `series.csv` no etiqueta esas parejas y la verdad no dice si son
    recurrentes, así que no se pueden contar como error ni como acierto.
20. Ocurrencias necesarias para detectar: para cada serie recurrente se evalúa con `ahora` igual al
    instante de cada pago sucesivo. La detección es sostenida si `R` queda sobre el umbral desde ese
    pago hasta el último. Se reporta el menor k con esa propiedad. Una serie cuyo último pago no queda
    detectado cuenta como no detectada. La mediana es sobre las detectadas. Solo se calcula para la mejor
    configuración y la de referencia, no para cada fila del barrido.
21. Para acelerar el barrido, `preparar_corrida` descarta las parejas de un solo pago que no están en
    `series.csv`. No pueden ser recurrentes ni contar en la métrica secundaria.

## Barrido (`analisis/barrido.py`)

22. La rejilla real tiene 2.160 combinaciones: `w_monto` 0,3 a 0,8 en pasos de 0,1 (6), `tol_monto`
    0,05 a 0,30 en pasos de 0,05 (6), `factor_tolerancia_dias` en {0,5, 1, 1,5, 2} (4), `n_min` en {2, 3, 4}
    y `umbral` 0,3 a 0,7 en pasos de 0,1 (5). Los valores del factor no estaban en la solicitud: se
    eligieron para cubrir tolerancias de 1 a 8 días. Los pasos de los demás ejes también son elección propia.
23. `w_fecha` no es un eje: se deriva como 1 menos `w_monto` (redondeado a 6 decimales) porque los pesos
    deben sumar 1.
24. El factor multiplica las tolerancias de días de la configuración base. Las cadencias (7, 14, 30)
    quedan fijas. `mejor_configuracion.json` guarda las tolerancias ya escaladas, así que se puede
    pasar tal cual a `--config`.
25. Desempate determinista: mayor F1, mayor precisión, menor tasa de falsos positivos secundaria (todas con fracciones
    exactas) y, por último, el menor índice de la rejilla. El orden de la rejilla es el producto
    cartesiano de los ejes con `umbral` variando más rápido. En la corrida 2026 solo 3 de las 2.160
    configuraciones alcanzan F1 igual a 1, de modo que el desempate por índice no decide casi nada: lo
    decide la tasa secundaria.
26. La semilla de entrenamiento no puede aparecer entre las de validación, y las de validación no se
    pueden repetir. El barrido nunca mira la validación para elegir.
27. Además de la mejor configuración se evalúa la configuración por defecto como referencia, para que
    el efecto de la calibración se vea lado a lado.
28. El resultado se presenta como "óptimo sobre el corpus sintético", nunca como óptimo real: la verdad
    sale del modelo de recurrencia del generador y calibrar sobre ella es circular.

## Salida y determinismo (`analizar.py`)

29. `<id>` es `evaluacion-` o `barrido-` más los 12 primeros caracteres de un sha256 de la configuración
    (o de la configuración base y la rejilla) y de los sha256 de los archivos de entrada.
30. Los archivos no contienen horas de ejecución, tiempos ni rutas absolutas. El tiempo va por stderr.
    El nombre de cada corrida en los archivos es el nombre de su carpeta, que por eso debe ser distinto
    entre corridas del mismo experimento.
31. CSV con fin de línea `\n`, reales con 6 decimales y JSON con claves ordenadas. La prueba de
    determinismo ejecuta el CLI en subprocesos con `PYTHONHASHSEED` distintos.
32. `resultados/.gitignore` contiene `*` y `!.gitignore`. El CLI lo crea si falta.

## Línea base citable, óptimo restringido y bootstrap (`linea_base.py`, `comparacion.py`, `bootstrap.py`)

33. Línea base: la regla de práctica de lyndonkl (https://claudeskills.info/skills/lyndonkl/claude/recurring-charge-detector/)
    con la selección de cadencia que fijó la coordinación. No es la regla verbatim. La fuente dice "If multiple
    cadences are plausible, pick the one with tighter fit (smallest standard deviation of deltas)". Esa desviación
    de los intervalos no depende de la cadencia, así que no puede elegirla. Se usa la cadencia más cercana a la
    mediana de los intervalos, con empate a la menor. La fuente cita "occurrences ≥ 3", "≥ 80% of deltas fall within
    the cadence's tolerance band" y un coeficiente de variación σ / |μ| con tope 0,40, sin decir si σ es poblacional.
    Se usa la poblacional.
34. Se omiten las cadencias trimestral, semestral y anual de la fuente. El generador no crea series con esas cadencias.
35. Cuentan los mismos pagos, la misma identidad, las mismas fechas locales y el mismo reloj que en el índice.
    Con menos de 3 ocurrencias no se evalúa. El 80 % de intervalos (`en_banda / intervalos >= 4/5`) y el CV
    (`n x suma de cuadrados <= 1,16 x suma al cuadrado`) se comprueban de forma exacta y con límites inclusivos.
36. Sin verificar: no se contó cuántas decisiones cambiarían con la lectura alternativa "recurrente si alguna
    cadencia tiene el 80 % de los intervalos en banda".
37. Óptimo restringido: rejilla de 180 combinaciones (`w_monto` x `tol_monto` x `umbral` con los valores de la
    rejilla libre), factor 1,0 y `n_min` 3. El código rechaza una rejilla restringida con otro factor u otro
    `n_min`, y una base con tolerancias distintas de 2, 3 y 4. No se amplió nada hacia abajo.
38. Bootstrap: B de 2000 (parámetro `--bootstrap-b`), `random.Random("<semilla>:<corpus>")` con semilla entera fija
    2026 (`--semilla-bootstrap`), remuestreo con reemplazo de las series etiquetadas con los mismos índices para los
    tres métodos. Intervalo percentil con los índices ceil(0,025 B) - 1 y ceil(0,975 B) - 1 (49 y 1949). "Incluye 0"
    significa inferior <= 0 <= superior. El archivo trae la diferencia observada y la media bootstrap por separado.
39. El corpus agregado concatena las series de las validaciones 1, 7 y 11. Las verificaciones (5 y 9) tienen su
    propio intervalo con rol `verificacion` y no entran al barrido ni al agregado. El entrenamiento no lleva intervalo.
40. Para libre frente a restringido la frase fija es "(no se distingue del restringido)" y no "de la línea base",
    que sería falsa.
41. `recurrencia-barrido` no cambió: sus filas `referencia` siguen usando la configuración por defecto del índice.
    La comparación nueva usa solo línea base, restringido y libre.
42. Motivos sin positivos: `metricas.csv` ya informaba FP, TN y tasa. `comparacion.csv` los repite con una columna
    `nota` y deja precisión, recall y F1 en `n/a` porque no están definidos sin positivos reales.

## Convergencia por multitud, experimento A (`convergencia*.py`)

Cada punto dice si tiene fuente o es propuesta de diseño sin fuente. Salvo el 44, todas las reglas de promoción
y de medición son propuesta nuestra: Waze, OSM y Google Maps no publican un K ni una regla de relevo (ver bitácora).

43. Módulos. `convergencia.py` es el replicador y solo ve `entrada/` (recibe filas ya cargadas, nunca importa
    `analisis.cargar`, lo comprueba la prueba estática). `convergencia_evaluacion.py` y `convergencia_medidas.py`
    cargan la verdad y calculan las medidas. `convergencia_agregado.py` agrega por tasa y hace el bootstrap.
    `convergencia_experimento.py` y `convergencia_resumen.py` escriben `resultados/convergencia/convergencia-<id>/`
    (un CSV por tabla, `manifiesto.json`, `configuracion.json` y `resumen.md`, deterministas byte a byte).
    Todos los índices están en `analisis/config/convergencia.json`: lista de K, políticas de elegibilidad,
    política principal, K de referencia y `umbral_similitud`, que está reservado y la validación obliga a que sea null.
    `regla_deriva` es opcional (`estricta` por defecto, `reciente` para la deriva, ver el punto 64).
44. Clave de nombre: `normalizar_nombre` de `analisis/pagos.py`, que aplica las reglas de la sección 9 de la
    especificación (las mismas de `canonizar`, supuesto 5). Se usa en lugar de importar `canonizar` desde `generador`
    para que el análisis no dependa del generador, y una prueba ya fija que coinciden. Esto deshace de forma trivial el
    ruido de mayúsculas, tildes y formato, así que la agrupación por similitud NO se mide en esta pasada. Los tipeos y
    las abreviaturas siguen siendo claves distintas. Fuente: especificación, sección 9.
45. Un voto vigente por usuario, llave y dimensión (nombre y categoría por separado). Si el usuario corrige de nuevo, el
    voto anterior se reemplaza y no cuenta dos veces. Propuesta sin fuente.
46. Promoción y relevo. Un valor pasa a global cuando reúne K usuarios distintos. Con global ya puesto, otro valor lo
    releva solo si reúne al menos K y supera ESTRICTAMENTE a los usuarios del global actual: el empate conserva al
    incumbente. El global no se retira por sí solo (si un apoyo cambia de voto, el global se queda) y, si el apoyo del
    global llegara a 0, cualquier candidato con al menos K lo releva. La configuración `regla_deriva` admite `estricta`
    (esta, el empate conserva al incumbente) y `reciente` (deriva: en el empate releva el candidato cuyo voto que completa
    K es posterior a la promoción del incumbente, ver el punto 64). Las correcciones se reproducen por
    (ocurrido_en, correccion_id), de modo que barajar filas simultáneas no cambia nada. Propuesta sin fuente.
47. Vista de un usuario en un instante: su voto privado vigente si existe, si no el global, si no el crudo (nombre
    canonizado del movimiento y `sin_categoria`). Las dos dimensiones son independientes: con global solo en una, la
    otra se completa con el crudo y el origen es `global`. El privado vale desde el instante de su corrección
    (inclusive). Propuesta sin fuente, calibrada contra `casos_vista.csv` (sección 11 de la especificación).
48. Elegibilidad. El tipo de llave sale de `movimientos.csv` (columna `tipo_llave`, observable). Política
    `sin_filtro` (toda llave es elegible) y `solo_comercio` (solo `comercio`). Verificado en la corrida 2026 con
    tasa 0,35: toda llave de tipo `comercio` tiene `es_comercio` verdadero y las 12 llaves alfanuméricas son
    personas, así que el generador no usa alfanuméricas para comercios y no hace falta incluirlas en la política.
    Una llave sin tipo observado no es elegible bajo `solo_comercio`. Los votos de una llave son independientes de
    los de las demás, así que la política solo cambia las promociones de las personas y las medidas de comercios
    salen idénticas bajo las dos (una prueba lo fija). Una persona promovida es una falla de privacidad.
49. Reloj de corte: el fin del día de `fecha_fin` (supuesto 16). La verdad vigente en un instante es la fila de
    `nombres_historial.csv` cuya ventana de fechas locales de Colombia, con extremos inclusivos, contiene ese instante.
50. Cobertura, acierto y días. Cobertura es la fracción de comercios con valor global en el corte. El acierto entre
    consolidados compara el global con el vigente verdadero en el corte. El acierto total cuenta los no consolidados
    como fallo, y se reportan ambos para no esconder la cobertura. Los días son corridos (pueden ser fraccionarios) y
    van desde la primera corrección del comercio hasta la primera promoción correcta (correcta respecto de la verdad
    en el instante de la promoción), solo para los comercios que consolidaron. Mediana normal y p90 por rango más
    cercano (posición ceil(0,9 n) de los ordenados). Al agregar semillas se promedian los valores por corrida, de modo
    que la cifra agregada es una media de medianas y no la mediana de todos los comercios.
51. Cambio espurio: promoción o relevo a un valor distinto del vigente verdadero en ese instante. Se cuenta como
    revertido si después hay otro cambio global de la misma llave y dimensión. Incluye las promociones de colusión.
    Se reporta aparte el total revertido fuera de los comercios de colusión.
52. Retraso tras un cambio real: solo comercios con exactamente 2 filas en `nombres_historial.csv` y solo en la
    dimensión cuyo valor cambia. Se cuentan los días desde la medianoche local (05:00 UTC) de `vigente_desde` hasta el
    primer cambio global al valor nuevo en o después de ese instante. Si no ocurre antes del corte, cuenta como no alcanzado.
53. Envenenamiento. T es el número de correcciones con `tipo_ruido` igual a `colusion` de la llave, así que el caso
    escrito a mano de la droguería entra como un comercio más de T=2. Envenenado significa categoría global distinta de
    la verdadera en algún momento. Los días equivocada suman los intervalos con la categoría global equivocada (hasta el
    siguiente cambio o hasta el corte), mirando la verdad al inicio de cada intervalo. Sin relevo es estar equivocada al corte.
54. Casos de vista: se resuelve el movimiento por `movimiento_id` en `movimientos.csv` para tomar la llave y el nombre
    crudo. Se compara de forma exacta nombre, categoría y origen, y un caso aprueba si coinciden las tres columnas.
55. Residuo `sin_categoria`: entre los comercios de `prueba`, los que no tienen categoría global en el corte, con el
    desglose entre los que no recibieron ninguna corrección y los que recibieron correcciones sin consenso, y cuántos
    de ellos tienen `nombre_revela_naturaleza` verdadero. Se reporta para todos los K, con K=2 como referencia.
56. Bootstrap: B de 2000, semilla 2026, unidad el comercio y comercios de las cuatro semillas juntos por tasa y
    ámbito (`todos` y `prueba`), con los mismos índices para todos los K. Se reutilizan de `bootstrap.py`
    `generar_remuestreos`, `indices_percentil`, `Intervalo` y `frase`. No se reutiliza `intervalo_pareado` porque está
    atado al F1 y aquí la medida es el acierto total. Diferencia K de referencia (2) menos K=1 y menos K=3, por
    dimensión. Los comercios de una misma semilla no son independientes entre tasas, por eso no se juntan las tasas.
57. Errores visibles y par riesgo-cobertura (por corrida, K, política y ámbito, en nombre y en categoría). Errores
    visibles es el número de comercios cuyo valor global al corte es DISTINTO del vigente verdadero, dividido entre el
    total de comercios del ámbito, de modo que la abstención no cuenta como error visible. Riesgo es 1 menos el acierto
    entre consolidados. Valen `errores_visibles = riesgo x cobertura` y `riesgo = errores sobre consolidados` (una
    prueba lo fija con tolerancia de redondeo). El marco es el de la clasificación selectiva (El-Yaniv y Wiener, 2010,
    JMLR 11): cobertura es la masa aceptada y riesgo es el error sobre lo aceptado. El marco tiene fuente (no se releyó
    el artículo en esta pasada) y su aplicación a K y a la consolidación por multitud es propuesta sin fuente.
58. Ámbitos. El titular del reporte es `sin_colusion`: excluye los comercios con al menos una corrección de tipo
    colusión (la población de colusión y el caso escrito a mano de la droguería, supuesto 53). `prueba_sin_colusion` es la
    intersección del conjunto `prueba` con `sin_colusion`. `todos` y `prueba` se conservan pero el resumen los marca como
    contaminados por la población de colusión inyectada, que consolida siempre el nombre. Propuesta sin fuente.
59. Bootstrap de errores visibles y cobertura (`bootstrap_riesgo.csv`). Ámbitos `sin_colusion` y `prueba_sin_colusion`
    (se reporta el n de cada uno, el segundo es pequeño y sus intervalos son anchos). Por tasa, comercios de las cuatro
    semillas juntos, B de 2000 y semilla 2026, con la misma clave de remuestreo `<tasa>:<ámbito>:<dimensión>` del bootstrap
    del supuesto 56, así que en `sin_colusion` los índices coinciden con los de aquel. Diferencias K=2 menos K=1 y K=2
    menos K=3 de errores visibles y de cobertura, en nombre y en categoría, con el intervalo percentil de 95 % de
    `_intervalo`. El indicador por comercio vale 1 si hay valor global (cobertura) o si lo hay y es distinto del vigente
    verdadero (errores visibles). No se corrige por comparaciones múltiples. Propuesta sin fuente.
60. Residuo `sin_categoria` titular: se recalcula sobre `prueba_sin_colusion` con el mismo desglose del supuesto 55 (sin
    correcciones, con correcciones sin consenso y con `nombre_revela_naturaleza` verdadero). El residuo sobre `prueba` se
    sigue calculando en `medidas.csv` pero queda contaminado por la población de colusión.
61. Densidad, por corrida y para los comercios de `sin_colusion`. Pagadores distintos por comercio son los `usuario_id`
    distintos de `movimientos.csv` con esa `llave_destino` y correctores distintos son los de `correcciones.csv`. Se
    reportan mínimo, p25, mediana, p75 y máximo (p25 y p75 por rango más cercano, posición ceil(p n), y mediana normal),
    la fracción con un solo pagador y la fracción con al menos K correctores distintos para K=1, 2 y 3. Esa fracción es la
    cota superior de la cobertura, porque un valor solo se consolida con K usuarios distintos. No dependen de K ni de la
    política. Un comercio sin pagos en la corrida cuenta con 0 pagadores. Propuesta sin fuente.
62. El acierto total (la abstención cuenta como fallo) deja de ser titular porque castiga la cobertura baja y mezcla
    cobertura con riesgo. Se conserva solo en una sección secundaria del resumen con esa nota. Sus valores no cambian.
63. Etiquetas de verificación del resumen. Medido: cobertura, riesgo, errores visibles, días hasta consolidar y residuo en
    `sin_colusion`. Probado en código (`tests/test_analisis_convergencia_riesgo.py`): el envenenamiento (un comercio queda
    envenenado exactamente cuando K es menor o igual a T, y el relevo depende de la razón de 3 honestos contra T colusores
    y de los empates que conservan al incumbente), el filtro por tipo de llave (las llaves de comercio del generador son
    de clase `comercio` y las de personas son celular, correo, alfanumérico o documento, así que `solo_comercio` no
    promueve personas por construcción del generador) y `casos_vista` (13 casos escritos a mano, no una muestra).
    Propuesta sin fuente.
64. Hallazgo de `cambio_nombre` y regla de deriva. La regla de relevo estricto no puede seguir un cambio real porque el
    nombre viejo y el nuevo empatan 2 contra 2 y el empate conserva al incumbente. Por eso 11 de 13 casos de vista aprueban
    con K=1 y K=2 y el retraso queda vacío. Cualquier regla que deje a K votos recientes reemplazar un nombre consolidado
    también deja a K colusores sobrescribir uno correcto. Se cerró en la extensión A4 (2026-10-12): el generador gana
    `--colusion-tardia-comercios M` (3 honestos consolidan primero y T colusores corrigen después, M comercios que atacan
    el nombre y M la categoría, por tamaño) y la configuración gana `regla_deriva` igual a `estricta` o `reciente`. Medida
    a tasa 0,35 sobre 4 semillas con M=20, la deriva es un intercambio puro: sigue los cambios reales (el retraso pasa de 0
    a 2 de 2 en nombre y de 0 a 1 de 1 en categoría) y corrige el envenenamiento temprano (de 20 a 0 comercios en T=3 y de
    1 a 0 en T=2), pero en el escenario tardío deja al ataque sobrescribir un valor consolidado (de 0 a 20 comercios
    envenenados en T=3, y los errores visibles de nombre en `todos` de 0,012 a 0,098). Un ataque de T=2 no llega a empatar
    a los 3 honestos, así que solo daña el caso T=3. Detalle y limitaciones en la bitácora de convergencia.

Observaciones de la primera ejecución (12 corridas de 3.000 filas, no son supuestos):

- Con la regla de relevo estricto, los 20 comercios de colusión con T=3 quedan envenenados con todos los K y sin relevo
  al corte: los 3 honestos solo empatan con los 3 colusores y el empate conserva al incumbente. La droguería escrita a
  mano (2 colusores y 2 honestos) tampoco se corrige con K=1 ni K=2 por la misma razón.
- Con tasas 0,1, 0,35 y 0,6 ningún comercio con cambio real de nombre o de categoría llega a adoptar el valor nuevo con
  ningún K: el valor viejo tiene 2 votos y el nuevo reúne 2, que empata (verificado en la corrida 2026 con tasa 0,35 y consistente con `retraso_*_alcanzados` en 0 y 11 de 13 casos en las 12 corridas). Es la consecuencia de no tener regla de deriva.
  Por eso fallan los dos casos de vista posteriores al cambio con K=1 y K=2 (11 de 13 casos aprobados). La regla pedida no puede reproducir `cambio_nombre` ni `cambio_nombre_categoria` de `casos_vista.csv`: la sección 11 de la especificación dice que con K=2 sobre el nombre nuevo se ve el nuevo, y con estricto el empate lo impide.
- Unas 41 de las 168 o 170 llaves de comercio de cada corrida son la población de colusión guionada (20 de T=2, 20 de T=3 y la droguería). No dependen de la tasa de corrección, por eso el envenenamiento sale igual en las tres tasas. Tienen al menos 4 correcciones con nombres equivalentes, y las 20 de T=3 fijan una categoría equivocada con todos los K. Esto empuja cobertura, acierto de nombre, espurios y residuo, y por eso se reporta también el ámbito `sin_colusion`.
- Con K=2 los errores visibles de nombre en `sin_colusion` valen 0,016 en todas las semillas y tasas, es decir 2 comercios
  por corrida, el mismo número que los 2 cambios reales de nombre que nunca se alcanzan (`retraso_nombre_alcanzados` en
  0). Es una coincidencia de conteos y no se verificó llave por llave. Con K=3 los errores visibles de nombre valen 0.
- La población de colusión consolida el nombre con los tres K: la diferencia de `con_nombre_global` entre `todos` y
  `sin_colusion` es 41 con K=1, 2 y 3 en las cuatro corridas de tasa 0,35. En categoría la diferencia es 41 con K=1 y 2 y 40 con K=3.
- Pagadores y correctores: en `sin_colusion` la mediana de pagadores distintos por comercio es de 4 a 5 y la fracción con
  un solo pagador ronda 0,06. La cobertura con K=1 coincide con su cota (cualquier corrector basta) y con K=2 y K=3 queda
  por debajo de la cota por los desacuerdos entre correcciones. Hay comercios con 0 pagadores en la corrida.
- El acierto de nombre entre consolidados con K=3 vale 1 en casi todas las tasas: el ruido de nombre del generador son
  tipeos y formato, y tres usuarios casi nunca coinciden en el mismo tipeo.

## Avisos, experimento C (`avisos*.py`)

Es una PRUEBA DE ACEPTACIÓN sobre 5 usuarios y 13 etiquetas escritas a mano, sin fórmula de umbral. No es una medición
estadística y no produce tasas. Todas las reglas son propuesta sin fuente, salvo el z modificado (punto 70) y la ventana
de 7 días (punto 71). Se declararon antes de ejecutar y no se ajustó ningún parámetro a las 13 filas.

65. Módulos. `avisos.py`, `avisos_datos.py`, `avisos_estadistica.py` y `avisos_config.py` son la lógica y solo ven filas de
    `entrada/` (una prueba estática comprueba que no importan el cargador, la evaluación ni el arnés, y que no nombran la
    carpeta de verdad). Reciben las series como parámetro (`SerieDeclarada`) y no saben de dónde vienen.
    `avisos_corrida.py`, `avisos_fuentes.py`, `avisos_evaluacion.py`, `avisos_fondo.py`, `avisos_resultado.py`,
    `avisos_experimento.py` y `avisos_resumen.py` son el arnés: sí leen la verdad. Todos los índices están en
    `analisis/config/avisos.json`.
66. Dos fuentes de series, para no confundir el error de los avisos con el del detector. ORACULO: las filas de la verdad
    de series con `es_recurrente` verdadero, cargadas solo en el arnés. DETECTADAS: las series que marca el detector B con
    el óptimo restringido (w_monto 0,6, tol_monto 0,15, tolerancias de 2, 3 y 4 días, n_min 3 y umbral 0,7), con el reloj
    en la fecha de evaluación. Solo `pago_no_alcanza` y `cobro_nuevo` usan series. Los otros tres usan totales.
67. Reloj. `ahora` es el último segundo de la fecha de evaluación en Colombia (UTC menos 5), es decir el día siguiente a las
    04:59:59Z, y solo cuentan los movimientos con `ocurrido_en <= ahora`. Con 23:59:59Z el salario del perfil 2 del 31 de
    agosto (21:30 en Colombia, 02:30Z del día siguiente) saldría contado como cero. Los meses y los días son locales.
68. Gasto e ingreso. Gasto: salidas `liquidacion_confirmada` sin devolución, sin retenciones (GMF) ni envíos a personas,
    igual que la regla de referencia de la especificación. Ingreso: entradas `deposito` salvo la descripción
    `Saldo inicial`, que es un saldo y no un ingreso (con ella, abril del perfil 2 suma 1,8 millones en vez de 1). Los
    demás depósitos cuentan como ingreso, incluidos los de los usuarios de fondo. El saldo es entradas menos salidas de
    cualquier tipo hasta `ahora`, retenciones y devoluciones incluidas.
69. Línea base mensual: mediana (promedio de los dos centrales si hay una cantidad par) o media de los meses completos
    previos al mes de `ahora` que tuvieron gasto. Un mes es completo para un usuario si empieza en o después de su primer
    movimiento (fecha local). Se exigen al menos 3 meses completos y, para el gasto, 3 con gasto (una categoría con menos
    de 3 meses con gasto no se evalúa), igual que la regla de referencia, que descarta los meses sin gasto. Sin eso no se
    dispara ningún aviso. En `caida_ingreso` los meses sin ingreso sí cuentan como cero.
70. Regla R1 (multiplicador 1,0, con `prorrateo_acumulado` apagado por defecto): `presupuesto_anticipado` es el gasto acumulado del mes en curso mayor que la
    línea base. Con la bandera encendida el acumulado se proyecta al cierre del mes antes de compararlo. `categoria_sobre_media` es lo mismo por categoría y dispara si CUALQUIER categoría supera la suya. R2: z
    modificado de Iglewicz y Hoaglin, 0,6745 x (x menos centro) / MAD, con umbral 3,5
    (https://pyod.readthedocs.io/en/latest/_modules/pyod/models/mad.html). El centro es la mediana o la media según la columna y la
    MAD es siempre la mediana de las desviaciones absolutas respecto de la mediana. Con MAD cero se usa el respaldo R1.
    R2 se califica contra la columna de la línea base que corresponde, con la mediana como lectura principal.
71. `caida_ingreso`: el ingreso del último mes completo (el de `ahora` si es su último día, si no el anterior) menor que
    (1 menos 0,3) x línea base de los meses completos anteriores, y solo si MAD / mediana es menor que 0,25 (guardia
    contra el ingreso irregular). Evaluar el mes en curso a mitad de mes dispararía antes del sueldo. En R2 con MAD
    cero el respaldo es R1 completo, con su 0,3 y su guardia. `pago_no_alcanza`: una serie con pago esperado entre hoy y
    hoy + 7 días (ventana por defecto de Yodlee Projected Low Balance,
    https://developer.yodlee.com/resources/yodlee/insights-details/docs/projected-low-balance) y saldo menor que la suma de los
    montos esperados (mediana de los pagos de la serie). El umbral es el monto del pago, no el piso de 100 dólares de
    Yodlee. La fecha esperada es el último pago más k cadencias con el menor k mayor o igual que 1 tal que no queda antes de
    hoy.
72. `cobro_nuevo`: una llave con primer pago en los últimos 35 días (inclusive) que forma parte de una serie o tiene al
    menos 2 pagos de monto similar (tolerancia de 0,15 sobre la mediana). La identidad es la llave, así que el cambio de
    día o de monto de una serie existente nunca es un cobro nuevo, porque la llave ya tenía pagos antes de la ventana. Solo hay
    una etiqueta negativa en los datos: este tipo solo mide falsos positivos.
73. Categoría. En `entrada/` no hay categoría. La fuente principal es la del oráculo (`comercios.csv`). Aparte, y como
    fuente secundaria, la categoría global que infiere el experimento A con K=2 y política `sin_filtro` a la fecha de
    evaluación (una llave sin consenso queda `sin_categoria`). Con las pocas correcciones de las corridas de 3.000 filas,
    casi todo cae en `sin_categoria` y la regla por categoría degenera en la del total.
74. Evaluación. Por cada fila de `avisos_esperados.csv` y por cada combinación (R1 o R2) x (mediana o media) x (series
    ORACULO o DETECTADAS) se guarda si dispara, si debía, si pasa, la línea base, el valor, el estadístico y la causa.
    Falsos positivos sobre las etiquetas negativas de la columna correspondiente y falsos negativos sobre las positivas.
75. Usuarios sin etiqueta. Cada usuario se evalúa al final de cada mes desde el de su primer movimiento y en la fecha final. El
    conteo de avisos se rotula "sin verdad, no es tasa de falsos". Los perfiles etiquetados se evalúan también en las
    fechas sin etiqueta, en un grupo aparte (`perfil_etiquetado`), y ahí se ve cómo se comporta cada regla fuera del día para
    el que se escribió la etiqueta.

76. Población de gasto con verdad por diseño (`generador/gasto_poblacion.py`). Se activa con `--gasto-usuarios N` (por
    defecto 0, la salida de datos queda byte a byte igual). Crea N usuarios ciclando nueve diseños, con seis meses
    completos (abril a septiembre de 2026) y evaluación el 20 de octubre. Cada diseño es positivo en un tipo y negativo en
    los otros cuatro, más un control sin avisos. La verdad sale de la INTENCIÓN con brechas grandes (por ejemplo 4 veces o
    0,3 veces la base), nunca ajustada al umbral de la regla, igual que `series_fondo.py`. Un usuario de gasto son unos 50
    movimientos, así que la población encarece el corpus (27 usuarios piden 6.000 filas). El tope de avisos por usuario y
    mes está diseñado, no implementado.

77. Prorrateo como variante medida. `prorrateo_acumulado` en la configuración (por defecto apagado) proyecta el acumulado
    del mes en curso al cierre antes de compararlo con la línea base. Con la población de 27 usuarios es un intercambio
    puro: R2 pasa de 2 falsos negativos a 0 (la proyección amplifica el ritmo del perfil 4 y el z supera el umbral) y los
    falsos positivos suben de 1 a 28 (un gasto de octubre normal proyecta por encima de la base). Detalle en la bitácora
    de avisos.

78. Tope de avisos por usuario y mes (`tope_avisos_mes`, por defecto 3). Se aplica en la evaluación (`aplicar_tope` en
    `avisos_evaluacion.py`), sobre la verdad y sobre la salida de la regla con la misma prioridad: `pago_no_alcanza`,
    `caida_ingreso`, `cobro_nuevo`, `presupuesto_anticipado` y `categoria_sobre_media`. Agrupa por corrida, usuario,
    fecha de evaluación y combinación, y con 0 no recorta. Como recorta los dos lados por igual, no cambia la exactitud:
    su costo es la intención que deja de mostrarse. La población incluye el diseño `tope_excedido`, con cuatro
    intenciones el 20 de octubre, para ejercerlo.

Observaciones de la primera ejecución del experimento C (13 corridas de 3.000 filas, no son supuestos):

- R1 aprueba 13 de 13 filas en 10 corridas y 12 de 13 en las 3 corridas de semilla 11. R2 aprueba 11 de 13 en las 13
  corridas. Con las series del oráculo y con las detectadas el resultado sobre las etiquetas es idéntico en todas.
- R2 falla las dos filas del perfil 4 (`presupuesto_anticipado` y `categoria_sobre_media` del 20 de octubre) en las 13
  corridas. Los totales mensuales son 1,0, 1,3, 1,1, 1,3, 1,2 y 1,3 millones, la mediana 1,25 millones, la MAD 50.000 y el z
  modificado de octubre (1,3 millones) vale 0,6745, muy por debajo de 3,5 (1,349 con la media como centro). La etiqueta dice
  que debe disparar, la regla robusta no. Es un hallazgo y no un defecto: no se cambió el umbral. En los perfiles 1 y 2, R2
  pasa sus 5 filas de totales (1, 2, 3, 5 y 6) solo porque la MAD vale cero y entra el respaldo R1.
- La fila 9 (`caida_ingreso` del perfil 3, que debe ser false) varía entre corridas. En las 3 corridas de semilla 11, R1
  dispara: el ingreso de septiembre es 400.000 frente a una mediana de 930.000 y MAD / mediana vale 0,2258, que queda
  por debajo de la guardia de 0,25. En la corrida 2026 septiembre es 1.140.000, no hay caída y MAD / mediana vale
  0,2714, así que ahí la guardia además bloquea. La guardia de 0,25 no basta para el ingreso irregular de todas las semillas.
- Filas que pasan sin poner a prueba la regla: el perfil 5 (un solo mes completo), `pago_no_alcanza` del perfil 3 (el
  siguiente pago de Claro es el 4 de noviembre, fuera de la ventana, así que el saldo nunca se compara), `cobro_nuevo`
  del perfil 3 (la llave de Claro tiene pagos desde abril, aunque hay otra llave nueva con un solo pago que no dispara) y
  `caida_ingreso` del perfil 3 en la corrida 2026 (septiembre no cae).
- Los perfiles etiquetados en fechas sin etiqueta (grupo `perfil_etiquetado`): el 31 de julio de 2026 el perfil 3 dispara
  `caida_ingreso` con R1 y con R2 (julio 430.000 frente a una mediana de 890.000, MAD de 60.000, MAD / mediana de 0,067, z
  de -5,2). Es el perfil de ingreso irregular y no hay etiqueta ese día. El perfil 2 dispara `cobro_nuevo` el 30 de
  septiembre: dos pagos de unos 88.760 a una misma llave nueva (el 7 y el 25) cumplen "2 pagos de monto similar".
- Usuarios sin etiqueta de la corrida 2026 (106 usuarios, sin verdad, no es tasa de falsos): con R1 y mediana salen 82 avisos
  `categoria_sobre_media` sobre 176 evaluaciones evaluables, 57 `presupuesto_anticipado` sobre 210, 0 `caida_ingreso` sobre 381,
  3 `pago_no_alcanza` sobre 381 y 6 `cobro_nuevo` sobre 381 (5 con series detectadas). Con R2 y mediana bajan a 42, 20, 0, 3 y 6. El
  acumulado sin prorratear se acerca a la mediana al final de cada mes, por eso la proporción de gasto es tan alta.
- La categoría inferida con K=2 aprueba lo mismo que la del oráculo, pero casi todo cae en `sin_categoria` y la regla por
  categoría degenera en la del total.

## Lo que no se verificó

- Los pesos, tolerancias, cadencias y el umbral siguen sin fuente externa. Solo Plaid está citada en
  el plan (3 ocurrencias para una serie madura) y no se contrastó aquí.
- La mejor configuración se ajusta al modelo de recurrencia del generador. Con datos reales el
  rendimiento puede ser muy distinto.
- Solo se usaron corridas de 3.000 filas con la variante A (y la B de la semilla 2026 como
  comprobación). No se probó con otros tamaños.
- Convergencia: la agrupación por similitud no se midió en la primera pasada (después se midió, ver la bitácora). K no se
  calibró. La regla de promoción y de relevo no tiene fuente externa. La regla de deriva `reciente` se probó (extensión
  A4) y no se calibró, solo se midió a tasa 0,35. No se probó el peso decreciente con semivida ni otra regla de deriva.
- Clasificadores del residuo: el TF-IDF local se midió (ver la bitácora de convergencia). MiniLM y el LLM por API no se
  midieron.

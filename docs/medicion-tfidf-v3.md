# Medición del clasificador con el artefacto TF-IDF v3

Este informe mide el clasificador de la cascada antes y después del reentrenamiento con el catálogo
v3. Cada cifra sale de un comando que está escrito aquí, de un artefacto con huella conocida y de un
archivo de salida con su sha256. Lo que baja, baja: el catálogo v3 perdió nombres y el clasificador
decide menos.

## Qué se midió y contra qué

| Pieza | Valor |
| --- | --- |
| Hoja de evaluación | `dataset-inteligencia/oro/v2/etiquetas-v2-evaluacion.csv`, sha256 `09df5e5cc69ed9ee4cd3ce712c1a7ec9b9bc48c10a0213fb153a143722d32c21` (150 filas, 122 con etiqueta) |
| Partición | `dataset-inteligencia/oro/v1/conjunto.csv`, parte `prueba` (150 filas) |
| Artefacto antes | `tfidf-v2`, sha256 `fe69c5efda1f885cb9bd0de0fcd835d7cf098c976f9eb868fddf29d79e864253`, 529 marcas, 11862 nombres, 9 centroides |
| Artefacto después | `tfidf-v3`, sha256 `7aa998a833ec111de76c6d3c56a8f0780903b2c1927f839d9c41c211d9e0bbea`, 430 marcas, 5728 nombres, 7 centroides, 737436 bytes |
| Catálogo del modo `todos` | `dataset-inteligencia/catalogo/v3/catalogo_v3.csv`, sha256 `f58eb87ebd08ddac733d7ff91098a6e413ab48911e1eb310a827795a93348ccb` |

El artefacto v3 se entrenó sin semillas de léxico (`--sin-lexico`), igual que el v2 desplegado: el
medidor de paridad rechaza un artefacto con semillas porque la regla que se despliega no las lleva. El
antes se midió en un worktree temporal (`git worktree add /tmp/vera-v2 9989f67`), con el v2 todavía
en el árbol, y no se commiteó nada desde ahí.

## Comandos

Los tres modos, con el artefacto después:

```bash
go run ./cmd/evaluar-clasificador -modo clasificador \
  -csv <dataset>/oro/v2/etiquetas-v2-evaluacion.csv \
  -conjunto <dataset>/oro/v1/conjunto.csv -salida /tmp/clasificador-v3.json
go run ./cmd/evaluar-clasificador -modo primer-corte \
  -csv <dataset>/oro/v2/etiquetas-v2-evaluacion.csv \
  -conjunto <dataset>/oro/v1/conjunto.csv -salida /tmp/primer-corte-v3.json
go run ./cmd/evaluar-clasificador -modo todos \
  -csv <dataset>/oro/v2/etiquetas-v2-evaluacion.csv \
  -conjunto <dataset>/oro/v1/conjunto.csv \
  -catalogo <dataset>/catalogo/v3/catalogo_v3.csv -salida /tmp/todos-v3.json
```

El antes corre los mismos comandos desde `/tmp/vera-v2` y escribe `/tmp/clasificador-v2.json`,
`/tmp/primer-corte-v2.json` y `/tmp/todos-v2.json`. El arnés no acepta `-catalogo` con
`primer-corte` (lo rechaza por diseño), así que `primer-corte` se mide sin catálogo; `todos` lleva el
catálogo v3 en las dos corridas para que el catálogo no se confunda con el reentrenamiento.

## Antes y después

| Modo | Artefacto | Filas | Con etiqueta | Mostradas | Correctas | Precisión | Wilson 95% | Cobertura | Abstenciones |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| clasificador | v2 | 150 | 122 | 10 | 9 | 0,900 | 0,596 a 0,982 | 0,082 | 112 |
| clasificador | v3 | 150 | 122 | 8 | 7 | 0,875 | 0,529 a 0,978 | 0,066 | 114 |
| primer-corte | v2 | 150 | 122 | 10 | 9 | 0,900 | 0,596 a 0,982 | 0,082 | 112 |
| primer-corte | v3 | 150 | 122 | 8 | 7 | 0,875 | 0,529 a 0,978 | 0,066 | 114 |
| todos | v2 | 150 | 122 | 13 | 12 | 0,923 | 0,667 a 0,986 | 0,107 | 109 |
| todos | v3 | 150 | 122 | 12 | 10 | 0,833 | 0,552 a 0,953 | 0,098 | 110 |

`clasificador` y `primer-corte` dan lo mismo porque el catálogo curado no enciende en este modo y el
léxico del artefacto está vacío: el escalón que decide es el sexto.

La paridad en desarrollo, medida con `dataset-inteligencia/medir_desarrollo_tfidf.py` sobre las 300
filas de la partición de desarrollo del oro v1 (nunca la de prueba):

| Artefacto | Decididas | Correctas |
| --- | --- | --- |
| v2 | 39 | 38 |
| v3 | 13 | 8 |

El desplome de cobertura viene del catálogo: el v3 no sostiene `vivienda_servicios` ni
`comisiones_costos_financieros` y trae 5728 nombres frente a 11862. Menos nombres por centroide es un
centroide más corto, y con el umbral de coseno 0,16 la consulta queda por debajo más veces. No se
retocó el umbral ni se podó nada para maquillar la cifra.

## Aperturas del informe

Parte medida (el arnés imprime una sola, porque la hoja de evaluación cae completa en `prueba`):

| Parte | Filas | Con etiqueta | Mostradas | Correctas | Precisión | Cobertura |
| --- | --- | --- | --- | --- | --- | --- |
| prueba | 150 | 122 | 12 | 10 | 0,833 | 0,098 |

Estratos con alguna fila mostrada (modo `clasificador`, después):

| Estrato | Con etiqueta | Mostradas | Correctas | Precisión |
| --- | --- | --- | --- | --- |
| cadenas | 13 | 1 | 1 | 1,000 |
| combustible_y_transporte | 9 | 3 | 3 | 1,000 |
| comida_y_bebida | 10 | 1 | 1 | 1,000 |
| educacion | 6 | 1 | 0 | 0,000 |
| prendas_calzado_farmacia | 7 | 2 | 2 | 1,000 |

Categorías con alguna fila mostrada (modo `clasificador`):

| Categoría | v2 etiquetadas/mostradas/correctas | v3 etiquetadas/mostradas/correctas |
| --- | --- | --- |
| salud | 18 / 3 / 3 | 18 / 3 / 3 |
| alimentos_mercado | 13 / 2 / 2 | 13 / 0 / 0 |
| vivienda_servicios | 12 / 1 / 0 | 12 / 0 / 0 |
| restaurantes_domicilios | 11 / 1 / 1 | 11 / 1 / 1 |
| transporte | 10 / 3 / 3 | 10 / 3 / 3 |
| educacion | 5 / 0 / 0 | 5 / 1 / 0 |

Los dos errores de lo mostrado: en v2 una fila `vivienda_servicios` predicha como `transporte`; en v3
una fila `educacion` predicha como `otros_gastos`.

## La compuerta y la decisión

El umbral acordado es precisión de lo mostrado 0,90. Después del reentrenamiento el clasificador
queda en 0,875 y primer-corte en 0,875, con el límite inferior del Wilson en 0,529: no alcanza.
`todos` con el catálogo v3 queda en 0,833, también por debajo. Por eso `CLASIFICADOR` sigue apagado
en producción y el valor queda escrito con esta medición, sin fecha nueva ni recalibración. En
`local` y `demo` la matriz versionada (`infra/servidor/banderas.tsv`) mantenía `tfidf` para medir la
cascada completa, y la compuerta que se incumple es la de producción: encenderlo allí sería afirmar
una precisión que el artefacto v3 no sostiene. La decisión se materializa en la tarjeta `C-04`.

Lo que este informe no mide: memoria de contraparte, cuenta interna y MCC, que viven en Postgres y el
comando no levanta. Tampoco mide el efecto del catálogo curado con el artefacto v2 aislado del
reentrenamiento. Ambas quedan fuera por alcance, no por omisión silenciosa.

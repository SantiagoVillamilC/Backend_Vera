# Reconstruir la copia enriquecida de la base de demo

La copia de `inteligencia.movimiento_enriquecido` se puede borrar y volver a calcular. Este
documento describe cómo reconstruirla con los seis escalones encendidos y cómo comprobar que
cada origen quedó representado.

**No hay base de demo en esta sesión.** La corrida que se guardó como evidencia se hizo sobre la
base desechable sembrada `vera_semilla_m8` en `127.0.0.1:55437`, no sobre la copia de demo. Los
pasos de este documento son los de la demo y no se ejecutaron contra ella, así que el criterio de
aceptación queda comprobado sobre una base desechable, no sobre la demo.

## Qué enciende cada escalón

`escalonesDelLote` en `internal/inteligencia/enriquecimiento.go` es el único punto que decide
qué señales entran al lote. El orden de la cascada no cambia: memoria, contraparte interna,
MCC, catálogo, léxico y clasificador.

| Escalón | Origen en la copia | Cuándo entra |
| --- | --- | --- |
| 1 | `memoria` | siempre |
| 2 | `contraparte_interna` | siempre |
| 3 | `mcc` | solo en la rama de QR a comercio: el escalón se mira después de descartar la rama de llave |
| 4 | `catalogo` | solo si `inteligencia.catalogo_marca` tiene filas |
| 5 | `lexico` | `LEXICO=v2` |
| 6 | `tfidf` | `CLASIFICADOR=tfidf` |
| 7 | `sin_categoria` | cierre de la cascada, o abstención del clasificador en el escalón 6 |

La abstención del clasificador se guarda con origen `sin_categoria` y escalón 6: el origen dice que
ningún peldaño dio categoría y el escalón dice que el último que la miró fue el 6.

El escalón 3 no se evalúa en los pagos por llave ni cuando la contraparte es una persona.
`Decidir` cierra esa rama con `sin_categoria` antes de mirar el MCC, y `escribirMovimiento` marca
con canal de llave a todo destino persona, así que una transferencia entre personas nunca llega a
ese escalón aunque el asiento traiga un MCC.

`cmd/enriquecer` no usa banderas. Recibe el modo como primer argumento (`lote` o `reconstruir`)
y un límite opcional. Lo demás se configura con variables de entorno:

- `APP_DATABASE_URL`: obligatoria. Es la cadena del rol de aplicación, no la del dueño.
- `LEXICO`: vacío o `apagado` deja el escalón 5 fuera. `v2` lo enciende.
- `CLASIFICADOR`: vacío o `apagado` deja el escalón 6 fuera. `tfidf` carga el artefacto
  embebido.
- El catálogo se carga siempre desde `inteligencia.catalogo_marca` con `CargarCatalogo`. Sin
  filas, el escalón 4 queda apagado y la copia no cambia por él.

## Comando

Compila los binarios y reconstruye la copia completa sobre la base de demo.

```bash
make build
export APP_DATABASE_URL='postgres://rol_api:CLAVE@127.0.0.1:55437/vera_semilla_demo?sslmode=disable'
LEXICO=v2 CLASIFICADOR=tfidf out/bin/enriquecer reconstruir 5000
```

`reconstruir` borra la copia, reinicia el cursor de enriquecimiento y vuelve a calcular todos
los movimientos en lotes del tamaño indicado. Imprime una huella al final, útil para comparar
dos reconstrucciones. No toca el ledger ni los asientos.

Para el camino incremental usa `lote` en lugar de `reconstruir`.

```bash
LEXICO=v2 CLASIFICADOR=tfidf out/bin/enriquecer lote 5000
```

Sin `LEXICO` ni `CLASIFICADOR` el comando igual escribe la copia, pero solo con memoria,
contraparte interna, MCC y catálogo. Es el modo correcto para comprobar que el escalón 4
funciona sin ayuda de los otros dos.

## Comprobación

Con `DATABASE_URL` (rol dueño) sobre la misma base:

```sql
SELECT origen_categoria, count(*)
  FROM inteligencia.movimiento_enriquecido
 GROUP BY 1
 ORDER BY 2 DESC;
```

El criterio de aceptación pide al menos cinco orígenes distintos. La copia de demo debe traer
`memoria`, `contraparte_interna`, `mcc`, `catalogo`, `lexico` y las filas de `sin_categoria`
que ningún escalón resolvió.

Para revisar que el catálogo no dejó nombres suyos en `sin_categoria`:

```sql
SELECT m.comercio_nombre, m.contraparte_nombre_normalizado, c.nombre_normalizado, count(*)
  FROM inteligencia.movimiento_enriquecido m
  JOIN inteligencia.catalogo_marca c
    ON c.nombre_normalizado = left(m.contraparte_nombre_normalizado,
                                   length(c.nombre_normalizado))
 WHERE m.categoria = 'sin_categoria'
   AND m.comercio_nombre IS NOT NULL
 GROUP BY 1, 2, 3;
```

La consulta debe devolver cero filas. El cruce no es un join por el nombre completo: el catálogo
guarda la llave de `ClaveDeCatalogo`, que recorta el nombre a 25 puntos de código antes de
normalizarlo, mientras la copia guarda `contraparte_nombre_normalizado`, que normaliza el nombre
completo con un tope de 40. Esa diferencia de largo no rompe el cruce: el recorte y la normalización
son locales sobre el mismo prefijo del nombre, así que `ClaveDeCatalogo(nombre)` siempre queda como
prefijo de `NormalizarNombre(nombre)`.

- Cuando el corte cae dentro de una corrida de separadores, la corrida se colapsa a un guion en los
dos lados y el guion final se recorta, así que la llave sale más corta y sigue siendo prefijo. Con
`AAAAAAAAAA__________BBBBBBBBBB` la llave es `AAAAAAAAAA-BBBBB` y el nombre normalizado es
`AAAAAAAAAA-BBBBBBBBBB`.
- Cuando el corte deja un punto, un apóstrofo o un ampersand en el borde, la normalización los quita
en los dos lados, así que la llave vuelve a quedar más corta y sigue siendo prefijo. Con
`ABCDEFGHIJKLMNOPQRSTUVWX.YZ` la llave es `ABCDEFGHIJKLMNOPQRSTUVWX` y el nombre normalizado es
`ABCDEFGHIJKLMNOPQRSTUVWXYZ`.

Por eso la consulta no pierde una coincidencia real: si el escalón 4 hubiera encontrado el nombre, la
llave que produce `ClaveDeCatalogo` sobre ese nombre es prefijo del valor de la columna y el cruce la
devuelve. Lo que el tamiz agrega son candidatos falsos, porque acepta cualquier llave del catálogo
que sea prefijo del nombre normalizado y no sabe dónde cayó el corte a 25. Un nombre cuyo corte da
`ALMACEN-EXITO-CENTRO-COM` también cruza con la llave más corta `ALMACEN-EXITO-CENTRO`. Una fila
devuelta es un candidato que hay que confirmar con `ClaveDeCatalogo` antes de darla por un fallo del
escalón, y cero filas sí descartan que el escalón 4 haya dejado sin categoría un nombre que el
catálogo conoce. El caso de los dos ejemplos está fijado en
`TestClaveDeCatalogoNoCoincideConElRecorteDeLaColumna` y la propiedad de prefijo que sostiene el
cruce, en `TestClaveDeCatalogoEsPrefijoDelNombreNormalizado`, los dos en
`internal/inteligencia/normalizar_test.go`. La versión anterior de esta consulta,
`rtrim(left(m.contraparte_nombre_normalizado, 25), '-')`, sí perdía coincidencias: contra la llave
`AAAAAAAAAA-BBBBB` comparaba el prefijo `AAAAAAAAAA-BBBBBBBBBB` y no encontraba nada.

La consulta de diagnóstico por mes vive en `bd/consultas-inteligencia/enriquecimiento.sql`
como `ConteoPorOrigenYMes`. Agrupa por mes en UTC y por origen, y se puede correr a mano:

```sql
SELECT date_trunc('month', ocurrido_en AT TIME ZONE 'UTC')::date AS mes,
       origen_categoria,
       count(*) AS total
  FROM inteligencia.movimiento_enriquecido
 GROUP BY 1, 2
 ORDER BY 1, 2;
```

El mes se calcula en UTC a propósito: `date_trunc` sobre la zona horaria de la sesión partiría el
año en meses distintos según el cliente que consulte la copia.

## Evidencia de la corrida

Los criterios se comprueban con la salida de la reconstrucción y de las consultas, no con una
afirmación. Guarda los artefactos en la misma carpeta y nombra la base y la fecha.

```bash
make build
export APP_DATABASE_URL='postgres://rol_api:CLAVE@127.0.0.1:55437/vera_semilla_demo?sslmode=disable'
export DATABASE_URL='postgres://dueno:CLAVE@127.0.0.1:55437/vera_semilla_demo?sslmode=disable'
LEXICO=v2 CLASIFICADOR=tfidf out/bin/enriquecer reconstruir 5000 2>&1 | tee evidencia/lote.log
```

Del log salen la línea `reconstruccion huella=...` y el resumen del lote con `por_origen`, que es
el conteo por origen que vio el proceso. Después, con el rol dueño:

```bash
psql "$DATABASE_URL" -c "SELECT origen_categoria, count(*) FROM inteligencia.movimiento_enriquecido GROUP BY 1 ORDER BY 2 DESC" | tee evidencia/origenes.txt
psql "$DATABASE_URL" -c "SELECT count(*) FROM inteligencia.catalogo_marca" | tee evidencia/catalogo.txt
psql "$DATABASE_URL" -c "SELECT m.comercio_nombre, count(*) AS candidatos FROM inteligencia.movimiento_enriquecido m JOIN inteligencia.catalogo_marca c ON c.nombre_normalizado = left(m.contraparte_nombre_normalizado, length(c.nombre_normalizado)) WHERE m.categoria = 'sin_categoria' AND m.comercio_nombre IS NOT NULL GROUP BY 1 ORDER BY 1" | tee evidencia/sin_categoria.txt
```

El comando imprime dos columnas, `comercio_nombre` y `candidatos`, que es lo que guarda
`evidencia/sin_categoria.txt`. La consulta de la sección Comprobación pide además las dos columnas
normalizadas para revisar un candidato a mano, así que su salida tiene cuatro columnas y no es la del
archivo.

`evidencia/origenes.txt` tiene que listar al menos cinco orígenes distintos y
`evidencia/sin_categoria.txt` tiene que salir sin filas candidatas. Si la base de demo no alcanza ese
número, el criterio no está cumplido y la reconstrucción tampoco lo arregla: falta algún origen en
los asientos, no en el código.

La evidencia de esta sesión está en `/home/chris/.claude/harness/scratch-rw/evidencia-a1-03/`. Se
tomó sobre `vera_semilla_m8`, la base desechable sembrada, no sobre `vera_semilla_demo`, que no
existe en esta sesión. No hay base de demo en esta sesión, así que el criterio queda medido sobre
una base desechable.

La reconstrucción de esa corrida salió con seis orígenes distintos: `lexico`, `sin_categoria`,
`tfidf`, `memoria`, `contraparte_interna` y `mcc`. No apareció `catalogo` aunque la tabla tenía 5728
filas cargadas, porque ninguno de los nombres de los asientos sembrados coincide con una llave del
catálogo. El control de `sin_categoria` salió sin candidatos. Los seis orígenes de escalón, con
`catalogo` incluido, están cubiertos por `TestElLoteEscribeElOrigenDeCadaEscalon`, que siembra el
nombre del catálogo a propósito.

## Pruebas

Las pruebas unitarias no necesitan base.

```bash
go test ./internal/inteligencia/
```

Las de integración necesitan `APP_DATABASE_URL` y una base desechable ya migrada.
`TestElLoteEscribeElOrigenDeCadaEscalon` enciende los seis escalones con el catálogo cargado por
`CargarCatalogo`, comprueba escalón, origen y categoría de cada uno con `Decidir`, siembra un
asiento por escalón, corre `Enriquecer` y vuelve a leer `origen_categoria` en
`inteligencia.movimiento_enriquecido`. Exige los seis orígenes exactos, uno por escalón.
`TestConteoPorOrigenYMesAgrupaLaCopia` ejercita la consulta de diagnóstico.

La prueba borra sus filas de la copia y de la memoria. Los asientos que siembra no se pueden borrar
porque el ledger es de solo inserción para el rol de la aplicación, y por eso la prueba deja el
cursor del lote por delante del último asiento sembrado: como el ledger solo crece hacia adelante,
un cursor más alto no salta ningún asiento y evita que un lote posterior reprocese los seis de la
prueba. Una reconstrucción sí los vuelve a procesar, porque reinicia el cursor y recorre el ledger
completo, así que cada corrida de la prueba deja seis asientos más en la copia reconstruida. Aun así,
córrela contra una base desechable.

Las pruebas que siembran `inteligencia.catalogo_marca` usan nombres `PRUEBA-<sufijo>` y un origen
propio de cada corrida, y limpian solo las filas de ese origen, así que no tocan las curadas que viven
en `bd/datos/catalogo-v3`. Ninguna vacía la tabla ni `inteligencia.categoria`. La prueba de extremo a
extremo del escalón 4 lee del archivo curado el nombre y la categoría, y si ese nombre no está en la
tabla lo escribe con un origen propio y lo retira al terminar: corre igual sobre una base recién
migrada y sobre una sembrada con `go run ./cmd/cargar-catalogo -aplicar`.

`TestEnriquecimientoDeUnaTransferenciaUnFondeoYUnGMF` llama `Reconstruir` sobre un servicio sin
`LEXICO` ni `CLASIFICADOR`, y `Reconstruir` borra la copia y reinicia el cursor, así que esa prueba
deja `inteligencia.movimiento_enriquecido` rehecha con menos escalones. Por eso la evidencia del
criterio se toma con la reconstrucción de la sección anterior después de la suite, no antes: correr
las pruebas encima de una copia ya medida la sustituye por una sin léxico ni clasificador.

```bash
go test -tags=integration ./internal/inteligencia/
```

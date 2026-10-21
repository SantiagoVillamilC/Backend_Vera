# Consulta y resumen de movimientos — SCRUM-127 / IA-A3-02

El contenido de la tarjeta coincide con HU-BE-IA-10 del DOCX. Sus criterios incluyen
además rango máximo de 366 días, total de movimientos, un movimiento por asiento,
aislamiento por usuario y periodos vacíos sin error.

## Contrato del catálogo

`listar_movimientos(desde,hasta,tipo?,limite?)` acepta fechas reales `YYYY-MM-DD`
inclusivas en Colombia (UTC-05:00), desde no posterior a hasta y hasta 366 días
calendario inclusive. El límite predeterminado y máximo es 20. `tipo` acepta únicamente
los seis motivos del núcleo. La validación se hace tanto en catálogo como en núcleo.

Devuelve `movimientos`, `total` y `hay_mas`. `total` cuenta todos los asientos que
coinciden con el filtro, no sólo los mostrados. El orden es por fecha descendente y,
en caso de empate, por ID de asiento descendente; ese ID no se envía al modelo.

Cada fila contiene fecha de Colombia, motivo principal, monto con signo, contraparte
enmascarada si está disponible y `componentes` con motivo e importe exactos. Una retención
con envío y GMF es **un asiento**, con dos componentes y monto total negativo. No se suma
el posting de retenido, que sólo es la contrapartida contable del envío. Cuando se filtra
por motivo se conservan exclusivamente sus componentes: `tipo=gravamen` devuelve el
gravamen, no el principal. Las liquidaciones que sólo tocan retenido no duplican el gasto.

`resumir_movimientos(desde,hasta,agrupar_por)` admite `motivo` o `contraparte`. Devuelve
entradas y salidas positivas, neto con signo, cantidad de asientos y hasta diez grupos.
Los totales globales abarcan todo el periodo aunque se recorte la lista de grupos.
Cada grupo contiene esos importes, conteo y `total = entradas + salidas` como volumen.
El orden es por ese volumen descendente, con desempate estable por la clave interna.
`hay_mas` informa grupos omitidos. Las sumas, restas y valores absolutos usan `Centavos`
con comprobación de desbordamiento; no se delega aritmética ni redondeo al modelo.

El conteo global y el de cada grupo cuenta un asiento una vez. Por motivo, un asiento
con envío y GMF participa en ambos grupos: sus conteos no deben sumarse para deducir el
conteo global. Por contraparte se agrupan los componentes asociados al mismo asiento,
incluido el costo de su GMF. No se fusionan personas distintas aunque sus etiquetas
enmascaradas sean iguales.

## Consulta y fecha indexada

El núcleo conserva el historial HTTP anterior. Los nuevos métodos `MovimientosEnRango`
y `ResumirMovimientos` utilizan `ApuntesDePeriodo`, generado por sqlc, con fechas en
intervalo `[desde, día posterior a hasta)`. Sólo selecciona asientos que tocaron el
disponible propio y usa la lógica existente `derivarLineas` de HU-NF-05 para sus motivos.
Las líneas necesarias de la contrapartida se usan dentro del núcleo para clasificar;
no se proyectan cuentas ajenas al resultado del modelo.

Se leen bloques de 500 asientos con cursor compuesto de fecha e ID, dentro de una única
transacción `REPEATABLE READ, READ ONLY`: listado, conteo y resumen comparten snapshot
aun si llegan inserciones concurrentes o con fecha anterior. No se carga el historial
completo en memoria: el listado retiene como máximo 20 filas, y el resumen acumula un
registro por clave de grupo antes de ordenar y recortar. Se respeta el contexto y plazo.

La migración 0027 añade `posting.ocurrido_en`, rellena los postings previos desde su
asiento y crea un trigger que fija la fecha al insertar, incluso si se proporciona otra.
Es una copia derivada de `journal_entry.ocurrido_en`, inmutable para el rol de aplicación.
Así puede existir el índice compuesto `(cuenta_id, ocurrido_en DESC, entry_id DESC)`;
un índice de journal por sí solo no combinaría fecha y cuenta. Se añade también un
índice de transferencia/tipo de asiento para resolver la recepción interna. El rollback
quita esa copia e índices, conservando fechas originales, asientos e importes.

## Contrapartes y fronteras

La unión con `nucleo.transferencia` identifica la operación. En recibidos se toma el
emisor; en enviados se prefiere la cuenta receptora realmente asentada. Si aún no existe
ese asiento se admite una referencia UUID estable de destino. Una llave externa queda
como referencia interna para agrupar, pero no se divulga ni se convierte en un nombre
inventado. No se resuelve un teléfono antiguo contra su dueño actual.

Después de cerrar la transacción del núcleo, el manejador pide a auth únicamente los
nombres de las referencias de esa página o de sus diez grupos. Auth consulta por UUID,
no por búsquedas libres ni por teléfonos. El catálogo enmascara los nombres antes de
enviarlos al proveedor. Sin nombre, la lista muestra contraparte vacía y los grupos usan
`Sin contraparte` o `Contraparte no disponible`. No se entregan UUID, cuentas ni llaves.
El modelo no puede suministrar usuario, parte ni referencias de terceros como argumentos.

## Validación y alcance

Las funciones quedan conectadas en `cmd/api`. No hay endpoints HTTP nuevos en esta HU;
la exposición del chat sigue en SCRUM-129. Se mantienen las protecciones previas de
consentimiento, cuotas y orquestación.

Las pruebas unitarias verifican fechas, año bisiesto, 366 días, exactitud, diez grupos,
conteos por asiento y desbordamiento. La integración con PostgreSQL prueba los seis
motivos, bordes de medianoche, usuario distinto de ParteID, terceros, contrapartes con
la misma máscara, filtros, periodos vacíos y 501 asientos con empates de fecha.
Los esperados se calculan a partir de importes sembrados conocidos. También verifica
que el cliente no pueda elegir la fecha del índice. Una prueba de migración separada
crea datos anteriores a 0027, aplica, revierte y reaplica sin perder importes ni fechas.

La migración de este índice se numeró 0027 al integrar la rama del asistente con
`dev`: 0021–0026 pertenecen a inteligencia. El SQL del índice y su backfill no
cambiaron y no se renumeró ninguna migración ya publicada en `dev`.

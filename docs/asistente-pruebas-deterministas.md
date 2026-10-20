# Casos deterministas — SCRUM-141 / IA-A9-01

Esta tarjeta cubre el conjunto versionado y las pruebas simuladas de HU-BE-IA-14.
La evaluación con Gemini, sus métricas y la primera línea base pertenecen a
SCRUM-139 / IA-A9-02. Un resultado verde aquí no mide la comprensión ni la seguridad
de un modelo real: verifica lo que hace el backend ante un guion conocido.

## Ejecutar

Sin claves, PostgreSQL, Redis ni acceso de red en tiempo de ejecución:

```sh
go test -race ./internal/copilot/evaluacion ./cmd/evaluar-vera
go run ./cmd/evaluar-vera > informe-vera.json
go run ./cmd/evaluar-vera -casos ruta/casos.json
```

El comando devuelve `0` si pasan todos los casos, `1` ante una regresión y `2`
ante configuración, lectura, cancelación o escritura inválida. `go run` conserva
el fallo aunque su propio código de salida sea `1`; para distinguir `1` de `2`
se usa el binario compilado. El informe JSON incluye modo, versiones, SHA-256
del conjunto y aserciones por caso. No imprime entradas, respuestas, argumentos,
instrucciones ni datos financieros. Es reproducible: no contiene latencias,
fechas de ejecución ni identificadores aleatorios.

## Conjunto y oráculo

`internal/copilot/evaluacion/casos.json` contiene 52 casos con ID estable,
categoría, entrada, llamadas esperadas, argumentos esperados, códigos del catálogo
y restricciones de respuesta. La referencia es **2026-10-19, 10:00 UTC-5**:
«la semana pasada» corresponde al 12–18 de octubre. El reloj se inyecta en el
orquestador para que las instrucciones conserven esa referencia en otras fechas.

El campo `guion` define las respuestas del proveedor simulado y es independiente
de `llamadas_esperadas` y `restricciones`. El ejecutor nunca construye la respuesta
simulada a partir del oráculo. Tests negativos cambian función, argumentos,
importe, código esperado, texto obligatorio y presencia de tarjeta para comprobar
que el evaluador falla. Rechaza conjuntos con menos de 40 casos, IDs duplicados,
categorías obligatorias ausentes, pasos ambiguos y expectativas incompletas.

Categorías cubiertas:

| Categoría | Ejemplos |
|---|---|
| Consultas | Saldo disponible/retenido, máximo enviable, cupo mensual y GMF |
| Argumentos | Hoy, ayer, semana pasada, rango mensual, filtro y límite, grupos, celular, UUID, «50 mil», llave y referencia opaca |
| Aclaración | Monto o destinatario ausente, «cincuenta», llave numérica ambigua, varias coincidencias |
| Fuera de alcance | Clima, diagnóstico y programación |
| Inyección | Revelar instrucciones, copia literal bloqueada, función de ejecución inexistente, identidad ajena, nombres maliciosos, rol elevado |
| Errores y topes | Proveedor caído/timeout, consentimiento ausente, función no disponible, transferencia ajena, búsqueda limitada, destino propio, cinco vueltas y ocho funciones |

Usa `provsim`, el orquestador y el catálogo de producción. Los manejadores y la
memoria son fixtures de puertos; validación de argumentos, normalización de
celulares, conversión de pesos a centavos, enmascarado, serialización, límites y
bloqueos provienen del código real. Las herramientas no tienen un puerto que
ejecute dinero. La tarjeta se obtiene de la memoria del servidor y se compara
con ella, no con una frase generada por el proveedor.

## Cifras y alcance de las verificaciones

El verificador busca importes con `$`, `COP` o `pesos` y exige que coincidan con
un campo `centavos` de un resultado de herramienta de ese mismo ciclo. No toma
importes del guion ni del oráculo como fuente de verdad. Acepta la representación
canónica del backend (`120000.00 COP`), formato colombiano (`$120.000,00`) y
agrupaciones válidas con coma (`$120,000.00`). Usa
enteros y comprueba desbordamiento, signos y centavos superiores al límite seguro
de JavaScript. Nunca calcula un saldo o un resumen a partir de la narración.

La detección automática no interpreta cantidades escritas con palabras, monedas
distintas de COP ni afirmaciones financieras sin marcador monetario. Las
restricciones de texto complementan esta comprobación en los casos versionados;
la evaluación real deberá registrar sus límites y revisión cuando corresponda.

La persistencia y el dinero se verifican además en las integraciones existentes:
`confirmacion_integration_test.go` (OTP, revalidación, atomicidad, duplicados y
concurrencia), `historial_integration_test.go` (aislamiento, borrado y retención)
y `vera_texto_integration_test.go` (rutas, consentimiento, reintento y voz).
Los casos simulados no sustituyen esas pruebas con PostgreSQL y Redis reales.

## CI y cambios futuros

`go test ./...` ejecuta los casos sin tags especiales. El workflow también ejecuta
el comando y publica totales, versiones, huella y fallos en el resumen del job.
Cualquier caso fallido bloquea el paso; no se aplica el umbral del 90 % de la
evaluación probabilística a los tests simulados. No se llama a Gemini en CI.

Al cambiar instrucciones, funciones o comportamiento se revisan los casos y sus
expectativas explícitamente; no se regeneran para aceptar automáticamente la
implementación. El conjunto puede ampliarse manteniendo los IDs anteriores.

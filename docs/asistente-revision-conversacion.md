# Revisión de conversación continua — IA-A9-02

Revisión local del 20 de octubre de 2026 (informes en UTC del 21). Datos
financieros ficticios, modelo `gemini-3.5-flash-lite`, proyecto Free Tier
confirmado por el operador. No se habilitó facturación ni se cambió de modelo.

## Hallazgos y correcciones

1. **Bloqueo real de una consulta normal.** En la secuencia anterior, el turno
   «¿Podría pasarle 30 mil pesos a Laura? Solo estoy preguntando…» recibió
   `ProviderSafety`, sin usar micrófono ni TTS. El adaptador imponía
   `BLOCK_MEDIUM_AND_ABOVE` en las cuatro categorías. Se cambió a
   `BLOCK_ONLY_HIGH`, conservando los filtros y el descarte de cualquier salida
   bloqueada. La comparación no permite atribuir cada falso positivo a una
   categoría de Google ni demuestra que esperar 30 segundos sea su causa.
2. **Veinte solicitudes no eran veinte turnos.** Transcripción, vueltas de
   function calling, redacción, TTS y reintentos consumen solicitudes físicas.
   El laboratorio arrancaba con 20 para todo. Ahora arranca con 120 y muestra
   ambos contadores; este margen local no aumenta la cuota de Google.
3. **Transcripción anterior visible junto a un error nuevo.** Se limpiaba el
   error, pero quedaba el texto del clip anterior. Ahora se limpia al comenzar
   otra grabación. La etapa de transcripción tiene su propio código de bloqueo,
   se conserva el clip fallido en memoria y se ofrece reintentar sin grabar otra vez.
4. **JSON válido con texto dañado.** La primera prueba hablada produjo
   caracteres de control dentro de «cómo estás». El parser aceptaba ese texto.
   Ahora lo rechaza y permite un solo nuevo intento si la salida es mal formada.
   No modifica palabras para adivinar la pregunta ni reintenta un bloqueo de seguridad.
5. **Una respuesta breve podía originar una propuesta incorrecta.** La primera
   corrida coloquial interpretó «pues cincuenta» como 50.000 y preparó una
   propuesta. Se añadió una aclaración determinista para importes aislados de
   ese tipo, antes de llamar al proveedor o a herramientas. No interpreta
   selecciones numéricas de destinatarios ni pretende resolver todo el lenguaje natural.
6. **El fallo de redacción interrumpía el turno después de obtener datos.** El
   perfil del laboratorio puede usar las lecturas autorizadas del turno actual
   para construir un respaldo con cifras exactas. Nunca muestra texto parcial
   bloqueado ni rescata importes de otra consulta. Sin lecturas, reconoce que
   no pudo responder y permite continuar. El respaldo queda identificado y
   conserva un motivo técnico. No encubre cuotas, credenciales ni cancelación.
7. **TTS tiene disponibilidad propia.** Google limitó la síntesis durante las
   pruebas, mientras el chat seguía contestando. La voz del navegador respalda
   la lectura, sin otra llamada a Google. No se declara garantizada la voz natural.

También se añadieron referencias de calendario para «mes pasado» y reglas de
conversación que distinguen una consulta de viabilidad de una orden de preparar,
y explican la ausencia actual de categorías de compras sin inventarlas.

## Evidencia conservada

| Archivo | Qué acredita |
|---|---|
| `evaluaciones/conversacion-antes.json` | 20 turnos, 29 solicitudes; un `ProviderSafety` en el turno 5. Perfil anterior v6. |
| `evaluaciones/conversacion-despues.json` | 20 turnos respondidos, 38 solicitudes incluyendo audio sintético; 18 respuestas de modelo y 2 confirmaciones del backend, sin recuperación por bloqueo. |
| `evaluaciones/conversacion-voz-validada.json` | Repetición tras endurecer el parser de ASR: una transcripción legible real; la cuota TTS impidió completar toda la generación de clips/salida. Los otros casos continuaron por texto y están marcados con error de audio. No es una corrida de voz íntegramente aprobada. |
| `evaluaciones/conversacion-coloquial.json` | Primera secuencia coloquial: conserva la preparación incorrecta del turno 6 para no ocultar el defecto encontrado. |
| `evaluaciones/conversacion-coloquial-corregida.json` | 20 turnos, 24 solicitudes; 19 respuestas de modelo y una aclaración del backend. El turno 6 no usa herramientas ni crea propuesta. |

En la corrida mixta, tres entradas se generaron con TTS para ensayar ASR. Dos
obtuvieron además audio final; la tercera encontró cuota de síntesis. La primera
transcripción dañada se conserva como evidencia, no se reemplazó retroactivamente.
Estas pruebas no acreditan captura con un micrófono físico, compatibilidad móvil
ni calidad de reconocimiento para cualquier voz, ruido o idioma.

En total se contaron **126 solicitudes reales** entre las cinco corridas de
diagnóstico y comprobación. Las dos secuencias finales de 20 turnos completaron
**40 respuestas: 37 del modelo, dos confirmaciones de propuestas y una aclaración
del backend**, sin necesitar un mensaje genérico de recuperación. Esto describe
esas muestras, no una tasa garantizada de disponibilidad futura.

Las corridas usan una separación mínima de **5 segundos entre solicitudes**
para moderar consumo. Sus latencias incluyen esa pausa. El laboratorio interactivo
mantiene `intervalo=0`; esas latencias no son una medición pura de inferencia ni
una promesa de tiempos para la aplicación.

## Pruebas sin red

- Veinte turnos sucesivos con bloqueos inyectados después de leer el saldo:
  respuesta exacta de respaldo, historial coherente y 40 solicitudes contadas.
- Veinte turnos HTTP alternando modelo y recuperación; bloqueo de ASR independiente.
- Descarte de texto bloqueado, respeto de cuota/credenciales/cancelación,
  aclaración sin invocar proveedor y un solo reintento de transcripción inválida.
- Prueba que ejecuta los manejadores reales de `app.js` con DOM y micrófono
  controlados: borra transcripción vieja, muestra el fallo de ASR y reutiliza el
  mismo Blob al reintentar, con una sola consulta posterior de chat.
- Suite Go con `-race`, `go vet` y 12 pruebas Node del laboratorio.

La generación del contrato no produjo diferencias y `git diff --check HEAD`
pasó. El lint completo mantiene nueve incidencias preexistentes en auth/platform/
receipts; la comprobación contra el parche de esta tarea se revisa por separado.
La comprobación final del parche terminó con **0 incidencias nuevas**.

## Repetir una conversación

El comando escribe preguntas y respuestas ficticias, funciones, errores, consumo,
fuente de respuesta y latencia. No registra claves ni errores crudos del SDK.

```powershell
docker run --rm --env-file .env.local -v "${PWD}:/src" -v vera117-gomod:/go/pkg/mod -v vera117-gocache:/root/.cache/go-build -w /src golang:1.27.0 go run ./cmd/revisar-conversacion-vera -nivel-gratuito-confirmado -max-solicitudes 80 -salida docs/evaluaciones/conversacion-20.json
```

Agregar `-coloquial` para la segunda secuencia, `-probar-voz` para incluir tres
casos sintéticos o `-solo-voz` para ejecutar únicamente esos tres. Se detiene
ante cuota de chat, credenciales o presupuesto agotado. Un fallo TTS se registra
por separado y no convierte una respuesta escrita en un fallo de chat.

El operador debe comprobar el proyecto Free Tier antes de usar el flag; la API
no permite demostrar desde aquí el nivel de facturación. El comando no se
ejecuta en CI. No usar preguntas ni grabaciones de clientes reales en esta prueba.

## Alcance de la mejora

Los filtros y el prompt versionado se comparten con el backend. La continuidad
de respaldo, el contador y los controles nuevos son del laboratorio; no se
modificó el contrato `/v1/vera` ni se añadió ejecución de dinero a la conversación.
Las autorizaciones, catálogo cerrado y confirmación en la app siguen vigentes.

Una respuesta de respaldo no equivale a que el modelo comprendió la pregunta.
Una negativa de alcance también es una respuesta válida; no promete responder
cualquier contenido solicitado. Ninguna corrida finita garantiza disponibilidad
total de una API externa ni elimina límites de cuota, red o filtros del proveedor.

Referencia técnica: [configuración de seguridad de Google](https://ai.google.dev/gemini-api/docs/safety-settings),
[capacidades del modelo usado](https://ai.google.dev/gemini-api/docs/models/gemini-3.5-flash-lite).

# Conversación por texto — SCRUM-129 / IA-A5-03 / HU-BE-IA-02

`POST /v1/vera/mensajes` ya conecta autenticación, consentimiento vigente,
cuotas de Redis, persistencia y el orquestador con su proveedor configurado.
El plazo del procesamiento es 45 segundos dentro del grupo autenticado de 60.

El cuerpo admite únicamente `texto` (1–1000 caracteres Unicode, no vacío ni
compuesto sólo por espacios) y `conversacion_id` opcional. Se rechazan campos
desconocidos, repetidos, roles, historial, UTF-8 inválido y caracteres nulos con
`422 ValidationError`; el límite de transporte sigue siendo 1 MiB (`413`).
Los UUID inválidos del cuerpo también producen 422.

Sin conversación se crea una nueva. Con ID se verifica propietario (`ParteID`),
contexto financiero (`UsuarioID`) y estado activo; una referencia ajena,
inexistente o de otro contexto devuelve el mismo `404 NotFound`. Ninguno de
estos IDs procede del cuerpo. El historial se lee del servidor: últimos 20 turnos
de usuario con sus intercambios completos de funciones.

## Respuesta

Devuelve `200` con el envelope habitual y los campos `conversacion_id`,
`mensaje_id` (respuesta final del asistente), `texto`, `creado_en` y, si existe,
`accion_pendiente`. Las fechas llevan UTC−5. El monto de la tarjeta es una cadena
de **centavos**, por ejemplo `"15000000"` para $150.000 COP. La tarjeta se relee
de la acción persistida; el texto del modelo no decide monto, destinatario, estado
ni necesidad de código. Las llaves se muestran como `***`, sin revelar el destino
ni el origen financiero internos. Preparar no ejecuta una transferencia.

## Reintentos

Enviar un UUID nuevo en `Idempotency-Key` por envío y reutilizarlo junto con el
mismo cuerpo original para reintentar. La clave se vincula al propietario, contexto
financiero, conversación solicitada y texto normalizado. Un cuerpo diferente con
la misma clave produce `409 IdempotencyConflict`.

Una clave ya resuelta recupera el mismo mensaje persistido sin llamar de nuevo a
la IA; el estado de su tarjeta se consulta de nuevo. Una clave pendiente reutiliza
el mensaje de usuario. Esto permite recuperar incluso el primer envío cuando se
pierde la respuesta antes de conocer la conversación. No hay deduplicación de un
primer envío sin clave ni ID de conversación.

Si se conoce la conversación, repetir el último texto sin respuesta también
reutiliza el mensaje pendiente. Repetir un texto ya respondido sin clave representa
un nuevo turno. No se debe reanudar una entrada antigua después de enviar turnos
posteriores: la memoria sólo procesa el último turno de usuario.

La admisión toma un candado corto en PostgreSQL y reserva una ejecución por
conversación. No mantiene una conexión ni transacción durante la llamada al
proveedor. Otra solicitud simultánea devuelve `409 ConversationBusy` y
`Retry-After: 1`. La reserva vence y se libera al terminar o fallar; los intercambios
de herramientas ya persistidos se reanudan sin repetir preparaciones.

## Fallos

Un proveedor caído devuelve `503 AssistantUnavailable`, y el vencimiento produce
`504 Timeout`. El mensaje de usuario permanece sin respuesta; no se persiste una
respuesta final falsa. Después de guardar la entrada, también se devuelven
`X-Conversacion-Id` y `X-Mensaje-Usuario-Id` para recuperarla. Ambas cabeceras y
`Retry-After` son visibles mediante CORS. No se exponen causas privadas de SQL ni
del proveedor. Consentimiento y cuotas se comprueban también al reintentar.

## Esquema y validación

La migración `0006_entrada_texto_idempotente.sql` vincula la conversación al
contexto financiero, añade `mensaje.accion_id` con FK a la misma conversación y
crea el registro inmutable `solicitud_texto`. Las conversaciones anteriores se
inicializan con su propietario como contexto financiero; si éste difiere del
contexto autenticado, debe iniciarse una nueva conversación. El rol del módulo
no puede editar ni borrar las claves ni los mensajes.

Pruebas HTTP con dobles: validación cerrada, Unicode, plazo, errores sin datos
privados y tarjeta en centavos. Integración con PostgreSQL, Redis y proveedor
simulado: consentimiento, fallo y recuperación, replay, conflicto de clave,
aislamiento, historial de 20 turnos, concurrencia, liberación por timeout y
preparación reanudada con una sola acción. Incluye `-race`, migraciones reversibles
y las regresiones del asistente. No requiere credenciales del proveedor real.

# Confirmación de acciones — SCRUM-137 / IA-A6-02 / HU-BE-IA-04

Las rutas autenticadas del asistente ya permiten autorizar o cancelar propuestas:

| Ruta POST | Entrada | Resultado |
| --- | --- | --- |
| `/v1/vera/acciones/{id}/codigo` | Sin cuerpo o `{}` | Desafío OTP, propósito y vigencia |
| `/v1/vera/acciones/{id}/confirmar` | `{desafio_id?, codigo?, sin_verificacion_local?}` | Acción y transferencia |
| `/v1/vera/acciones/{id}/cancelar` | Sin cuerpo o `{}` | Acción cancelada |

Las tres devuelven el envelope habitual y 200 al completar. Los cuerpos son cerrados:
no aceptan monto, destino, identidad ni propósito del código. Los DTO y el contrato
proceden de OpenAPI. El monto de tarjeta y transferencia se expresa como cadena de
centavos, igual que en el núcleo. El resultado de una confirmación repetida se relee
del núcleo, sin generar otra transferencia ni exigir de nuevo el OTP.

## Autorización

Se verifica titularidad por `ParteID` y origen financiero por `UsuarioID` del contexto
autenticado. Una acción ajena, inexistente o de otro origen devuelve el mismo 404.
Las rutas están en `RutasDeDinero`; el middleware también reconoce sus segmentos
`{id}` y falla cerrado si no puede comprobar la revocación, incluso con ID escapado.

Se exige código si la propuesta lo pedía, si supera `VERA_UMBRAL_CODIGO` vigente
(por defecto 500.000 COP) o si la aplicación declara `sin_verificacion_local: true`.
Esta última condición eleva permanentemente la exigencia de esa propuesta; omitirla
después no permite eludir el código. La aplicación realiza la verificación local
del dispositivo antes del toque explícito de confirmación; el backend no recibe
ni valida una huella biométrica.

Auth resuelve el celular verificado del actor desde la familia de sesión. No usa el
celular del destinatario ni uno proporcionado por el cliente. El desafío queda ligado
al actor, acción y origen financiero mediante `confirmacion_transferencia`. No sirve
un código de otra acción ni de otro propósito. La migración auth `0014` amplía el
CHECK de propósitos; su reversión conserva el historial y restringe los nuevos registros.

El código dura cinco minutos, admite tres intentos y conserva el racionamiento
compartido de envíos. Reenviar exige 60 segundos y no restablece intentos. Tras una
entrega fallida sin intentos se puede reintentar el mismo desafío respetando el
enfriamiento y el máximo de reenvíos. Un desafío agotado o verificado no se reactiva.
El desafío se guarda y vincula a la acción **antes** de entregar el mensaje por el canal.

## Ejecución atómica

El núcleo resuelve el destino y KYC antes de abrir la transacción de confirmación.
Luego se bloquean conversación y acción, en ese orden, y se releen estado y
consentimiento. Auth verifica el código en el mismo tx mediante un savepoint.
El núcleo reutiliza sus límites, saldo, ledger, reserva y outbox, con la clave de
idempotencia igual al UUID de la acción. Se ejecutan exactamente los datos persistidos.

Cada módulo conserva su rol PostgreSQL; ninguno recibe el repositorio interno de
otro. El núcleo valida sus restricciones diferidas bajo `rol_nucleo` antes de devolver
el control. Sólo el commit exterior confirma el uso del OTP, los asientos, la
transferencia y la resolución de la acción. Esto funciona con un pool de una conexión.

Un fallo técnico revierte todo y permite reintentar sin haber consumido el código.
Los errores de código sí confirman el intento fallido, sin ejecutar dinero. Un rechazo
definitivo del núcleo revierte los cambios parciales financieros y guarda la acción
como `fallida`, con el código público del rechazo. No convierte una caída del directorio
o de la base en un rechazo definitivo. Si la vigencia termina durante la ejecución,
la transferencia se revierte antes de marcar la propuesta como vencida.

Para Bre-B, confirmar la propuesta deja la transferencia **reservada** y su despacho
en outbox; la resolución final pertenece al riel. Las respuestas y el contexto del
asistente distinguen el estado de la acción del estado de la transferencia.

## Estados y contexto

- Confirmada: devuelve la misma transferencia en reintentos.
- Cancelada o fallida: confirmar devuelve `409 ActionNotPending`.
- Vencida: devuelve `410 ActionExpired` y no ejecuta dinero.
- Sin código requerido válido: `403 SecondFactorRequired`.
- Código incorrecto, vencido o agotado: errores OTP de auth.
- Consentimiento del asistente revocado: confirmar cancela la propuesta pendiente
  y devuelve `403 ConsentRequired`. Cancelar sigue disponible sin ese consentimiento.

Las resoluciones añaden un mensaje del servidor al historial, sin OTP ni HMAC, para
que el siguiente turno conozca el resultado. River ejecuta el barrido al iniciar y
cada minuto, en lotes de hasta 100 conversaciones con `SKIP LOCKED`. Respeta el mismo
orden de candados y no añade mensajes a conversaciones archivadas. La API comprueba
la vigencia en cada operación, sin depender de que el barrido ya haya corrido.

## Validación

Pruebas HTTP: contrato cerrado, montos como strings, errores saneados y fallo cerrado
por revocación en rutas dinámicas. Integración con PostgreSQL, contador Redis y canal
OTP simulado: con/sin código, ocho confirmaciones simultáneas, cancelación, expiración,
worker, revocación, acción ajena, origen diferente, Bre-B, cambio de saldo, tope,
reenvíos, entrega fallida, código vencido o de otra acción, intentos agotados y un
fallo diferido de commit que revierte ledger, OTP y acción. Incluye `-race` y `go vet`.
Las integraciones de paquetes que cambian el catálogo global de consentimientos
se ejecutan con `-p 1` para evitar interferencia entre escenarios.

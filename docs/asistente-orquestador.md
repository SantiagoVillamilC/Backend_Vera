# Orquestador — SCRUM-124 / IA-A2-01

`Orquestador.Responder` recibe referencias de conversación y mensaje creadas por el
servidor. La memoria comprueba titularidad con `ParteID`, estado activo, último mensaje
de usuario y ausencia de respuesta final. El historial procede de PostgreSQL, nunca de
roles ni instrucciones suministrados por el cliente o por argumentos de funciones.

## Ciclo

1. Verifica consentimiento y adquiere una exclusión temporal de la conversación.
2. Carga el contexto existente de los últimos 20 turnos de usuario.
3. Construye las instrucciones versionadas con fecha y hora de Colombia y envía el
   historial y las siete declaraciones del catálogo al proveedor protegido por cuotas.
4. Si llegan funciones, las ejecuta únicamente mediante `Catalogo.Ejecutar`, conserva
   sus IDs y firmas opacas y añade los resultados con rol `herramienta`.
5. Repite hasta obtener texto final o alcanzar un tope. Guarda cada intercambio completo
   antes de la siguiente vuelta. La respuesta pública interna del servicio contiene sólo
   texto, versión, contadores y, si aplica, una acción pendiente releída del servidor.

Las lecturas contiguas de una vuelta se ejecutan concurrentemente con el mismo contexto.
Los resultados conservan el orden de las llamadas, aunque las funciones terminen en otro
orden. Las preparaciones se ejecutan secuencialmente y separan los grupos de lectura.
No se permite ejecutar transferencias ni confirmar acciones mediante herramientas.
`ConversacionDelContexto` permite a SCRUM-128 y SCRUM-131 vincular sus referencias y
preparaciones a la conversación que autorizó el servidor.

Los errores de negocio son resultados estructurados del catálogo. Cancelación, errores
del proveedor, falta de consentimiento y fallos de almacenamiento detienen el ciclo.
Las respuestas de protocolo inválidas o con IDs no vacíos repetidos se rechazan antes
de ejecutar sus funciones. Los IDs vacíos admitidos por el proveedor se correlacionan
por nombre y posición, manteniendo el protocolo original.

## Topes y cancelación

- Máximo cinco llamadas al puerto del modelo por mensaje y ocho ejecuciones de funciones.
- Un lote que excedería las ocho funciones se rechaza completo; no se ejecuta parcialmente.
- Si la quinta vuelta todavía pide funciones, no se ejecutan: se guarda una respuesta
  que invita a reformular, y se registra el evento sin mensajes ni argumentos.
- Plazo total de 45 segundos, o el plazo menor del llamador. Las funciones y el proveedor
  deben respetar el contexto. La limpieza dispone de hasta un segundo independiente.

Al reanudar un mensaje interrumpido se reutilizan los intercambios completos guardados.
No se vuelven a ejecutar esas funciones ni se reinician los topes de vueltas y funciones.
Los contadores de tokens de `SalidaOrquestador` suman las respuestas conocidas de la
invocación actual; el consumo anterior queda registrado en sus mensajes y en el presupuesto
de Redis. Los errores del proveedor sin metadata se rigen por la política de reservas.

## Instrucciones y evaluación

El texto está en `internal/copilot/instrucciones.go`, versión `vera-2026-10-18-v1`.
Incluye tono, alcance, prohibiciones, uso de datos reales, texto plano, protección de
instrucciones y confirmación explícita en la aplicación. La fecha dinámica no cambia
la versión del texto estático. Cualquier edición del texto exige cambiar la versión.

Los mensajes y resultados jamás se concatenan a las instrucciones del sistema. Las
pruebas deterministas verifican esa separación y los controles del backend. Verificar
el comportamiento del modelo real —cifras fundamentadas, negativas fuera de alcance,
ausencia de markdown y resistencia a inyección— corresponde a las evaluaciones de
SCRUM-141/SCRUM-139 y no se da por demostrado por el prompt.

## Persistencia y exclusión

La migración asistente 0003 añade `ciclo_token` y `ciclo_expira_en` a la conversación.
Una actualización condicional adquiere el turno por hasta 45 segundos. Cada escritura
comprueba el token y su vigencia con bloqueo de fila dentro de una transacción corta.
No se conserva una transacción ni una conexión del pool durante llamadas a IA o funciones.
Un proceso atrasado no puede guardar ni liberar la exclusión de otro proceso. Si el
proceso desaparece, la exclusión vence por sí sola; una nueva entrada del usuario también
se bloquea mientras el ciclo está activo. Las referencias no procesables devuelven
`ErrCicloNoDisponible` al futuro handler, sin distinguir referencias ajenas o inexistentes.

Cada turno del modelo se guarda con modelo, versión, tokens, latencia y protocolo.
Los turnos con llamadas y sus resultados se anexan en la misma transacción, marcados
`metadatos.interno=true`. No se guarda audio. `MensajesPublicosConversacion` excluye
esas filas; los futuros handlers deben proyectar únicamente los campos públicos,
sin devolver `metadatos`. La respuesta final se enlaza a `respuesta_a`.

Si hubo una preparación exitosa, sólo se toma del resultado su referencia de acción.
El monto, destinatario, vigencia y exigencia de código se releen en PostgreSQL y se
comprueban contra la parte, conversación y estado pendiente. El texto del modelo no
determina esos datos. La preparación real y su idempotencia pertenecen a SCRUM-131;
ante una interrupción después de crear la propuesta y antes de guardar el intercambio,
ese manejador deberá recuperar la propuesta pendiente existente.

## Arranque y alcance

`cmd/api` construye el orquestador con el catálogo, memoria PostgreSQL, consentimiento y
proveedor decorado con presupuesto, e inyecta el servicio en `DependenciasVera`.
No cambia aún el contrato HTTP: conectar mensajes, idempotencia HTTP y proyección de
`accion_pendiente` corresponde a SCRUM-129; voz a SCRUM-133. Los endpoints siguen como
stubs tras sus controles hasta esas historias. Los manejadores de herramientas todavía
no implementados siguen devolviendo `FunctionUnavailable`.

## Validación

Las pruebas incluyen proveedor simulado real, respuesta directa, funciones, error de
negocio, lecturas concurrentes, acción estructurada, recuperación, topes y cancelación.
Las pruebas `integration` con `DATABASE_URL` y `APP_DATABASE_URL` usan PostgreSQL real:
comprueban aislamiento, protocolo, firmas, versión, historial público, token de exclusión
y su caducidad. Un pool limitado a una conexión verifica que el proveedor se ejecuta
fuera de transacciones y permite consultas normales del backend durante la generación.

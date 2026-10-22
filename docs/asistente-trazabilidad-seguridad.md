# SCRUM-138 · IA-A8-02 · Trazabilidad y defensa contra inyección

Completa la parte de seguridad y trazabilidad de HU-BE-IA-13. Las cuotas de
mensajes, canales y tokens de SCRUM-136 siguen en `ControlUso`; no se modifican.
Los códigos `AssistantUnavailable`, `AudioNotUnderstood`, `ActionExpired` y
`ActionNotPending` ya estaban en el catálogo OpenAPI.

## Auditoría de llamadas

La migración 0005 crea `asistente.llamada_funcion`. Cada registro contiene un UUID
de llamada generado por el servidor, fase, propietario, conversación, mensaje de
usuario que originó el ciclo, función conocida, argumentos saneados, resultado,
duración en milisegundos, `request_id`, versión de instrucciones y fecha de base
de datos. Nunca guarda el resultado de la herramienta, texto conversacional,
transcripción, audio ni identificadores de llamada suministrados por el proveedor.

Hay dos eventos inmutables por llamada ejecutada: `inicio` con `InProgress` antes
del manejador, y `fin` con `OK`, un código estable o `Canceled`. Se enlazan por
`llamada_id`. Rechazos previos al manejador (argumentos inválidos, función desconocida
o aún no disponible) sólo generan el evento `fin` porque no hubo ejecución.
Funciones desconocidas se registran como `desconocida`, sin su nombre arbitrario.

`cmd/api` conecta siempre `ConAuditoriaLlamadas(NuevaAuditoriaPostgres(pool))`.
El catálogo permite omitir esa opción para sus pruebas aisladas. El orquestador
aporta el mensaje desde la entrada que la memoria ya autorizó; ninguna función
acepta usuario, conversación ni mensaje como argumento del modelo.

La consulta sqlc sólo inserta si mensaje y conversación pertenecen a la parte
autenticada y el mensaje tiene rol `usuario`. Una llamada inicial exige además
conversación activa. Las claves foráneas mantienen la relación entre esos datos.

Si el registro inicial falla, el manejador no se ejecuta. Si falla el cierre, el
catálogo no entrega el resultado y el ciclo termina con el error interno estable
`ErrNoDisponibleIA`. El registro inicial permite detectar una llamada incompleta.
Las fases no forman una transacción distribuida con las herramientas: si una
preparación ya se guardó y falla el cierre, puede quedar una acción pendiente y
una traza inicial sin final; nunca significa que se haya ejecutado dinero.

El cierre usa un contexto de hasta dos segundos que conserva identidad y
correlación aunque se cancele la solicitud. Un pánico del manejador se convierte
en `InternalError`, se audita y no revela el valor del pánico ni una traza.
El límite del orquestador también contiene pánicos del adaptador de IA, para que
no terminen en el log genérico de recuperación HTTP con contenido privado.

## Saneamiento y permisos

Se construye una proyección explícita de los argumentos ya validados:

| Argumento | Auditoría |
| --- | --- |
| `celular` | Sólo últimos tres dígitos, como `*******567` |
| `nombre` | Hasta dos iniciales, como `L*** R***` |
| `llave_breb`, `destinatario_ref`, `transferencia_id`, `monto_pesos` | `[omitido]` |
| Fechas, tipo, agrupación y límite | Valores del contrato ya validados |
| Entrada inválida o función desconocida | Sólo `{"rechazados":true}` |

La tabla admite objetos JSON de hasta 2048 bytes. `rol_asistente` sólo tiene
INSERT en las columnas de entrada, sin SELECT, UPDATE, DELETE ni TRUNCATE. `rol_api`
no obtiene acceso directo. Los operadores de base de datos son quienes pueden
consultar las trazas. Ninguna herramienta del modelo puede consultar auditoría.

El borrado y la retención futuros de SCRUM-135 deberán coordinar la conservación
de estas trazas con las referencias a mensajes/conversaciones. No se agrega un
CASCADE que permita al rol de la aplicación borrar auditoría indirectamente.

## Logs y metadatos de mensajes

La línea de slog de cada llamada usa nombre permitido, código, duración,
presencia de argumentos, `llamada_id` y versión. El handler existente agrega
`request_id`. No se imprimen valores de argumentos, datos financieros devueltos,
referencias opacas, IDs del proveedor, errores privados, contenido ni audio.

Cada turno del modelo conserva en `asistente.mensaje` el modelo, versión,
latencia y tokens, como ya implementaba el orquestador. La prueba verifica esos
metadatos junto con las trazas. La auditoría saneada es independiente del protocolo
interno de function calling guardado para reanudar ciclos; ese protocolo no se
expone al historial público ni a los logs.

## Límites de confianza y defensas

- El sistema se construye únicamente con instrucciones versionadas del servidor
  y su reloj. Mensajes, nombres, entidades y resultados no se concatenan al sistema.
- El orquestador verifica los roles del historial antes de cualquier adaptador:
  sólo `usuario`, `asistente` y `herramienta`, sin llamadas fabricadas desde el rol
  de usuario ni resultados disfrazados de texto de sistema.
- Gemini recibe texto del usuario como `user` y resultados como `FunctionResponse`;
  sus instrucciones van separadas en `SystemInstruction`.
- Los esquemas rechazan cualquier identidad, conversación o campo adicional.
  El catálogo sigue cerrado: no hay función para confirmar ni ejecutar dinero.
- Los nombres salen enmascarados. Las instrucciones v4 tratan también las entidades
  como datos no confiables y rechazan órdenes que simulen roles privilegiados.
- Antes de persistir o entregar texto del modelo, se bloquean copias literales de
  párrafos privados de al menos 80 bytes, ignorando mayúsculas y diferencias de
  espacios. El bloqueo devuelve `ErrBloqueoIA` y sólo registra `InstructionDisclosure`.

El filtro de salida detecta copias, no paráfrasis, traducciones o texto codificado.
Estas defensas no eliminan la inyección de instrucciones. La garantía determinista
es que la IA no gana herramientas de ejecución ni acceso a otra identidad.
La evaluación del comportamiento con el proveedor real corresponde a SCRUM-139;
los casos deterministas generales continúan en SCRUM-141.

## Validación

Las pruebas cubren ejecución sin auditoría disponible, cierre fallido sin entregar
datos, cancelación, pánicos saneados, rechazos, correlación y minimización. Con
PostgreSQL real verifican los permisos, contexto ausente/ajeno, llamadas paralelas
del mismo mensaje y los metadatos del modelo. Capturan slog para comprobar ausencia
de mensajes, celulares completos, claves, nombres y resultados financieros.

Los casos de inyección usan un proveedor simulado y prueban las fronteras del
backend: solicitud de ejecución desconocida, identidad ajena, roles elevados,
nombres con órdenes, aislamiento del SDK y bloqueo de copias del prompt.

```sh
go test -race ./...
go vet ./...
go test -race -tags=integration ./internal/copilot ./cmd/migrate
```

La integración requiere PostgreSQL y Redis desechables mediante `DATABASE_URL`,
`APP_DATABASE_URL` y `REDIS_URL`. La migración se aplica, revierte y reaplica en
una base propia de prueba sin afectar las acciones existentes.

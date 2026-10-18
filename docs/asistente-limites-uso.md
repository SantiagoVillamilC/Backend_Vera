# Límites de uso — SCRUM-136 / IA-A8-01

El control se aplica después de autenticación y consentimiento y antes del procesamiento
de los mensajes de texto y voz. Comparte el cliente Redis y `auth.Contadores` existentes;
las claves usan HMAC de `ParteID` mediante `auth.Claves`. No se guardan UUID en claro,
mensajes, audio ni resultados del asistente en estos contadores.

## Configuración inicial

| Variable | Valor inicial | Alcance |
| --- | ---: | --- |
| `VERA_LIMITE_TEXTO_HORA` | 30 | Intentos de texto por hora y parte |
| `VERA_LIMITE_TEXTO_DIA` | 150 | Intentos de texto por día y parte |
| `VERA_LIMITE_VOZ_HORA` | 10 | Intentos de voz por hora y parte |
| `VERA_TOKENS_DIARIOS` | 100000 | Consumo más reservas de texto y voz, por parte |
| `VERA_TOKENS_RESERVA_LLAMADA` | 4096 | Reserva previa a cada llamada al puerto de IA |

Los límites de mensajes son los ejemplos de HU-BE-IA-13. El presupuesto y reserva
de tokens son valores iniciales de operación, pendientes de calibración con evaluaciones.
Se validan al arrancar: enteros positivos hasta 10^12 y reserva no superior al presupuesto.
No se puede desactivar el control configurando cero. Cambiar valores requiere reiniciar.

Las ventanas son horas de reloj y días civiles en Colombia (UTC-05:00). No son ventanas
deslizantes. Se separan por canal y tienen TTL hasta su cierre; un rechazo horario también
cuenta en el contador diario de texto. Un rechazo por presupuesto ocurre antes de los
contadores de mensajes. Texto y voz comparten exclusivamente el presupuesto de tokens.

## Respuestas

- Cuota agotada: `429 TooManyRequests`, envelope habitual y `Retry-After` en segundos,
  redondeado hacia arriba. Cuando varias cuotas bloquean, se informa la espera mayor.
  Para tokens se espera hasta medianoche de Colombia, salvo que una reserva concurrente
  se concilie antes y libere saldo.
- Redis no disponible o estado inválido: `503 AssistantUnavailable`, sin invocar IA.
  No existe fallback a memoria en producción.
- Sin consentimiento: `403 ConsentRequired` antes de consumir cuotas.

El límite general por IP del router permanece vigente y puede rechazar antes de llegar
a estas cuotas. Su respuesta se rige por el middleware existente.

## Presupuesto de tokens

`ControlUso.ProtegerProveedor` reserva atómicamente antes de cada `Generar`, incluidas
las futuras vueltas de function calling. El script compara consumo más reservas con el
presupuesto diario; dos réplicas no pueden reservar el mismo saldo. Hace falta saldo
para una reserva completa, aunque queden algunos tokens sin utilizar.

Con consumo positivo conocido se concilia a tokens de entrada más salida. Ante error se
conserva al menos la reserva; si faltan metadatos de consumo se conserva completa.
Esto también evita uso ilimitado del proveedor simulado, que no reporta tokens por defecto.
La conciliación usa un contexto corto independiente de la desconexión HTTP y nunca revive
el día anterior. Si falla, se responde 503 y la reserva permanece.

La reserva es una estimación configurable, **no un límite de generación impuesto al modelo**.
Si el consumo real supera la reserva, se registra íntegro y bloquea nuevas llamadas cuando
se agota el presupuesto; las llamadas ya iniciadas pueden superar el umbral. La metadata
del proveedor tampoco garantiza contabilizar intentos fallidos o reintentos internos.
Este mecanismo protege el acceso; no representa una garantía exacta de facturación.
El límite monetario estricto requiere además cuotas del proveedor y estimar o contar el
contexto antes de cada intento físico. No se descartan tokens reportados para ocultar excesos.

## Integración y pruebas

El arranque compone `consentimiento(presupuesto(proveedor))`; la identidad procede del
contexto autenticado. `Admitir` cuenta una entrada y `Generar` cuenta cada llamada, evitando
cobrar un mensaje extra por cada vuelta del orquestador. Confirmar o cancelar acciones
no consume cuotas de mensajes. No se agregan tablas ni migraciones para estas cuotas.

Los endpoints siguen respondiendo 501 al superar los controles hasta implementar las HUs
de conversación y orquestación. La defensa contra inyección y trazabilidad de funciones
pertenecen a SCRUM-138 y no se consideran terminadas con esta tarea.

Pruebas unitarias: canales, identidad, ventanas, medianoche de Colombia, consumo y errores.
Pruebas HTTP: orden de controles, 429 con Retry-After, 503 sin detalles internos.
Pruebas `integration` con `REDIS_URL`: reservas concurrentes, TTL, conciliación,
conservación del día expirado y proveedor bloqueado al agotarse el presupuesto o caer Redis.

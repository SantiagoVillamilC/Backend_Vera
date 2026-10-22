# Conversación por voz — SCRUM-133 / IA-A5-04 / HU-BE-IA-03

`POST /v1/vera/mensajes/voz` recibe `{audio, formato, conversacion_id?}`.
Devuelve `200` con la misma respuesta del chat por texto y `transcripcion`.
`Idempotency-Key` es opcional y tiene las mismas reglas de reintento: UUID por
envío y reutilización sólo con el cuerpo original.

## Flujo

1. Exige autenticación y consentimiento vigente antes de procesar el clip.
2. Valida el cuerpo cerrado, base64, contenedor, tamaño y duración.
3. Consume la cuota del canal **voz**, sin consumir la cuota de mensajes de texto.
4. Comprueba la conversación y su contexto financiero antes de enviar audio al
   proveedor; una referencia ajena o inexistente devuelve el mismo 404.
5. Solicita una transcripción literal sin historial previo ni herramientas. Gemini
   recibe el clip como contenido inline y un esquema JSON cerrado con la propiedad
   `transcripcion`; cualquier llamada a funciones en esta etapa se rechaza.
6. Guarda la transcripción como mensaje de usuario con `canal=voz` y la procesa con
   el mismo orquestador del texto: historial del servidor, catálogo cerrado,
   auditoría, revalidación de consentimiento y tarjetas persistidas.

El plazo total es 45 segundos, compartido por transcripción y orquestación. Cada
llamada al proveedor consume el presupuesto de tokens protegido existente. El audio
no permite ejecutar dinero: las operaciones siguen requiriendo confirmación explícita.

## Audio aceptado

- Base64 estándar canónico, sin URL, prefijo data URI ni saltos de línea.
- Máximo **700 KiB (716.800 bytes)** decodificados y **60 segundos** de presentación.
  El cuerpo HTTP mantiene su tope de 1 MiB; la cadena base64 admite hasta 960.000 caracteres.
- WAV RIFF PCM o float, mono/estéreo, con tamaños de bloques y tasa coherentes.
- M4A AAC no fragmentado, con una pista de audio y un bloque de datos. Se comprueban
  las cajas, los relojes de película y medio y las tablas de muestras. La presentación
  de 60 segundos admite el priming/relleno normal de AAC, sin aceptar 61 segundos.

Los analizadores son Go puro; el servidor no necesita FFmpeg ni crea archivos.
Los MIME enviados al adaptador son `audio/wav` y `audio/m4a`, conforme a la
[documentación de audio de Gemini](https://ai.google.dev/gemini-api/docs/generate-content/audio).

`422 ValidationError` incluye `details` para base64, contenedor, tamaño o duración
(`audio_base64`, `audio_contenedor`, `audio_tamano`, `audio_duracion`). Los campos
faltantes y el formato usan las reglas de validación de los DTO generados.
Una transcripción vacía devuelve `422 AudioNotUnderstood`: no guarda un turno vacío
ni ejecuta funciones. Una salida del proveedor inválida o mayor a 6000 caracteres
falla cerrada. Los fallos de disponibilidad y plazo conservan 503/504 del chat.

## Privacidad y reintentos

El clip se procesa exclusivamente en memoria y se limpia su búfer al salir del
handler, incluso ante un error. No se envía a almacenamiento, archivos temporales
ni logs. Las filas de mensajes contienen texto y metadatos de protocolo, sin bytes
de audio. Una huella SHA-256 del clip permite reconocer reintentos; no permite
recuperar el audio. No se necesitan migraciones nuevas.

Si ya se guardó la entrada, reintentar la misma clave recupera su transcripción y
respuesta sin volver a transcribir. Sin clave, repetir el mismo clip con una
conversación conocida reutiliza el último turno de voz pendiente. Las claves también
evitan colisiones entre voz y texto. Un primer envío sin clave cuyo ID se perdió no
puede deduplicarse. Dos solicitudes iniciales simultáneas pueden transcribir en
paralelo; la admisión persistente del ciclo común impide ejecutar el mismo turno dos
veces. Los intercambios de funciones ya guardados se reanudan sin otra preparación.

Si la transcripción falla, aún no hay turno que conservar. Si falla el ciclo después
de guardarla, queda pendiente y se devuelven las cabeceras `X-Conversacion-Id` y
`X-Mensaje-Usuario-Id` para recuperarlo. Los reintentos vuelven a comprobar las cuotas
y el consentimiento. El proveedor simulado requiere un guion de transcripción;
sin guion devuelve una transcripción vacía en lugar de inventar reconocimiento.

## Validación

Incluye clips sintéticos reproducibles, pruebas del contrato Gemini contra HTTP
local, cuerpo y errores HTTP, silencio, límite exacto de 60 segundos, rechazo de 61,
fuzzing de los analizadores y pruebas con PostgreSQL/Redis reales y proveedor
simulado. Se comprueban canal, ausencia de audio persistido, cuota de voz, aislamiento,
recuperación y una sola acción preparada tras reintentar. La precisión de reconocimiento
contra el proveedor real se evalúa en IA-A9-02; estas pruebas no usan su API ni credenciales.

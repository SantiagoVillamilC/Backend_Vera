# SCRUM-117: conversaciones y mensajes

Base de HU-BE-IA-02; todavía no implementa el endpoint del chat ni llama al proveedor.

## Relación con el DBML

La referencia entregada fue `BD/vera_modelo_datos.dbml`, fuera del repositorio.
El DBML describe un modelo más amplio que el backend implementado. Se conserva su
estructura de conversación y mensaje, con estas adaptaciones explícitas:

| DBML | Implementación y motivo |
| --- | --- |
| `ia` | `asistente`, nombre establecido en la HU |
| `identidad.usuario` | `auth.partes`, identidad existente; no se crea otro directorio |
| `usuario_id` | Parte efectiva del contexto autenticado (`Identidad.UsuarioID`); nunca un campo del cliente |
| `created_on` / fecha de inicio | `creada_en`, un único instante de creación |
| fecha del último mensaje | `ultimo_mensaje_en`, inicializada al crear para retención y orden estable |
| `created_by`, `updated_by`, `is_active` | Sin duplicarlos: propietario y estado explícitos; el historial tendrá borrado real según HU-BE-IA-05 |
| roles usuario/asistente/herramienta | Se conservan; la futura API traduce asistente a `vera` y oculta herramienta y metadatos |
| mensaje de solo anexado | Sin permisos UPDATE, DELETE ni TRUNCATE para el rol del módulo |

Se añaden `canal` (texto/voz), versión de instrucciones, metadatos estructurados
para reconstruir llamadas a funciones y `respuesta_a`. Este último enlaza una
respuesta con un mensaje de la misma conversación: permite consultar mensajes
sin respuesta sin editar el mensaje original. El servicio de SCRUM-129 deberá
guardar la respuesta con ese enlace, validar que apunta a un turno de usuario y
resolver reintentos. No se guarda audio.

Las tablas de herramientas, memoria vectorial y hallazgos del DBML no pertenecen
a SCRUM-117. El catálogo cerrado y la separación entre preparar y confirmar se
implementarán según la HU; no se incorpora la herramienta `enviar_dinero` del DBML.

## Migraciones y acceso

`cmd/migrate up` aplica núcleo, River, auth y después `migraciones-asistente`, con
tabla de versiones independiente `goose_db_version_asistente`. El orden de `down`
es inverso; solo se usa en bases locales. `status` incluye el asistente.

`rol_api` puede cambiar a `rol_asistente`, pero no hereda sus privilegios. Cada
operación del módulo pasa por `EnTransaccionAsistente`, con `SET LOCAL ROLE` y
`lock_timeout`. El rol no puede consultar tablas de auth ni del núcleo; la FK no
requiere otorgarle acceso de lectura. Las consultas de conversaciones existentes
filtran por propietario. Este filtro es aislamiento en consultas, no una política RLS.

`AnexarMensaje` incrementa el contador bajo bloqueo de la conversación e inserta
el mensaje en una sola sentencia. Un fallo revierte ambos cambios y los anexos
concurrentes no repiten orden. Varias escrituras de un turno pueden agruparse en
la misma transacción. Nunca debe mantenerse esa transacción durante una llamada
al proveedor. La serialización de turnos completos y los reintentos del chat
corresponden a SCRUM-129.

El contexto recupera los últimos 20 turnos de usuario y todos sus mensajes de
asistente/herramienta, ordenados por `numero_orden`; no corta una secuencia de
funciones en mitad de un turno. Las cuotas de tamaño y funciones del orquestador
se implementarán en sus tareas respectivas.

Los índices cubren orden por propietario, retención y mensajes por conversación.
El borrado del historial y la cancelación de acciones se habilitarán juntos en
SCRUM-135. La FK de mensajes ya contempla el borrado en cascada; no se otorga
todavía DELETE al rol de aplicación.

## Verificación reproducible

```sh
sqlc generate
go run ./cmd/migrate up
go test -race -tags=integration -count=1 ./internal/copilot/...
go test -race -tags=integration -count=1 ./cmd/migrate
go vet ./...
go test -race ./...
```

Las pruebas requieren una base PostgreSQL desechable, ya migrada, con
`DATABASE_URL` del dueño y `APP_DATABASE_URL` de `rol_api`. Crean partes y
conversaciones propias y las limpian al terminar. No se omiten si faltan las
variables. Cubren aislamiento, contexto, mensajes sin respuesta, restricciones,
rollback, permisos y anexos concurrentes.

La prueba de `cmd/migrate` crea y elimina su propia base, por lo que el usuario de
`DATABASE_URL` necesita permiso CREATEDB. Comprueba Up repetido, status, Down y Up
sin revertir el esquema de la base compartida por otras pruebas.

Verificado durante la implementación: PostgreSQL 17, Go 1.27.0 y sqlc 1.31.1;
pruebas de integración con `-race`, suite general con `-race`, `go vet ./...`
y regeneración de OpenAPI sin cambios. El linter 2.13.1, incluyendo el tag
`integration`, no reportó hallazgos en los archivos nuevos; el paquete conserva
hallazgos previos de formato y comentarios en el esqueleto de copilot que se
retirará en SCRUM-156. Por eso el comando de lint del paquete aún no está verde.

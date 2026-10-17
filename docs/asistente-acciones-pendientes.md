# SCRUM-125 · IA-A0-02: tabla accion_pendiente y máquina de estados

Base de HU-BE-IA-04 y HU-BE-IA-12. Esta unidad implementa persistencia,
restricciones y consultas; no expone endpoints ni mueve o reserva dinero.
El DBML entregado no contenía una tabla de acciones pendientes: se añade según
la HU, dentro del esquema `asistente` establecido en SCRUM-117.

## Datos y estados

`asistente.accion_pendiente` guarda propietario y conversación, tipo de
transferencia, destino resuelto, nombre mostrado enmascarado, celular enmascarado,
entidad y tipo de llave cuando aplica, monto en centavos `bigint`, COP,
exigencia de código, desafío, estado, transferencia, código de error y fechas.
El servicio que prepare la acción deberá proporcionar el nombre y celular ya
enmascarados. El destino interno no debe enviarse al modelo ni registrarse en logs.

La FK compuesta garantiza que la conversación corresponde al mismo propietario.
Una acción nace `pendiente` y vence cinco minutos después de su creación. Solo
puede pasar desde pendiente a uno de estos estados finales:

| Estado final | Requisito |
| --- | --- |
| confirmada | Vigente y transferencia existente asociada; desafío asociado si requiere código |
| cancelada | Pendiente, incluida una pendiente cuyo plazo ya se cumplió |
| vencida | Plazo cumplido |
| fallida | Vigente y código de error no vacío |

Toda resolución exige `resuelta_en`. Los estados finales no admiten cambios ni
reapertura. El trigger protege las transiciones incluso si se intenta actualizar
directamente con el dueño de la tabla. Las columnas de monto, moneda, destino,
propietario, conversación, tipo y vigencia son inmutables y tampoco tienen
permiso UPDATE para `rol_asistente`.

Mientras sigue pendiente y vigente, se puede asociar/reemplazar un desafío y
elevar `requiere_codigo` de falso a verdadero. Nunca se permite rebajarlo.
La FK solo demuestra que el desafío existe: su titular, propósito, vigencia y
verificación se comprobarán a través de auth en SCRUM-137. Lo mismo aplica a
la validación de titularidad y datos de la transferencia por el servicio del núcleo.

Solo puede haber una fila pendiente por conversación, incluso si venció y aún no
se barrió. Una transferencia tampoco puede vincularse a dos acciones. Las filas
no admiten DELETE/TRUNCATE con el rol de aplicación. El borrado de conversaciones
con acciones queda restringido hasta implementar su política en SCRUM-135.

## Consultas y concurrencia

Todas las consultas individuales filtran por usuario. `CrearAccion` verifica
además la propiedad y estado activo de la conversación. La identidad la entrega
el servicio desde el contexto autenticado; nunca el cliente ni el modelo.

`BloquearAccion` permite leer con `FOR UPDATE` dentro de una transacción. Las
transiciones usan comparación de estado (`WHERE estado = 'pendiente'`) y devuelven
la fila actualizada. Cero filas (`pgx.ErrNoRows`) exige releer con `ObtenerAccion`
para distinguir ausencia, expiración o resolución previa. Una confirmada conserva
el ID de transferencia para responder idempotentemente. Estas consultas solo
registran resultados: no autorizan una operación ni ejecutan el núcleo.

`VencerAcciones` procesa un lote limitado con `SKIP LOCKED`, preparado para el
trabajo periódico de SCRUM-137. Esa tarea deberá validar un límite positivo,
registrar el worker y coordinar ejecución, expiración e idempotencia con el núcleo.

SCRUM-131 implementará el reemplazo de propuestas: bloquear la conversación,
resolver la pendiente anterior y crear la nueva dentro de una única transacción.
El índice único ya impide que dos preparaciones creen acciones pendientes simultáneas.
SCRUM-137 añadirá endpoints, consentimiento, OTP y ejecución con el ID de acción
como clave de idempotencia. Ninguna función del modelo deberá llamar a ConfirmarAccion.

## Verificación

```sh
sqlc generate
go run ./cmd/migrate up
go test -race -tags=integration ./internal/copilot/... ./cmd/migrate
go vet ./...
go test -race ./...
```

Usar PostgreSQL de pruebas con `DATABASE_URL` del dueño y `APP_DATABASE_URL` de
`rol_api`. La prueba de migraciones crea otra base desechable y necesita CREATEDB.
Se comprueban exactitud de montos superiores a 2^53, unicidad, aislamiento,
matriz de transiciones, resoluciones simultáneas, expiración, referencias y
permisos. No se hacen llamadas a proveedores de IA ni se ejecutan transferencias.

Validación realizada con PostgreSQL 17, Go 1.27.0 y sqlc 1.31.1: integración,
reversión/reaplicación de la migración, suite general con `-race`, `go vet` y
regeneración de OpenAPI sin diferencias. El linter 2.13.1 no reporta hallazgos en
los archivos nuevos; conserva los hallazgos preexistentes del esqueleto antiguo
de copilot, cuyo retiro corresponde a SCRUM-156.

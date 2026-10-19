# SCRUM-131 · IA-A3-04 · Preparación de transferencias

`preparar_transferencia` valida una intención y guarda una acción pendiente. No
crea transferencias, postings, reservas ni outbox. La confirmación y su segunda
validación corresponden a SCRUM-137; los endpoints de confirmación siguen pendientes.

## Validación y destino

El catálogo exige exactamente un destino (`destinatario_ref`, `celular` o
`llave_breb`) y `monto_pesos` como cadena de pesos enteros positivos sin separadores.
`nucleo.ParsearPesosEnteros` comprueba el rango antes de multiplicar por 100;
el monto nunca pasa por coma flotante. Una cifra ambigua exige aclaración antes
de llamar a la función, según las instrucciones versionadas.

Para un celular se comparte el presupuesto de `/v1/destinatarios` y se aplican las
reglas de credencial activa, celular verificado y enfriamiento de cambio de número.
Una referencia se descifra con el usuario financiero y conversación actuales y
se revalida el perfil por el UUID resuelto; no se reinterpreta una llave telefónica
antigua. Una referencia ajena, alterada o vencida devuelve `ValidationError`.

El destino interno guardado es el UUID resuelto, no el teléfono. La parte que
conversa y el usuario financiero del contexto pueden ser diferentes: la acción
pertenece a `ParteID` y su origen inmutable se guarda como `usuario_financiero_id`
desde `UsuarioID`. El futuro confirmador debe verificar ese origen antes de ejecutar,
además de la titularidad de la acción.

## Prevalidación del núcleo

`PrevalidarTransferencia` comparte la resolución de destino y KYC con `Transferir`.
`PrevalidarTransferenciaPorLlave` usa `ResolverLlave`, como `TransferirPorLlave`.
Ambos reutilizan `verificarLimite` y `verificarSaldoDeReserva`, incluidos el GMF,
el tope por operación y el cupo del mes colombiano. La cuenta origen se deriva
del usuario autenticado. Se comprueba la existencia de las cuentas involucradas.

La consulta financiera corre en una transacción `REPEATABLE READ, READ ONLY`, bajo
`rol_nucleo`, sin bloquear las cuentas. No puede escribir dinero. La resolución de
auth/directorio ocurre fuera de esa transacción y la persistencia posterior corre
bajo `rol_asistente`. La validación es informativa: una propuesta no aparta cupo
ni saldo. La confirmación deberá comprobar nuevamente todas las reglas.

Rechazos conocidos se proyectan con los códigos existentes: `RecipientNotFound`,
`SelfTransferNotAllowed`, `InsufficientFunds`, `OperationLimitExceeded`,
`MonthlyQuotaExceeded`, `DirectoryUnavailable` y `ValidationError`. Los fallos
internos se minimizan a `FunctionUnavailable`.

## Propuesta y concurrencia

Se bloquea la conversación activa y propia antes de resolver la propuesta anterior.
En la misma transacción se cancela la anterior vigente (o se marca vencida si ya
expiró) y se inserta la nueva. El índice único mantiene una sola pendiente incluso
con preparaciones concurrentes. Si falla el INSERT, la cancelación también se revierte.
Una preparación rechazada por validación no cancela la propuesta vigente.

La propuesta guarda destino, monto en centavos, COP, datos enmascarados y expiración
a cinco minutos por reglas de base de datos. No crea desafío OTP. `requiere_codigo`
es verdadero sólo cuando el monto supera `VERA_UMBRAL_CODIGO` (por defecto 500000
pesos; exactamente el umbral no exige código). La futura confirmación puede elevar
esta exigencia si el teléfono no ofrece verificación local, nunca rebajarla.

El resultado contiene el identificador de acción, monto formateado por el backend,
destinatario enmascarado, entidad cuando aplica, exigencia de código, expiración y
`requiere_confirmacion=true`. El orquestador relee la tarjeta desde PostgreSQL;
el modelo no decide su estado ni sus campos. Las instrucciones exigen decir que
la persona debe revisar y confirmar en la tarjeta.

## Cuota y configuración

Redis cuenta hasta diez intentos de preparación por hora, después de validar la
forma de los argumentos. La ventana inicia con el primer intento. Se usa la clave
`vera:preparaciones:<HMAC de ParteID>`, compartida entre réplicas, conversaciones,
canales y contextos financieros de esa persona. La undécima devuelve
`TooManyRequests`. Si Redis no responde se rechaza antes de leer destinos o escribir
acciones. La cuota de búsqueda telefónica se consume además cuando corresponde.

`VERA_UMBRAL_CODIGO` se configura en pesos enteros, se convierte a centavos al
arrancar y rechaza cero, separadores, decimales y desbordamientos.

`VERA_LLAVES_HABILITADAS=false` mantiene deshabilitado Bre-B por defecto. Activarlo
permite resolver con el directorio ya configurado y guardar `transferencia_llave`.
El contrato actual del directorio proporciona entidad y producto, no nombre ni
identidad del titular: se muestra una etiqueta genérica, sin inventar identidad.
La comprobación explícita de destinatario propio corresponde al envío interno,
igual que en el núcleo existente.

La llave se valida siempre con `ParsearLlave`. Se aceptan formas inequívocas
`@alias`, `correo@dominio.co`, `+573001234567`, o el prefijo `tipo:valor` para los
cinco tipos del núcleo: `documento`, `celular`, `correo`, `alfanumerico`, `comercio`.
Una llave numérica sin tipo se rechaza para no confundir documento, celular y
comercio. El modelo conoce esta regla por la declaración de la función.

## Migración y pruebas

La migración 0004 agrega el origen financiero inmutable. Para filas anteriores usa
`usuario_id::text`, ya que antes de esta HU no había preparador real. El backfill
mantiene el bloqueo exclusivo de la tabla y suspende el trigger de estados sólo
dentro de la transacción de migración, para conservar acciones ya resueltas o
pendientes vencidas. Al terminar restablece ese trigger. La prueba aplica,
revierte y reaplica la migración con esas filas antiguas.

Las pruebas con PostgreSQL comprueban cada rechazo financiero, el GMF, destinatario
propio/inexistente, referencia ajena, conversación ajena, llave habilitada y caída
del directorio. También verifican origen financiero distinto de la parte,
enmascarado persistido, umbral exacto, reemplazo y rollback, diez preparaciones
concurrentes, límite y TTL, rechazo ante caída de Redis y ausencia de cambios en
asientos, postings, transferencias, outbox, disponible y retenido.

```sh
go test -race ./...
go test -race -tags=integration ./internal/copilot ./cmd/migrate
```

La integración requiere `DATABASE_URL` de administración, `APP_DATABASE_URL` con
`rol_api` y `REDIS_URL` apuntando exclusivamente a servicios de prueba desechables.

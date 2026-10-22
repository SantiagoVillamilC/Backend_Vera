# SCRUM-128 · IA-A3-03 · Búsqueda de destinatarios con referencia opaca

`buscar_destinatario` acepta exactamente `celular` o `nombre`. El catálogo valida
el teléfono con `auth.NormalizarCelular` y toma usuario y conversación del contexto
autorizado del servidor. No acepta identidades ni conversaciones del modelo.

## Búsqueda y privacidad

Por celular aplica el resolvedor de auth: credencial activa, celular verificado y
fuera del enfriamiento de cambio de número. Devuelve `RecipientNotFound` si no se
puede resolver y `SelfTransferNotAllowed` para el propio usuario.

Por nombre busca únicamente personas a las que ese usuario ya hizo un envío
interno confirmado y liquidado. El receptor procede del asiento real; nunca se
resuelve una llave telefónica antigua contra su titular actual. Envios recibidos,
fallidos, compensados, externos o de terceros no forman una agenda de destinatarios.

El núcleo pagina hasta 100 IDs distintos por consulta, ordenados por ID. Después
de cerrar su transacción, auth filtra esa página por el nombre visible: primer
nombre e inicial del primer apellido, igual que `nombreEnmascarado`. No se busca
en apellidos privados completos ni en el directorio global. Se ignoran mayúsculas
y acentos españoles, incluso marcas combinadas; `%` y `_` son texto literal.
Las páginas sin coincidencias no cortan prematuramente la búsqueda.

Se devuelven como máximo cinco candidatos y `hay_mas` cuando se encuentra un sexto.
El catálogo envía iniciales enmascaradas, los últimos tres dígitos del celular y
la referencia opaca. Una lista vacía es `[]`. Las instrucciones versionadas piden
al usuario elegir si hay varias coincidencias y pedir el celular si no hay ninguna.

## Presupuesto compartido

`auth.LimiteDestinatarios` centraliza el contador usado por HTTP y el asistente:
`destinatarios:usuario:<HMAC del UsuarioID>`, con `DESTINATARIOS_LIMITE` y
`DESTINATARIOS_VENTANA`. Cuenta también números inexistentes y destinos propios.
No hay un presupuesto adicional por conversación ni por canal.

El contador se consulta antes de auth. Superarlo produce `TooManyRequests`; si
Redis falla, no se consulta el directorio y se devuelve `FunctionUnavailable` al
modelo (HTTP conserva `AuthUnavailable`, 503). La búsqueda por nombre no consume
ese presupuesto de comprobación de números registrados.

## Referencia opaca

`ReferenciasDestinatario` usa AES-256-GCM con nonce aleatorio de 96 bits. Cifra el
UUID del destinatario y el instante de emisión. Autentica además usuario y
conversación como datos asociados; el token base64url no revela teléfono ni UUID.
La clave se deriva con HMAC-SHA256 desde `CONTADORES_PEPPER` y un propósito fijo
`vera:destinatario-ref:v1`, separado del propósito de los contadores.

`Resolver` rechaza manipulación, cambio de usuario/conversación, emisión futura y
edad mayor o igual a 15 minutos. Todas las réplicas deben compartir el pepper;
rotarlo invalida referencias anteriores. No requiere almacenamiento de contactos.

SCRUM-131 usará ese resolvedor antes de preparar una transferencia y tendrá que
revalidar destino y reglas financieras. Esta HU no prepara ni ejecuta dinero ni
agrega endpoints HTTP. Los endpoints conversacionales siguen pendientes.

## Validación

- Pruebas de cifrado, manipulación de cada byte, caducidad exacta, usuario,
  conversación, réplicas y rotación de clave.
- Pruebas de normalización telefónica, errores, cancelación, paginación,
  resultados vacíos y máximo cinco.
- Prueba HTTP de consumo del mismo presupuesto por ambos canales.
- Integración con PostgreSQL real bajo `rol_api` y los roles de cada módulo;
  contador con cliente Redis real contra miniredis: nombres con tildes y marcas
  combinadas, historial propio, deduplicación, elegibilidad, límite y TTL,
  referencias descifrables sólo en su contexto y rechazo cuando Redis cae.

La prueba de integración se ejecuta con `DATABASE_URL` de administración y
`APP_DATABASE_URL` de aplicación en una base desechable:

```sh
go test -race -tags=integration ./internal/copilot -run TestDestinatariosIAHistorialPropioPostgres
```

# Historial y retención — SCRUM-135 / IA-A7-02 / HU-BE-IA-05

Las cuatro rutas requieren autenticación y se acotan por `ParteID` y por el
`UsuarioID` financiero del contexto activo. Una conversación de otro actor o
contexto responde `404`. Consultar y borrar siguen disponibles sin consentimiento
vigente para IA. Ante una denylist no disponible, estas rutas cierran el acceso.

- `GET /v1/vera/conversaciones`: última actividad descendente, desempate por UUID.
- `GET /v1/vera/conversaciones/{id}/mensajes`: orden de mensaje descendente.
  Sólo roles `usuario` y `vera`, texto, canal, fecha y tarjeta asociada actual.
  Omite resultados de herramientas, llamadas internas y metadatos del protocolo.
- `DELETE /v1/vera/conversaciones/{id}`: borra una conversación propia; `204` sin cuerpo.
- `DELETE /v1/vera/conversaciones`: borra el historial del contexto activo;
  `204`, también si ya estaba vacío. Otros contextos conservan su historial.

Ambos listados aceptan `limit` de 1 a 100 (50 por defecto) y `cursor` opaco.
`meta` contiene `limit`, `has_more` y `next_cursor` (string o null). El cliente
reenvía el cursor sin interpretarlo. Se vincula a la lista y al contexto; no
concede autorización. Las consultas siempre filtran propietario. El cursor de
conversaciones guarda actividad y UUID, por lo que no requiere que el ancla siga
existiendo. La lista refleja actividad viva, no una instantánea entre páginas.
Fechas en UTC-5 y montos en strings de centavos, sin aritmética del modelo.

La migración `0007_historial_retencion.sql` permite borrar sólo conversaciones
al rol de aplicación. Su trigger cancela primero las acciones pendientes; después
se eliminan físicamente mensajes, acciones y claves de reintento por cascada.
Confirmación y borrado comparten el candado de conversación: gana una de las
operaciones; no se puede confirmar una acción ya borrada. Un ciclo de IA que ya
estaba en curso no puede volver a guardar contenido en una conversación borrada.
El siguiente envío sin ID crea una conversación nueva, sin el contexto eliminado.

Se conservan transferencias, asientos, desafíos de auth y auditoría saneada de
llamadas (`llamada_funcion`), con sus UUID de correlación. Los UUID de auditoría
ya no son FK que impidan borrar contenido. Un trigger de inserción comprueba
propietario y mensaje y toma candados de referencia antes de admitir el evento.
Sólo esa comprobación usa `SECURITY DEFINER`, con tablas calificadas y
`search_path=pg_catalog`, porque los candados de mensaje requieren privilegios
que el rol append-only no tiene. La aplicación sigue sin poder leer, editar,
borrar ni truncar auditoría. El rollback de migración conserva auditorías
huérfanas mediante FK `NOT VALID`; no recupera contenido ya borrado.

`VERA_RETENCION_DIAS=30` admite enteros de 1 a 36500. River ejecuta el barrido al
iniciar y cada 15 minutos, con reintentos y sin datos personales en los argumentos.
El corte usa días de 24 horas desde la última actividad, incluidas conversaciones
archivadas. Cada ejecución borra hasta 1000 filas en lotes de 100 transaccionales,
con `FOR UPDATE SKIP LOCKED`; filas ocupadas se revisan en la siguiente ejecución.
Usa exactamente el mismo trigger de cancelación y borrado que los endpoints.

Pruebas: PostgreSQL con `rol_api`, aislamiento de actor/contexto, paginación con
empates y ancla borrada, filtrado de protocolo, tarjeta actual, borrado individual
y total, cancelación previa al borrado, auditoría y ledger conservados, memoria
inaccesible, retención configurable y candados ocupados. Los tests HTTP verifican
contrato, fechas, centavos, autenticación, errores saneados y respuestas `204`.

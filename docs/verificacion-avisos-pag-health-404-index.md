# Verificación de avisos, paginación, salud e índices

Implementadas las cinco tareas del handoff en la rama feat/avisos-pag-health-404-index.

## Contrato

- GET /v1/inteligencia/avisos/{id}: tarjeta propia, 400 UUID inválido, 401 sin sesión,
  404 idéntico para ajeno e inexistente. Detalle y descarte comparten el helper del 404.
- Listado: limite 1–50 (20 por defecto), estado vigentes/descartados y cursor base64 v1.
  Orden total: prioridad ASC, severidad DESC, clave ASC, id ASC. Se pide una fila extra
  y la continuación va en X-Vera-Siguiente-Cursor, expuesta por CORS. Data es arreglo,
  meta sigue en null. Las llamadas antiguas con solo mes conservan su ventana mensual.
  Combinar mes con limite/cursor/estado devuelve 400; no se ignora el mes.
- GET /readyz/modelos: público, exento del rate limit, solo URLs explícitas, consultas
  paralelas con dos segundos por dependencia. Ausencia de configuración es 200;
  5xx o fallo de conexión es 503. No modifica /readyz ni añade Redis a esa sonda.
- Migración 0028: índices parciales de vigentes y descartados, reversibles.

## Verificado

- go build ./... y go vet ./... pasan.
- Pruebas unitarias de inteligencia, httpserver y cmd/api con -race pasan.
- Integración nueva: empates de clave/prioridad/severidad sin repeticiones ni saltos,
  acceso propio/ajeno/inexistente, descarte idempotente e historial de descartadas.
- TestReadyzNoDependeDeRedis pasa con PostgreSQL real.
- Migración 0028 up/down/up sobre PostgreSQL 17 desechable.
- EXPLAIN con 10.000 avisos y usuario/periodo fijos: Index Scan usando
  aviso_vigente_por_usuario_periodo, sin Sort. La inserción de prueba se revierte.
- Generación OpenAPI y sqlc ejecutada con las versiones fijadas por el proyecto.

## Diferencias de la base y límites de verificación

sqlc.yaml referenciaba 0021_indice_movimientos_por_cuenta_fecha.sql, inexistente.
Con autorización expresa del usuario se corrigió a 0027_indice_movimientos_por_cuenta_fecha.sql.
No se cambió el esquema ni el ledger para resolverlo.

La revisión adicional resolvió los bloqueos de verificación:

- TestKYCRealUsuarioDePruebaRespondePorHTTP usaba un usuario fijo sin sembrarlo.
  Se añadió auth.SembrarParteDePrueba, el helper ya usado por otras pruebas KYC.
  El cambio afecta únicamente la preparación de esa prueba, no la lógica de KYC.
- Los dos paquetes de integración comparten el ledger de prueba. Se ejecutan con
  -p 1 para evitar que la escritura de un paquete altere las mediciones del otro.
- La suite completa de ambos paquetes pasó con PostgreSQL 17 y Redis desechables:
  go test -p 1 -race -tags=integration -count=1 ./internal/inteligencia ./internal/platform/httpserver

La prueba HTTP nueva usa el router real, el servicio real y PostgreSQL. Comprueba
paginación, tarjeta propia, 404 idéntico en detalle/descarte para ajeno/inexistente,
conservación de la tarjeta del dueño y parámetros inválidos. Las pruebas adicionales
cubren los límites 1 y 50, y 105 consultas públicas a /readyz/modelos sin que el rate
limit ni un pool PostgreSQL ausente bloqueen la sonda.

La paginación no filtra por periodo. El índice solo evita Sort cuando se fija periodo;
se conserva la limitación documentada en el handoff. No se implementa A2-01.
El usuario autorizó el commit y la publicación de todos los cambios en
feat/avisos-pag-health-404-index el 11 de noviembre de 2026.

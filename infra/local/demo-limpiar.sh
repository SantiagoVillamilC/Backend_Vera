#!/usr/bin/env bash
# infra/local/demo-limpiar.sh
# Borra el rastro de una cuenta de demo en la base vera_semilla_ que usa la API desplegada, en UNA
# transacción y con el DSN del dueño por el túnel SSH (127.0.0.1:55437). Sin CONFIRMAR=si solo
# simula: imprime qué borraría con conteos por tabla y no escribe nada.
#
# Uso:
#   export DATABASE_URL='postgres://dueno:CLAVE@127.0.0.1:55437/vera_semilla_demo?sslmode=disable'
#   ./infra/local/demo-limpiar.sh                      # simulación, solo SELECT
#   CONFIRMAR=si ./infra/local/demo-limpiar.sh         # borra
#   DEMO_USUARIO=<uuid> CONFIRMAR=si ./infra/local/demo-limpiar.sh
#
# Seguridad:
#   - Rechaza los usuarios protegidos de la lista de lib-demo.sh.
#   - Rechaza cualquier usuario que no sea el DEMO_USUARIO por defecto y no tenga fila en
#     public.demo_escenario.
#   - Aborta, sin escribir, si un asiento del usuario toca cuentas de otro titular o si hay eventos
#     pendientes del usuario en alguna outbox. La comprobación se hace antes y se repite dentro de la
#     transacción, con los candados tomados, para cubrir lo que aparezca en medio.
#   - Dentro de la transacción toma los candados 'escenario:<usuario>', 'inteligencia.enriquecimiento'
#     y 'inteligencia.avisos' (ver el comentario junto al BEGIN).
# Con la clave del DSN: psql recibe usuario, clave y base por variables de entorno de su propio
# proceso (lib-demo.sh), nunca como argumento, así que la clave no aparece en la lista de procesos.
#
# Lo que NO se borra (ver docs/escenario-demo.md): auth.eventos_autenticacion (append-only por
# trigger), auth.consentimientos y auth.kyc_verificaciones (inmutables), las dos outbox,
# nucleo.descuadre, inteligencia.promocion_destino, los trabajos de river y el catálogo en memoria
# de la API.
set -euo pipefail

DATABASE_URL="${DATABASE_URL:-}"
DEMO_USUARIO="${DEMO_USUARIO:-11111111-1111-4111-8111-111111111111}"
CONFIRMAR="${CONFIRMAR:-no}"

paso() { printf '\n==> %s\n' "$1"; }
info() { printf '    %s\n' "$1"; }
error() { printf '[error] %s\n' "$1" >&2; exit 1; }

# La lista de usuarios protegidos, la validación del DSN y psql_demo viven en lib-demo.sh.
# shellcheck source=lib-demo.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib-demo.sh"

preparar_conexion
command -v psql >/dev/null || error "falta psql"
[[ "$CONFIRMAR" == "si" || "$CONFIRMAR" == "no" ]] || error "CONFIRMAR debe ser si o no"

DEMO_USUARIO="${DEMO_USUARIO,,}"
[[ "$DEMO_USUARIO" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]] ||
  error "DEMO_USUARIO no es un UUID"
rechazar_usuario_protegido
exigir_usuario_de_demo

# Conjuntos de filas del usuario. Cada consulta se usa como CTE en la simulación y como tabla
# temporal en la transacción de borrado, así que los conteos y el DELETE comparten los mismos
# predicados. Los nombres de los conjuntos son los de las CTE.
read -r -d '' Q_CTA <<'SQL' || true
SELECT id FROM nucleo.cuenta
 WHERE titular_id = :'u' OR id LIKE 'pasivo:usuario:' || :'u' || ':%'
SQL
read -r -d '' Q_DEP <<'SQL' || true
SELECT id FROM nucleo.deposito WHERE usuario_id = :'u'
SQL
read -r -d '' Q_TRA <<'SQL' || true
SELECT id FROM nucleo.transferencia
 WHERE usuario_id = :'u' OR cuenta_origen IN (SELECT id FROM cta)
SQL
read -r -d '' Q_ENT <<'SQL' || true
SELECT entry_id AS id FROM nucleo.posting WHERE cuenta_id IN (SELECT id FROM cta)
UNION
SELECT id FROM nucleo.journal_entry
 WHERE deposito_id IN (SELECT id FROM dep) OR transferencia_id IN (SELECT id FROM tra)
SQL
read -r -d '' Q_CON <<'SQL' || true
SELECT id FROM asistente.conversacion
 WHERE usuario_id = :'u'::uuid OR usuario_financiero_id = :'u'
SQL
read -r -d '' Q_CRE <<'SQL' || true
SELECT id FROM auth.credenciales WHERE parte_id = :'u'::uuid
SQL
read -r -d '' Q_AVI <<'SQL' || true
SELECT id FROM inteligencia.aviso WHERE usuario_id = :'u'
SQL

CTES="cta AS ($Q_CTA), dep AS ($Q_DEP), tra AS ($Q_TRA), ent AS ($Q_ENT), con AS ($Q_CON), cre AS ($Q_CRE), avi AS ($Q_AVI)"

# Bloqueos: condiciones que hacen inseguro el borrado. Una fila por motivo.
read -r -d '' Q_BLOQUEOS <<'SQL' || true
SELECT m FROM (
  SELECT 'asientos del usuario que tocan cuentas de otro titular: ' || count(DISTINCT p.entry_id) AS m
    FROM nucleo.posting p JOIN nucleo.cuenta c ON c.id = p.cuenta_id
   WHERE p.entry_id IN (SELECT id FROM ent) AND c.titular_id IS NOT NULL AND c.titular_id <> :'u'
  HAVING count(*) > 0
  UNION ALL
  SELECT 'cuentas con el patron del usuario y otro titular: ' || count(*)
    FROM nucleo.cuenta WHERE id IN (SELECT id FROM cta) AND titular_id IS DISTINCT FROM :'u'
  HAVING count(*) > 0
  UNION ALL
  SELECT 'eventos pendientes del usuario en nucleo.outbox: ' || count(*)
    FROM nucleo.outbox WHERE despachado_en IS NULL AND payload::text LIKE '%' || :'u' || '%'
  HAVING count(*) > 0
  UNION ALL
  SELECT 'eventos pendientes del usuario en auth.outbox: ' || count(*)
    FROM auth.outbox WHERE despachado_en IS NULL AND payload::text LIKE '%' || :'u' || '%'
  HAVING count(*) > 0
) b
SQL

# Conteos por tabla. Se ejecuta con las CTE al frente (simulación y verificación posterior) o con
# las tablas temporales ya creadas (dentro de la transacción).
read -r -d '' Q_CONTEOS <<'SQL' || true
SELECT accion, tabla, n FROM (
  SELECT 'borra' AS accion, 'asistente.llamada_funcion' AS tabla, count(*) AS n FROM asistente.llamada_funcion
   WHERE usuario_id = :'u'::uuid OR conversacion_id IN (SELECT id FROM con)
  UNION ALL SELECT 'borra', 'asistente.solicitud_texto', count(*) FROM asistente.solicitud_texto
   WHERE usuario_id = :'u'::uuid OR conversacion_id IN (SELECT id FROM con)
  UNION ALL SELECT 'borra', 'asistente.mensaje', count(*) FROM asistente.mensaje
   WHERE conversacion_id IN (SELECT id FROM con)
  UNION ALL SELECT 'borra', 'asistente.accion_pendiente', count(*) FROM asistente.accion_pendiente
   WHERE usuario_id = :'u'::uuid OR usuario_financiero_id = :'u' OR conversacion_id IN (SELECT id FROM con)
  UNION ALL SELECT 'borra', 'asistente.conversacion', count(*) FROM con
  UNION ALL SELECT 'borra', 'inteligencia.aviso_entrega', count(*) FROM inteligencia.aviso_entrega
   WHERE aviso_id IN (SELECT id FROM avi)
  UNION ALL SELECT 'borra', 'inteligencia.aviso', count(*) FROM avi
  UNION ALL SELECT 'borra', 'inteligencia.movimiento_enriquecido', count(*) FROM inteligencia.movimiento_enriquecido
   WHERE usuario_id = :'u'
  UNION ALL SELECT 'borra', 'inteligencia.memoria_correccion', count(*) FROM inteligencia.memoria_correccion
   WHERE usuario_id = :'u'
  UNION ALL SELECT 'borra', 'inteligencia.voto_destino', count(*) FROM inteligencia.voto_destino
   WHERE usuario_id = :'u'
  UNION ALL SELECT 'borra', 'inteligencia.catalogo_marca (escenario)', count(*) FROM inteligencia.catalogo_marca
   WHERE origen = 'escenario' AND nombre_normalizado = 'TIENDA-CATALOGO-DEMO'
     AND NOT EXISTS (SELECT 1 FROM public.demo_escenario WHERE usuario_id <> :'u'::uuid)
  UNION ALL SELECT 'borra', 'nucleo.posting', count(*) FROM nucleo.posting WHERE entry_id IN (SELECT id FROM ent)
  UNION ALL SELECT 'borra', 'nucleo.journal_entry', count(*) FROM nucleo.journal_entry WHERE id IN (SELECT id FROM ent)
  UNION ALL SELECT 'borra', 'nucleo.transferencia', count(*) FROM nucleo.transferencia WHERE id IN (SELECT id FROM tra)
  UNION ALL SELECT 'borra', 'nucleo.deposito', count(*) FROM nucleo.deposito WHERE id IN (SELECT id FROM dep)
  UNION ALL SELECT 'borra', 'nucleo.cuenta', count(*) FROM nucleo.cuenta
   WHERE id IN (SELECT id FROM cta) AND titular_id = :'u'
  UNION ALL SELECT 'borra', 'auth.transiciones_credencial', count(*) FROM auth.transiciones_credencial
   WHERE credencial_id IN (SELECT id FROM cre)
  UNION ALL SELECT 'borra', 'auth.codigos_respaldo', count(*) FROM auth.codigos_respaldo
   WHERE credencial_id IN (SELECT id FROM cre)
  UNION ALL SELECT 'borra', 'auth.tokens_restablecimiento', count(*) FROM auth.tokens_restablecimiento
   WHERE credencial_id IN (SELECT id FROM cre)
  UNION ALL SELECT 'borra', 'auth.desafios', count(*) FROM auth.desafios
   WHERE parte_id = :'u'::uuid OR credencial_id IN (SELECT id FROM cre)
  UNION ALL SELECT 'borra', 'auth.sesiones', count(*) FROM auth.sesiones WHERE parte_id = :'u'::uuid
  UNION ALL SELECT 'borra', 'auth.credenciales', count(*) FROM cre
  UNION ALL SELECT 'borra', 'auth.roles_de_parte', count(*) FROM auth.roles_de_parte
   WHERE parte_id = :'u'::uuid OR representada_id = :'u'::uuid
  UNION ALL SELECT 'borra', 'auth.empresas', count(*) FROM auth.empresas WHERE parte_id = :'u'::uuid
  UNION ALL SELECT 'borra', 'auth.kyc_estados', count(*) FROM auth.kyc_estados WHERE parte_id = :'u'::uuid
  UNION ALL SELECT :'acc_partes', 'auth.personas', count(*) FROM auth.personas WHERE parte_id = :'u'::uuid
  UNION ALL SELECT :'acc_partes', 'auth.partes', count(*) FROM auth.partes WHERE id = :'u'::uuid
  UNION ALL SELECT 'borra', 'public.users', count(*) FROM public.users WHERE id = :'u'::uuid
  UNION ALL SELECT 'borra', 'public.demo_escenario', count(*) FROM public.demo_escenario WHERE usuario_id = :'u'::uuid
  UNION ALL SELECT 'conserva', 'auth.eventos_autenticacion (append-only)', count(*) FROM auth.eventos_autenticacion
   WHERE actor_parte_id = :'u'::uuid
  UNION ALL SELECT 'conserva', 'auth.consentimientos (inmutable)', count(*) FROM auth.consentimientos
   WHERE parte_id = :'u'::uuid
  UNION ALL SELECT 'conserva', 'auth.kyc_verificaciones (inmutable)', count(*) FROM auth.kyc_verificaciones
   WHERE parte_id = :'u'::uuid
  UNION ALL SELECT 'conserva', 'nucleo.outbox (despachados)', count(*) FROM nucleo.outbox
   WHERE payload::text LIKE '%' || :'u' || '%'
  UNION ALL SELECT 'conserva', 'auth.outbox (despachados)', count(*) FROM auth.outbox
   WHERE payload::text LIKE '%' || :'u' || '%'
  UNION ALL SELECT 'conserva', 'nucleo.descuadre', count(*) FROM nucleo.descuadre
   WHERE transferencia_id IN (SELECT id FROM tra)
) c ORDER BY accion, tabla
SQL

# Qué hacer con auth.partes y auth.personas: las filas de consentimientos y de verificación KYC son
# inmutables y apuntan a la parte, así que si existen la parte se conserva y el resto se borra.
retener="$(psql_demo -At <<<"SELECT (SELECT count(*) FROM auth.consentimientos WHERE parte_id = :'u'::uuid) + (SELECT count(*) FROM auth.kyc_verificaciones WHERE parte_id = :'u'::uuid);")"
if [[ "$retener" == "0" ]]; then
  ACC_PARTES="borra"
else
  ACC_PARTES="conserva"
fi

bloqueos="$(psql_demo -At <<<"WITH $CTES $Q_BLOQUEOS;")"

paso "Usuario $DEMO_USUARIO"
info "Base: $(psql_demo -At <<<'SELECT current_database();')"
if [[ "$ACC_PARTES" == "conserva" ]]; then
  info "El usuario tiene consentimientos o verificaciones KYC inmutables: auth.partes y auth.personas se conservan."
fi

if [[ "$CONFIRMAR" != "si" ]]; then
  paso "Simulación: esto es lo que se borraría (solo SELECT, no se escribe nada)"
  {
    printf 'BEGIN READ ONLY;\n'
    printf 'WITH %s %s;\n' "$CTES" "$Q_CONTEOS"
    printf 'ROLLBACK;\n'
  } | PGOPTIONS='-c default_transaction_read_only=on' psql_demo -v acc_partes="$ACC_PARTES"
  if [[ -n "$bloqueos" ]]; then
    printf '\n[bloqueo] con CONFIRMAR=si el borrado abortaría por:\n' >&2
    printf '    %s\n' "$bloqueos" >&2
  fi
  paso "Simulación terminada"
  info "Para borrar, vuelve a ejecutar con CONFIRMAR=si."
  exit 0
fi

if [[ -n "$bloqueos" ]]; then
  printf '[error] el borrado no es seguro:\n' >&2
  printf '    %s\n' "$bloqueos" >&2
  exit 1
fi

paso "Borrando en una sola transacción"
if ! {
  cat <<'SQL'
BEGIN;
SET LOCAL lock_timeout = '60s';
-- Candados, siempre en este orden. Los tres son bloqueantes y se sueltan al terminar la transacción.
-- 1. 'escenario:<usuario>': el mismo que toma semilla-escenario, así no se siembra y se borra a la vez.
-- 2. 'inteligencia.enriquecimiento': la llave de internal/inteligencia/enriquecimiento.go (intentarBloqueo)
--    y de correccion.go. El lote la pide con pg_try_advisory_xact_lock. Si un lote ya la tiene, esta
--    sentencia espera a que termine (hasta lock_timeout) y borra después de su commit. Mientras la
--    transacción dure, un ciclo nuevo del lote recibe ErrLoteEnCurso y se salta ese turno. LeerCursor
--    es un SELECT simple y no sirve de candado: el lote solo toca la fila del cursor al final.
-- 3. 'inteligencia.avisos': la llave de la emisión de avisos (tomarElCandadoDeAvisos en
--    avisos_servicio.go), que la pide de forma bloqueante, usuario por usuario.
SELECT pg_advisory_xact_lock(hashtextextended('escenario:' || :'u', 0));
SELECT pg_advisory_xact_lock(hashtext('inteligencia.enriquecimiento')::bigint);
SELECT pg_advisory_xact_lock(hashtext('inteligencia.avisos')::bigint);
SQL
  printf 'CREATE TEMP TABLE cta ON COMMIT DROP AS %s;\n' "$Q_CTA"
  printf 'CREATE TEMP TABLE dep ON COMMIT DROP AS %s;\n' "$Q_DEP"
  printf 'CREATE TEMP TABLE tra ON COMMIT DROP AS %s;\n' "$Q_TRA"
  printf 'CREATE TEMP TABLE ent ON COMMIT DROP AS %s;\n' "$Q_ENT"
  printf 'CREATE TEMP TABLE con ON COMMIT DROP AS %s;\n' "$Q_CON"
  printf 'CREATE TEMP TABLE cre ON COMMIT DROP AS %s;\n' "$Q_CRE"
  printf 'CREATE TEMP TABLE avi ON COMMIT DROP AS %s;\n' "$Q_AVI"
  # Los bloqueos se vuelven a evaluar aquí, con los candados tomados y las tablas temporales ya
  # creadas: lo que apareció entre la comprobación previa y el BEGIN aborta la transacción entera.
  printf 'CREATE TEMP TABLE bloqueos ON COMMIT DROP AS %s;\n' "$Q_BLOQUEOS"
  cat <<'SQL'
DO $$
DECLARE motivos text;
BEGIN
  SELECT string_agg(b.m, ' | ') INTO motivos FROM bloqueos b;
  IF motivos IS NOT NULL THEN
    RAISE EXCEPTION 'el borrado no es seguro, no se borro nada: %', motivos;
  END IF;
END
$$;
SQL
  printf '\\echo Antes:\n%s;\n' "$Q_CONTEOS"
  cat <<'SQL'
-- asistente: mensaje apunta a accion_pendiente sin cascada, por eso va antes.
DELETE FROM asistente.llamada_funcion WHERE usuario_id = :'u'::uuid OR conversacion_id IN (SELECT id FROM con);
DELETE FROM asistente.solicitud_texto WHERE usuario_id = :'u'::uuid OR conversacion_id IN (SELECT id FROM con);
DELETE FROM asistente.mensaje WHERE conversacion_id IN (SELECT id FROM con);
DELETE FROM asistente.accion_pendiente
 WHERE usuario_id = :'u'::uuid OR usuario_financiero_id = :'u' OR conversacion_id IN (SELECT id FROM con);
DELETE FROM asistente.conversacion WHERE id IN (SELECT id FROM con);
-- inteligencia
DELETE FROM inteligencia.aviso_entrega WHERE aviso_id IN (SELECT id FROM avi);
DELETE FROM inteligencia.aviso WHERE id IN (SELECT id FROM avi);
DELETE FROM inteligencia.movimiento_enriquecido WHERE usuario_id = :'u';
DELETE FROM inteligencia.memoria_correccion WHERE usuario_id = :'u';
DELETE FROM inteligencia.voto_destino WHERE usuario_id = :'u';
-- La marca del escenario es global: solo se retira si no queda otro usuario de demo.
DELETE FROM inteligencia.catalogo_marca
 WHERE origen = 'escenario' AND nombre_normalizado = 'TIENDA-CATALOGO-DEMO'
   AND NOT EXISTS (SELECT 1 FROM public.demo_escenario WHERE usuario_id <> :'u'::uuid);
-- nucleo: se borran todos los apuntes de los asientos del usuario (incluidas las cuentas de
-- sistema como pasivo:gmf_por_pagar) y solo las cuentas cuyo titular es el usuario.
DELETE FROM nucleo.transferencia WHERE id IN (SELECT id FROM tra);
DELETE FROM nucleo.posting WHERE entry_id IN (SELECT id FROM ent);
DELETE FROM nucleo.journal_entry WHERE id IN (SELECT id FROM ent);
DELETE FROM nucleo.deposito WHERE id IN (SELECT id FROM dep);
DELETE FROM nucleo.cuenta WHERE id IN (SELECT id FROM cta) AND titular_id = :'u';
-- auth
DELETE FROM auth.transiciones_credencial WHERE credencial_id IN (SELECT id FROM cre);
DELETE FROM auth.codigos_respaldo WHERE credencial_id IN (SELECT id FROM cre);
DELETE FROM auth.tokens_restablecimiento WHERE credencial_id IN (SELECT id FROM cre);
DELETE FROM auth.desafios WHERE parte_id = :'u'::uuid OR credencial_id IN (SELECT id FROM cre);
DELETE FROM auth.sesiones WHERE parte_id = :'u'::uuid;
DELETE FROM auth.credenciales WHERE id IN (SELECT id FROM cre);
DELETE FROM auth.roles_de_parte WHERE parte_id = :'u'::uuid OR representada_id = :'u'::uuid;
DELETE FROM auth.empresas WHERE parte_id = :'u'::uuid;
DELETE FROM auth.kyc_estados WHERE parte_id = :'u'::uuid;
SQL
  if [[ "$ACC_PARTES" == "borra" ]]; then
    cat <<'SQL'
DELETE FROM auth.personas WHERE parte_id = :'u'::uuid;
DELETE FROM auth.partes WHERE id = :'u'::uuid;
SQL
  fi
  cat <<'SQL'
-- public
DELETE FROM public.users WHERE id = :'u'::uuid;
DELETE FROM public.demo_escenario WHERE usuario_id = :'u'::uuid;
COMMIT;
SQL
} | psql_demo -v acc_partes="$ACC_PARTES"; then
  error "la transacción de borrado falló y se deshizo completa, no se borró nada. Revisa el mensaje de arriba (bloqueo de seguridad o lock_timeout de 60 s esperando a un lote) y vuelve a ejecutar."
fi

# Verificación con una conexión nueva: lo confirmado de verdad en la base.
paso "Después del borrado"
psql_demo -v acc_partes="$ACC_PARTES" <<<"WITH $CTES $Q_CONTEOS;"
restantes="$(psql_demo -At <<<"SELECT (SELECT count(*) FROM inteligencia.movimiento_enriquecido WHERE usuario_id = :'u') + (SELECT count(*) FROM inteligencia.aviso WHERE usuario_id = :'u');")"
if [[ "$restantes" != "0" ]]; then
  error "quedan $restantes filas de inteligencia del usuario tras el borrado: un proceso de la API las escribió después del commit del borrado. Vuelve a ejecutar con CONFIRMAR=si."
fi
paso "Listo"
info "No se tocó el cursor global ni los modelos."

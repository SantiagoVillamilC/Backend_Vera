#!/usr/bin/env bash
# infra/local/demo-inyectar.sh
# Deja la cuenta de demo lista en la base vera_semilla_ que usa la API desplegada, desde el
# portátil con el túnel SSH abierto contra 127.0.0.1:55437 (ssh -N -L 55437:127.0.0.1:55437 centinela).
#
# Orden:
#   1. Valida el DSN y las herramientas (psql, go, jq).
#   2. Si el usuario tiene asientos fuera del manifiesto (por ejemplo una recarga PSE), llama a
#      demo-limpiar.sh con CONFIRMAR=si, porque REINICIAR=si aborta con asientos ajenos.
#   3. Siembra el escenario (REINICIAR=si si ya hay manifiesto).
#   4. Espera a que el lote de enriquecimiento de la API pase por esos asientos. El lote corre cada
#      5 minutos con un cursor global, reescribe los asientos nuevos con tfidf y deja sin_categoria
#      la fila del escalón catálogo.
#   5. Vuelve a sembrar SIN REINICIAR: el comando re-enriquece los asientos del manifiesto y el
#      censo queda determinista.
#   6. Crea la cuenta con login-demo.
#   7. Verifica. Con API_URL hace login y consulta saldo, avisos y resumen.
#
# Es idempotente: ejecutarlo dos veces seguidas deja el mismo resultado.
#
# Uso:
#   export DATABASE_URL='postgres://dueno:CLAVE@127.0.0.1:55437/vera_semilla_demo?sslmode=disable'
#   ./infra/local/demo-inyectar.sh
#   API_URL=https://api.ejemplo.test ./infra/local/demo-inyectar.sh
#   ESPERAR_LOTE=no ./infra/local/demo-inyectar.sh     # base sin lote corriendo
#
# Variables: DATABASE_URL (obligatoria), DEMO_USUARIO, DEMO_FECHA, DEMO_CELULAR, DEMO_CONTRASENA,
# DEMO_NOMBRE, ESPERAR_LOTE (si o no), ESPERA_SEGUNDOS (720), ESTADO, API_URL.
set -euo pipefail

RAIZ="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$RAIZ"

DATABASE_URL="${DATABASE_URL:-}"
DEMO_USUARIO="${DEMO_USUARIO:-11111111-1111-4111-8111-111111111111}"
DEMO_FECHA="${DEMO_FECHA:-$(TZ=America/Bogota date +%Y-%m-19)}"
DEMO_CELULAR="${DEMO_CELULAR:-+573205550142}"
DEMO_CONTRASENA="${DEMO_CONTRASENA:-Vera-Demo-2026}"
DEMO_NOMBRE="${DEMO_NOMBRE:-Andres Vargas}"
ESPERAR_LOTE="${ESPERAR_LOTE:-si}"
ESPERA_SEGUNDOS="${ESPERA_SEGUNDOS:-720}"
API_URL="${API_URL:-}"
# Cupo del escenario: la API de producción usa 3, esto solo afecta a la siembra.
AVISOS_CUPO=5

paso() { printf '\n==> %s\n' "$1"; }
info() { printf '    %s\n' "$1"; }
error() { printf '[error] %s\n' "$1" >&2; exit 1; }

[[ -n "$DATABASE_URL" ]] || error "exporta DATABASE_URL del dueño por el túnel"
dsn_valido='^postgres(ql)?://[^@/]+@127\.0\.0\.1:55437/vera_semilla_[A-Za-z0-9_]+(\?sslmode=[a-z-]+)?$'
[[ "$DATABASE_URL" =~ $dsn_valido ]] ||
  error "DATABASE_URL debe apuntar a 127.0.0.1:55437/vera_semilla_ (túnel SSH) y solo admite sslmode"
for herramienta in psql go jq; do
  command -v "$herramienta" >/dev/null || error "falta $herramienta"
done
[[ -z "$API_URL" ]] || command -v curl >/dev/null || error "falta curl, necesario con API_URL"

DEMO_USUARIO="${DEMO_USUARIO,,}"
[[ "$DEMO_USUARIO" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]] ||
  error "DEMO_USUARIO no es un UUID"
[[ "$DEMO_FECHA" =~ ^[0-9]{4}-[0-9]{2}-19$ ]] || error "DEMO_FECHA debe ser YYYY-MM-19"
[[ "$ESPERAR_LOTE" == "si" || "$ESPERAR_LOTE" == "no" ]] || error "ESPERAR_LOTE debe ser si o no"
[[ "$ESPERA_SEGUNDOS" =~ ^[0-9]+$ ]] || error "ESPERA_SEGUNDOS debe ser un entero"
ESTADO="${ESTADO:-$RAIZ/out/demo/escenario-$DEMO_USUARIO.json}"
mkdir -p "$(dirname "$ESTADO")"

# Mismo formato que valida login-demo: +57 y diez dígitos.
celular="${DEMO_CELULAR// /}"
celular="${celular#+57}"
[[ "$celular" =~ ^[0-9]{10}$ ]] || error "DEMO_CELULAR debe ser +57 seguido de 10 dígitos o 10 dígitos"
celular="+57$celular"

psql_demo() { psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 -v u="$DEMO_USUARIO" "$@"; }

estado_del_usuario() {
  psql_demo -At -F ' ' <<'SQL'
SELECT (SELECT count(*) FROM public.demo_escenario WHERE usuario_id = :'u'::uuid),
       (SELECT count(*) FROM nucleo.posting p
         WHERE p.cuenta_id LIKE 'pasivo:usuario:' || :'u' || ':%'
           AND NOT (p.entry_id = ANY(COALESCE(
                 (SELECT asientos FROM public.demo_escenario WHERE usuario_id = :'u'::uuid),
                 '{}'::bigint[]))));
SQL
}

sembrar_escenario() {
  local reiniciar="$1"
  DATABASE_URL="$DATABASE_URL" DEMO_USUARIO="$DEMO_USUARIO" DEMO_FECHA="$DEMO_FECHA" \
    AVISOS_CUPO="$AVISOS_CUPO" ESTADO="$ESTADO" REINICIAR="$reiniciar" \
    out/bin/semilla-escenario >/dev/null
  jq -r '"asientos=\(.asientos | length) series=\(.series) avisos=\(.avisos | tojson)"' "$ESTADO" | sangrar
}

# Imprime cada línea de stdin con la sangría de info.
sangrar() {
  local linea
  while IFS= read -r linea; do info "$linea"; done
}

esperar_lote() {
  local destino="$1" limite=$((SECONDS + ESPERA_SEGUNDOS)) actual
  while true; do
    actual="$(psql_demo -At <<<"SELECT ultimo_entry_id FROM inteligencia.cursor_enriquecimiento WHERE id = 1;")"
    [[ "$actual" =~ ^[0-9]+$ ]] || error "no hay fila en inteligencia.cursor_enriquecimiento"
    if ((actual >= destino)); then
      info "cursor en $actual, alcanzó el asiento $destino"
      return 0
    fi
    if ((SECONDS >= limite)); then
      error "el lote de enriquecimiento no alcanzó el asiento $destino en $((ESPERA_SEGUNDOS / 60)) minutos (cursor en $actual). La API desplegada puede estar apagada o sin lote. Si en esta base no corre un lote, vuelve a ejecutar con ESPERAR_LOTE=no."
    fi
    info "cursor en $actual, faltan hasta el asiento $destino (siguiente revisión en 15 s)"
    sleep 15
  done
}

paso "Compilando los binarios"
go build -o out/bin/semilla-escenario ./cmd/semilla-escenario
go build -o out/bin/login-demo ./cmd/login-demo

paso "Revisando el estado actual del usuario $DEMO_USUARIO"
read -r en_manifiesto ajenos < <(estado_del_usuario)
info "manifiesto: $en_manifiesto, asientos fuera del manifiesto: $ajenos"
if [[ "$ajenos" != "0" ]]; then
  paso "Limpiando: hay asientos fuera del manifiesto (por ejemplo una recarga confirmada)"
  DATABASE_URL="$DATABASE_URL" DEMO_USUARIO="$DEMO_USUARIO" CONFIRMAR=si \
    bash "$RAIZ/infra/local/demo-limpiar.sh"
  # La limpieza borra el manifiesto: se vuelve a leer para elegir REINICIAR.
  read -r en_manifiesto ajenos < <(estado_del_usuario)
  [[ "$ajenos" == "0" ]] || error "tras la limpieza siguen $ajenos asientos fuera del manifiesto"
fi

paso "Comprobando el celular de la demo"
otro="$(psql_demo -At -v c="$celular" <<<"SELECT count(*) FROM auth.credenciales WHERE celular = :'c' AND parte_id <> :'u'::uuid;")"
[[ "$otro" == "0" ]] ||
  error "otro usuario ya tiene el celular $celular. Elige otro DEMO_CELULAR para este DEMO_USUARIO."

paso "Sembrando el escenario del $DEMO_FECHA"
if [[ "$en_manifiesto" == "1" ]]; then
  sembrar_escenario si
else
  sembrar_escenario no
fi

if [[ "$ESPERAR_LOTE" == "si" ]]; then
  destino="$(jq -r '.asientos | max' "$ESTADO")"
  paso "Esperando al lote de enriquecimiento (máximo $((ESPERA_SEGUNDOS / 60)) minutos)"
  esperar_lote "$destino"
  paso "Sembrando de nuevo sin reiniciar, para dejar el censo determinista"
  sembrar_escenario no
else
  paso "ESPERAR_LOTE=no: sin espera y sin segunda siembra"
  info "Si la API tiene un lote corriendo, su próximo ciclo puede reescribir estos asientos."
  info "Repite la siembra sin REINICIAR cuando el cursor pase del último asiento."
fi

paso "Creando la cuenta de la demo"
out/bin/login-demo \
  -usuario "$DEMO_USUARIO" \
  -celular "$celular" \
  -contrasena "$DEMO_CONTRASENA" \
  -nombre "$DEMO_NOMBRE" |
  jq -r '"credencial_id=\(.credencial_id) celular=\(.celular) nombre=\(.nombre)"' | sangrar

paso "Verificando en la base"
psql_demo <<'SQL'
SELECT tipo, to_char(periodo, 'YYYY-MM-DD') AS periodo, prioridad
  FROM inteligencia.aviso WHERE usuario_id = :'u' ORDER BY periodo, prioridad;
SELECT categoria, count(*) AS movimientos
  FROM inteligencia.movimiento_enriquecido WHERE usuario_id = :'u' GROUP BY 1 ORDER BY 1;
SQL

if [[ -n "$API_URL" ]]; then
  [[ "$API_URL" =~ ^https?://[^[:space:]]+$ ]] || error "API_URL debe empezar por http:// o https://"
  API_URL="${API_URL%/}"
  paso "Verificando contra la API"
  # El cuerpo del login viaja por stdin y el token por un archivo de configuración de curl en stdin:
  # ni la contraseña ni el token aparecen en la lista de procesos ni en la salida.
  respuesta="$(jq -n --arg c "$celular" --arg p "$DEMO_CONTRASENA" '{celular: $c, contrasena: $p}' |
    curl -sS --max-time 30 -X POST -H 'Content-Type: application/json' --data-binary @- "$API_URL/v1/auth/login")"
  token="$(jq -r '.data.access_token // empty' <<<"$respuesta")"
  [[ -n "$token" ]] || error "el login contra la API falló: $(jq -c '.error' <<<"$respuesta" 2>/dev/null || true)"
  info "login correcto"
  api_get() {
    curl -sS --max-time 30 -K - "$API_URL$1" <<<"header = \"Authorization: Bearer $token\""
  }
  info "saldo (centavos): $(api_get /v1/saldo | jq -r '.data.monto // "sin dato"')"
  mes_escenario="${DEMO_FECHA%-19}"
  api_get "/v1/inteligencia/resumen?mes=$mes_escenario" |
    jq -r '"resumen \(.data.mes): evaluable=\(.data.evaluable) avisos_emitidos=\(.data.avisos_emitidos) cupo=\(.data.cupo_avisos)"' | sangrar
  # Los avisos del escenario caen en dos períodos, así que se consulta cada mes por separado.
  meses="$(psql_demo -At <<<"SELECT DISTINCT to_char(periodo, 'YYYY-MM') FROM inteligencia.aviso WHERE usuario_id = :'u' ORDER BY 1;")"
  total=0
  while IFS= read -r mes; do
    [[ -n "$mes" ]] || continue
    info "avisos de $mes (tipo, periodo, accion_codigo):"
    lista="$(api_get "/v1/inteligencia/avisos?mes=$mes" | jq -r '.data[] | "\(.tipo)\t\(.periodo)\t\(.accion_codigo // "-")"')"
    while IFS= read -r fila; do
      [[ -n "$fila" ]] || continue
      info "  $fila"
      total=$((total + 1))
    done <<<"$lista"
  done <<<"$meses"
  info "avisos vigentes devueltos por la API: $total"
fi

paso "Listo"
info "Usuario: $DEMO_USUARIO"
info "Celular: $celular"
info "Estado del escenario en $ESTADO"
info "La contraseña no se imprime. Para quitar la cuenta usa demo-limpiar.sh."

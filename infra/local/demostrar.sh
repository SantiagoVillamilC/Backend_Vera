#!/usr/bin/env bash
# infra/local/demostrar.sh
# Compila los siete comandos y demuestra el backend completo sobre una base desechable: migraciones,
# semilla, lote con léxico v2 y clasificador TF-IDF, avisos, API, /metrics y una lectura autenticada.
# No toca ninguna base real: el sembrador exige loopback, el puerto 55437 y el prefijo vera_semilla_.
#
# Uso: ./infra/local/demostrar.sh
# Variables: USUARIOS, MESES, EXTRA, LOTES, PUERTO_PG, PUERTO_REDIS, PUERTO_API y DEJAR=si.
set -euo pipefail

RAIZ="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$RAIZ"

PUERTO_PG="${PUERTO_PG:-55437}"
PUERTO_REDIS="${PUERTO_REDIS:-6380}"
PUERTO_API="${PUERTO_API:-8080}"
BASE="http://127.0.0.1:${PUERTO_API}"
NOMBRE_PG="vera-demo-pg"
NOMBRE_REDIS="vera-demo-redis"
BASE_DATOS="vera_semilla_demo"
IMAGEN_PG="${IMAGEN_PG:-postgres:16.15-alpine3.24}"
IMAGEN_REDIS="${IMAGEN_REDIS:-redis:7.4.1-alpine3.20}"
USUARIOS="${USUARIOS:-1200}"
MESES="${MESES:-10}"
EXTRA="${EXTRA:-1200}"
LOTES="${LOTES:-2}"
DEJAR="${DEJAR:-no}"
SALIDA="$RAIZ/out/demo"
BIN="$RAIZ/out/bin"
API_PID=""

paso() { printf '\n==> %s\n' "$1"; }
info() { printf '    %s\n' "$1"; }
error() { printf '[error] %s\n' "$1" >&2; exit 1; }

limpiar() {
  if [[ -n "$API_PID" ]]; then
    kill "$API_PID" 2>/dev/null || true
    wait "$API_PID" 2>/dev/null || true
  fi
  if [[ "$DEJAR" != "si" ]]; then
    docker rm -f "$NOMBRE_PG" "$NOMBRE_REDIS" >/dev/null 2>&1 || true
  fi
}
trap limpiar EXIT

for herramienta in go docker make python3; do
  command -v "$herramienta" >/dev/null || error "falta $herramienta"
done

mkdir -p "$SALIDA"
if docker ps -a --format '{{.Names}}' | grep -qx "$NOMBRE_PG" ||
  docker ps -a --format '{{.Names}}' | grep -qx "$NOMBRE_REDIS"; then
  error "ya existen $NOMBRE_PG o $NOMBRE_REDIS: corre make limpiar"
fi

paso "Compilando los siete comandos"
make --no-print-directory build

paso "Levantando Postgres y Redis desechables"
docker run -d --rm --name "$NOMBRE_PG" \
  -e POSTGRES_USER=dueno -e POSTGRES_PASSWORD=clave -e POSTGRES_DB="$BASE_DATOS" \
  -p "127.0.0.1:${PUERTO_PG}:5432" "$IMAGEN_PG" >/dev/null
docker run -d --rm --name "$NOMBRE_REDIS" \
  -p "127.0.0.1:${PUERTO_REDIS}:6379" "$IMAGEN_REDIS" >/dev/null
# pg_isready puede responder contra el servidor temporal que la imagen usa para inicializar y
# después cerrarse, así que la señal de listo es una consulta real.
listo="no"
for _ in $(seq 1 60); do
  if docker exec "$NOMBRE_PG" psql -U dueno -d "$BASE_DATOS" -c "select 1" >/dev/null 2>&1; then
    listo="si"
    break
  fi
  sleep 1
done
[[ "$listo" == "si" ]] || error "Postgres no aceptó una consulta en 60 s"
docker exec "$NOMBRE_REDIS" redis-cli ping 2>/dev/null | grep -q PONG ||
  error "Redis no respondió"
info "Postgres en 127.0.0.1:${PUERTO_PG} y Redis en 127.0.0.1:${PUERTO_REDIS}"

set -a
# shellcheck disable=SC1091
source "$RAIZ/cargas/inteligencia/entorno.sh"
set +a
export DATABASE_URL="postgres://dueno:clave@127.0.0.1:${PUERTO_PG}/${BASE_DATOS}?sslmode=disable"
export APP_DATABASE_URL="postgres://rol_api:clave_api@127.0.0.1:${PUERTO_PG}/${BASE_DATOS}?sslmode=disable"
export REDIS_URL="redis://127.0.0.1:${PUERTO_REDIS}/0"
export PORT="$PUERTO_API"
export METRICAS_ACTIVO=true
export AVISOS_ACTIVO=true
export CLASIFICADOR=tfidf
export LEXICO=v2
export ENRIQUECIMIENTO_INTERVAL=24h

paso "Aplicando migraciones"
"$BIN/migrate" up >/dev/null
docker exec "$NOMBRE_PG" psql -U dueno -d "$BASE_DATOS" \
  -c "ALTER ROLE rol_api WITH PASSWORD 'clave_api';" >/dev/null
info "esquema al día y rol_api con su clave de demo"

paso "Sembrando ${USUARIOS} usuarios y ${MESES} meses"
"$BIN/semilla-inteligencia" -usuarios="$USUARIOS" -meses="$MESES" -asientos-extra="$EXTRA" \
  -tokens="$SALIDA/tokens.json" >"$SALIDA/semilla.json"
python3 - "$SALIDA/semilla.json" <<'PY'
import json
import sys

informe = json.load(open(sys.argv[1]))
print(f"    {informe['Asientos']} asientos, {informe['Lineas']} líneas, "
      f"{informe['Cuentas']} cuentas, huella {informe['HuellaDelLedger'][:16]}")
PY

paso "Enriqueciendo ${LOTES} lotes de 5000 con LEXICO=v2 y CLASIFICADOR=tfidf"
for _ in $(seq 1 "$LOTES"); do
  "$BIN/enriquecer" lote 5000 2>>"$SALIDA/enriquecer.log" ||
    error "el lote falló, mira $SALIDA/enriquecer.log"
done
info "lotes terminados, log en $SALIDA/enriquecer.log"

paso "Emitiendo avisos una vez"
"$BIN/avisos" -ahora="$(date -Iseconds)" >"$SALIDA/avisos.json" || error "avisos falló"
python3 - "$SALIDA/avisos.json" <<'PY'
import json
import sys

informe = json.load(open(sys.argv[1]))
print(f"    {informe['emitidos']} avisos en {informe['duracion_ms']} ms")
PY

paso "Arrancando la API en ${BASE}"
"$BIN/api" >"$SALIDA/api.log" 2>&1 &
API_PID=$!
python3 - "$BASE" <<'PY'
import sys
import time
import urllib.request

base = sys.argv[1]
for _ in range(60):
    try:
        with urllib.request.urlopen(base + "/healthz", timeout=2) as respuesta:
            if respuesta.status == 200:
                print("    /healthz 200")
                break
    except Exception:
        time.sleep(1)
else:
    raise SystemExit("la API no respondió en /healthz")
PY

paso "Comprobando /metrics y una lectura autenticada"
python3 - "$BASE" "$METRICAS_TOKEN" "$SALIDA/tokens.json" <<'PY'
import json
import sys
import urllib.request

base, token_metricas, ruta_tokens = sys.argv[1], sys.argv[2], sys.argv[3]


def pedir(ruta, cabeceras):
    peticion = urllib.request.Request(base + ruta, headers=cabeceras)
    with urllib.request.urlopen(peticion, timeout=5) as respuesta:
        return respuesta.status, respuesta.read()


estado, cuerpo = pedir("/metrics", {"Authorization": f"Bearer {token_metricas}"})
series = [linea for linea in cuerpo.decode().splitlines() if linea and not linea.startswith("#")]
print(f"    /metrics {estado}, {len(series)} series")
if estado != 200 or not any("vera_inteligencia" in linea for linea in series):
    raise SystemExit("metrics no trae las familias del contexto")

tokens = json.load(open(ruta_tokens))
cabeceras = {"Authorization": f"Bearer {tokens[0]['token']}", "X-Real-IP": "10.20.0.1"}

for ruta in ("/v1/inteligencia/movimientos?limit=5", "/v1/inteligencia/resumen"):
    estado, cuerpo = pedir(ruta, cabeceras)
    print(f"    {ruta} {estado}, {len(cuerpo)} bytes")
    if estado != 200:
        raise SystemExit(f"{ruta} no respondió 200")

for token in tokens[:120]:
    cabeceras = {"Authorization": f"Bearer {token['token']}", "X-Real-IP": "10.20.0.1"}
    estado, cuerpo = pedir("/v1/inteligencia/avisos", cabeceras)
    if estado != 200:
        raise SystemExit("/v1/inteligencia/avisos no respondió 200")
    tarjetas = len(json.loads(cuerpo).get("data") or [])
    if tarjetas:
        print(f"    /v1/inteligencia/avisos 200, {tarjetas} tarjetas de {token['usuario'][:8]}")
        break
else:
    print("    /v1/inteligencia/avisos 200, sin tarjetas en los primeros 120 usuarios")
PY

paso "Conteos en la base"
for consulta in \
  "asientos|select count(*) from nucleo.journal_entry" \
  "líneas|select count(*) from nucleo.posting" \
  "movimientos|select count(*) from inteligencia.movimiento_enriquecido" \
  "avisos|select count(*) from inteligencia.aviso"; do
  etiqueta="${consulta%%|*}"
  sql="${consulta#*|}"
  printf '    %-12s %s\n' "$etiqueta" \
    "$(docker exec "$NOMBRE_PG" psql -U dueno -d "$BASE_DATOS" -At -c "$sql")"
done

paso "Midiendo el primer corte con el arnés"
"$BIN/evaluar-clasificador" -csv ../dataset-inteligencia/oro/v2/etiquetas-v2-evaluacion.csv \
  -conjunto ../dataset-inteligencia/oro/v1/conjunto.csv -modo todos \
  -lexico ../dataset-inteligencia/lexico/v2/lexico_v2.csv \
  -salida "$SALIDA/evaluacion.json" >/dev/null 2>"$SALIDA/evaluacion.txt"
tail -2 "$SALIDA/evaluacion.txt" | sed 's/^/    /'

paso "Demo completo"
info "informes en $SALIDA"
if [[ "$DEJAR" == "si" ]]; then
  info "DEJAR=si: la API sigue en ${BASE} (pid ${API_PID}) y los contenedores quedan arriba"
  info "para limpiar: make limpiar"
  API_PID=""
else
  info "los contenedores y la API se apagan al salir"
fi

#!/usr/bin/env bash
# infra/local/levantar-base.sh
# Deja el backend corriendo en una máquina nueva: levanta Postgres 16 y Redis 7 en contenedores
# desechables, aplica las migraciones, pone clave al rol_api y, si se pide, siembra datos sintéticos.
# No toca ninguna base real: el sembrador exige loopback, el puerto 55437 y el prefijo vera_semilla_.
#
# Uso: ./infra/local/levantar-base.sh [--semilla[=N]] [--api] [--limpiar] [--ayuda]
set -euo pipefail

RAIZ="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$RAIZ"

PUERTO_PG="${PUERTO_PG:-55437}"
PUERTO_REDIS="${PUERTO_REDIS:-6380}"
BASE_DATOS="${BASE_DATOS:-vera_semilla_m8}"
NOMBRE_PG="${NOMBRE_PG:-vera-demo-pg}"
NOMBRE_REDIS="${NOMBRE_REDIS:-vera-demo-redis}"
IMAGEN_PG="${IMAGEN_PG:-postgres:16.15-alpine3.24}"
IMAGEN_REDIS="${IMAGEN_REDIS:-redis:7.4.1-alpine3.20}"
CLAVE_DUENO="${CLAVE_DUENO:-clave}"
CLAVE_API="${CLAVE_API:-clave_api}"
USUARIOS_SEMILLA="${USUARIOS_SEMILLA:-200}"
BIN="$RAIZ/out/bin"
SALIDA="$RAIZ/out/local"
SEMBRAR="no"
CON_API="no"

paso() { printf '\n==> %s\n' "$1"; }
info() { printf '    %s\n' "$1"; }
error() { printf '[error] %s\n' "$1" >&2; exit 1; }

mostrar_ayuda() {
  cat <<'EOF'
Uso: levantar-base.sh [opción]

  --semilla[=N]  Siembra N usuarios sintéticos (por defecto 200) y enriquece un lote.
  --api          Arranca la API en primer plano al final de la preparación.
  --limpiar      Borra los contenedores vera-demo-pg y vera-demo-redis (y sus volúmenes anónimos) y sale.
  --ayuda        Muestra esta ayuda.

Sin opciones deja la base lista y los contenedores arriba, sin arrancar la API.

Variables: PUERTO_PG, PUERTO_REDIS, BASE_DATOS, NOMBRE_PG, NOMBRE_REDIS, IMAGEN_PG,
IMAGEN_REDIS, CLAVE_DUENO, CLAVE_API y USUARIOS_SEMILLA.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --semilla) SEMBRAR="si"; shift ;;
    --semilla=*) SEMBRAR="si"; USUARIOS_SEMILLA="${1#*=}"; shift ;;
    --api) CON_API="si"; shift ;;
    --limpiar) LIMPIAR="si"; shift ;;
    --ayuda | -h | --help) mostrar_ayuda; exit 0 ;;
    *) error "opción desconocida: $1 (usa --ayuda)" ;;
  esac
done

if [[ "${LIMPIAR:-no}" == "si" ]]; then
  # -v borra también los volúmenes anónimos de estos dos contenedores y nada más.
  docker rm -f -v "$NOMBRE_PG" "$NOMBRE_REDIS" >/dev/null 2>&1 || true
  info "contenedores $NOMBRE_PG y $NOMBRE_REDIS borrados"
  exit 0
fi

for herramienta in docker go make; do
  command -v "$herramienta" >/dev/null || error "falta $herramienta"
done
[[ "$SEMBRAR" == "no" ]] || command -v python3 >/dev/null || error "falta python3 (lo usa --semilla para leer el token)"
docker info >/dev/null 2>&1 || error "el demonio de Docker no responde: arráncalo y vuelve a intentar"

[[ -f "$RAIZ/cargas/inteligencia/entorno.sh" ]] ||
  error "falta cargas/inteligencia/entorno.sh: de ahí salen los secretos de sandbox del bloque de auth"

# Fuente única de los secretos de sandbox. Después se reescriben los DSN con los puertos de este script.
set -a
# shellcheck disable=SC1091
source "$RAIZ/cargas/inteligencia/entorno.sh"
set +a
export DATABASE_URL="postgres://dueno:${CLAVE_DUENO}@127.0.0.1:${PUERTO_PG}/${BASE_DATOS}?sslmode=disable"
export APP_DATABASE_URL="postgres://rol_api:${CLAVE_API}@127.0.0.1:${PUERTO_PG}/${BASE_DATOS}?sslmode=disable"
export REDIS_URL="redis://127.0.0.1:${PUERTO_REDIS}/0"

existe_contenedor() { docker ps -a --format '{{.Names}}' | grep -qx "$1"; }
esta_corriendo() { [[ "$(docker inspect -f '{{.State.Running}}' "$1" 2>/dev/null)" == "true" ]]; }

paso "Levantando Postgres y Redis en contenedores desechables"
if existe_contenedor "$NOMBRE_PG"; then
  esta_corriendo "$NOMBRE_PG" || docker start "$NOMBRE_PG" >/dev/null
  info "reutilizo $NOMBRE_PG (ya existía)"
else
  docker run -d --memory 512m --cpus 0.5 --name "$NOMBRE_PG" \
    -e POSTGRES_USER=dueno -e POSTGRES_PASSWORD="$CLAVE_DUENO" -e POSTGRES_DB="$BASE_DATOS" \
    -p "127.0.0.1:${PUERTO_PG}:5432" "$IMAGEN_PG" >/dev/null
  info "$NOMBRE_PG nuevo en 127.0.0.1:${PUERTO_PG}"
fi

if existe_contenedor "$NOMBRE_REDIS"; then
  esta_corriendo "$NOMBRE_REDIS" || docker start "$NOMBRE_REDIS" >/dev/null
  info "reutilizo $NOMBRE_REDIS (ya existía)"
else
  docker run -d --memory 256m --cpus 0.2 --name "$NOMBRE_REDIS" \
    -p "127.0.0.1:${PUERTO_REDIS}:6379" "$IMAGEN_REDIS" >/dev/null
  info "$NOMBRE_REDIS nuevo en 127.0.0.1:${PUERTO_REDIS}"
fi

paso "Esperando a que Postgres acepte consultas"
# La imagen arranca un servidor temporal durante la inicialización y lo cierra enseguida, así que una
# sola consulta exitosa no basta: se exigen dos seguidas separadas por dos segundos.
seguidas=0
for _ in $(seq 1 60); do
  if docker exec "$NOMBRE_PG" psql -U dueno -d postgres -c "select 1" >/dev/null 2>&1; then
    seguidas=$((seguidas + 1))
    [[ "$seguidas" -ge 2 ]] && break
    sleep 2
  else
    seguidas=0
    sleep 1
  fi
done
[[ "$seguidas" -ge 2 ]] || error "Postgres no aceptó una consulta en 60 s; mira: docker logs $NOMBRE_PG"

# La base pedida puede faltar si el contenedor lo creó otro script con otro POSTGRES_DB.
existe="$(docker exec "$NOMBRE_PG" psql -U dueno -d postgres -Atc \
  "select 1 from pg_database where datname = '${BASE_DATOS}'")"
if [[ "$existe" != "1" ]]; then
  info "la base $BASE_DATOS no existe en $NOMBRE_PG, la creo"
  docker exec "$NOMBRE_PG" psql -U dueno -d postgres -Atc "create database ${BASE_DATOS}" >/dev/null
fi
docker exec "$NOMBRE_PG" psql -U dueno -d "$BASE_DATOS" -Atc "select 1" >/dev/null ||
  error "no puedo consultar la base $BASE_DATOS"
info "Postgres en 127.0.0.1:${PUERTO_PG}/${BASE_DATOS}"

for _ in $(seq 1 30); do
  if docker exec "$NOMBRE_REDIS" redis-cli ping 2>/dev/null | grep -q PONG; then
    info "Redis en 127.0.0.1:${PUERTO_REDIS}"
    break
  fi
  sleep 1
done
docker exec "$NOMBRE_REDIS" redis-cli ping 2>/dev/null | grep -q PONG ||
  error "Redis no respondió; mira: docker logs $NOMBRE_REDIS"

paso "Compilando los binarios en out/bin"
make --no-print-directory build

paso "Aplicando migraciones (esquema del núcleo, River y auth)"
"$BIN/migrate" up >/dev/null
info "esquema al día"

paso "Poniendo clave al rol_api"
# migrate crea rol_api sin contraseña; sin esto APP_DATABASE_URL no puede autenticarse.
docker exec "$NOMBRE_PG" psql -U dueno -d "$BASE_DATOS" \
  -c "ALTER ROLE rol_api WITH PASSWORD '${CLAVE_API}';" >/dev/null
info "rol_api listo"

if [[ "$SEMBRAR" == "si" ]]; then
  mkdir -p "$SALIDA"
  paso "Sembrando ${USUARIOS_SEMILLA} usuarios sintéticos"
  "$BIN/semilla-inteligencia" -usuarios="$USUARIOS_SEMILLA" -meses=10 \
    -asientos-extra="$USUARIOS_SEMILLA" -tokens="$SALIDA/tokens.json" >"$SALIDA/semilla.json"
  info "tokens con access reales en $SALIDA/tokens.json"

  paso "Enriqueciendo un lote con LEXICO=v2 y CLASIFICADOR=tfidf"
  CLASIFICADOR=tfidf LEXICO=v2 "$BIN/enriquecer" lote 5000 >/dev/null 2>"$SALIDA/enriquecer.log"
  info "lote enriquecido, log en $SALIDA/enriquecer.log"
fi

paso "Base lista"
info "exporta el entorno en tu shell con: source cargas/inteligencia/entorno.sh"
info "y arranca la API con:               out/bin/api          (escucha en http://127.0.0.1:${PORT:-8080})"
if [[ "$SEMBRAR" == "si" ]]; then
  info "prueba una lectura autenticada:"
  token="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))[0]["token"])' "$SALIDA/tokens.json")"
  info "    curl -s -H \"Authorization: Bearer ${token}\" -H 'X-Real-IP: 10.20.0.1' \\"
  info "      http://127.0.0.1:${PORT:-8080}/v1/inteligencia/resumen"
else
  info "sin datos: para ver movimientos usa --semilla (tarda un par de minutos)"
fi
info "para borrar los contenedores:       $0 --limpiar"

if [[ "$CON_API" == "si" ]]; then
  paso "Arrancando la API en primer plano (Ctrl+C para parar)"
  exec "$BIN/api"
fi

#!/usr/bin/env bash
# infra/local/sembrar-demo-prod.sh
# Siembra y enriquece la base de demo que usa la API desplegada. Se corre desde el portátil con
# el túnel SSH abierto contra 127.0.0.1:55437. No escribe en ninguna base que no sea
# vera_semilla_ en loopback, 55437 y el prefijo de la guarda.
#
# Uso:
#   export DATABASE_URL='postgres://dueno:CLAVE@127.0.0.1:55437/vera_semilla_demo?sslmode=disable'
#   export APP_DATABASE_URL='postgres://rol_api:CLAVE_API@127.0.0.1:55437/vera_semilla_demo?sslmode=disable'
#   ./infra/local/sembrar-demo-prod.sh
#   SEMBRAR=no DEMO_USUARIO=<uuid> ./infra/local/sembrar-demo-prod.sh
#   FORZAR=si ./infra/local/sembrar-demo-prod.sh   # solo si la base ya está sembrada a propósito
#
# El estado de la corrida queda en ../DEMO-PROD-STATE.json para que un agente o una persona
# pueda retomarla sin adivinar qué se sembró ni qué UUID se eligió.
set -euo pipefail

RAIZ="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$RAIZ"

DATABASE_URL="${DATABASE_URL:-}"
APP_DATABASE_URL="${APP_DATABASE_URL:-}"
USUARIOS="${USUARIOS:-1200}"
MESES="${MESES:-10}"
EXTRA="${EXTRA:-1200}"
LOTES="${LOTES:-8}"
SEMBRAR="${SEMBRAR:-si}"
FORZAR="${FORZAR:-no}"
DEMO_USUARIO="${DEMO_USUARIO:-}"
DEMO_CELULAR="${DEMO_CELULAR:-+573001234567}"
DEMO_CONTRASENA="${DEMO_CONTRASENA:-Vera-Demo-2026}"
DEMO_NOMBRE="${DEMO_NOMBRE:-Camila Rodriguez}"
ESTADO="${ESTADO:-$RAIZ/../DEMO-PROD-STATE.json}"

paso() { printf '\n==> %s\n' "$1"; }
info() { printf '    %s\n' "$1"; }
error() { printf '[error] %s\n' "$1" >&2; exit 1; }

[[ -n "$DATABASE_URL" ]] || error "exporta DATABASE_URL del túnel"
[[ -n "$APP_DATABASE_URL" ]] || error "exporta APP_DATABASE_URL del túnel"
[[ "$DATABASE_URL" == *"@127.0.0.1:55437/vera_semilla_"* ]] ||
  error "DATABASE_URL debe apuntar a 127.0.0.1:55437/vera_semilla_ (túnel SSH)"
[[ "$APP_DATABASE_URL" == *"@127.0.0.1:55437/vera_semilla_"* ]] ||
  error "APP_DATABASE_URL debe apuntar a 127.0.0.1:55437/vera_semilla_ (túnel SSH)"
command -v go >/dev/null || error "falta go"
command -v make >/dev/null || error "falta make"
command -v python3 >/dev/null || error "falta python3"

export DATABASE_URL APP_DATABASE_URL

paso "Compilando los binarios"
make --no-print-directory build
go build -o out/bin/login-demo ./cmd/login-demo

if [[ "$SEMBRAR" == "si" ]]; then
  if [[ "$FORZAR" != "si" ]] && command -v psql >/dev/null; then
    existentes="$(psql "$DATABASE_URL" -At -c "select count(*) from nucleo.journal_entry;")"
    [[ "$existentes" == "0" ]] ||
      error "la base ya tiene $existentes asientos. Usa SEMBRAR=no o FORZAR=si."
  fi
  paso "Sembrando ${USUARIOS} usuarios y ${MESES} meses"
  out/bin/semilla-inteligencia -usuarios="$USUARIOS" -meses="$MESES" -asientos-extra="$EXTRA"

  paso "Enriqueciendo ${LOTES} lotes de 5000"
  for _ in $(seq 1 "$LOTES"); do
    LEXICO=v2 CLASIFICADOR=tfidf out/bin/enriquecer lote 5000
  done

  paso "Verificando que no queden asientos pendientes"
  salida="$(LEXICO=v2 CLASIFICADOR=tfidf out/bin/enriquecer lote 5000 2>&1)"
  printf '%s\n' "$salida" | tail -1
  printf '%s\n' "$salida" | grep -q 'asientos=0' ||
    error "quedan asientos pendientes: corre otro lote antes de seguir"
fi

paso "Candidatos para el usuario de la demo"
candidatos=""
if command -v psql >/dev/null; then
  candidatos="$(psql "$DATABASE_URL" -v ON_ERROR_STOP=1 <<'SQL'
WITH candidatos AS (
  SELECT usuario_id,
         count(*) AS movimientos,
         count(*) FILTER (WHERE categoria = 'sin_categoria') AS sin_categoria
    FROM inteligencia.movimiento_enriquecido
   GROUP BY usuario_id
  HAVING count(*) FILTER (WHERE categoria = 'sin_categoria') > 0
)
SELECT c.usuario_id, c.movimientos, c.sin_categoria, m.comercio_nombre, m.veces
  FROM candidatos c
  JOIN (SELECT usuario_id, comercio_nombre, count(*) AS veces
          FROM inteligencia.movimiento_enriquecido
         WHERE comercio_nombre IS NOT NULL
         GROUP BY 1, 2
        HAVING count(*) >= 3) m ON m.usuario_id = c.usuario_id
 ORDER BY c.sin_categoria DESC, m.veces DESC
 LIMIT 5;
SQL
)"
  printf '%s\n' "$candidatos"
else
  info "psql no está instalado. Corre la consulta de la sección 4.2 de DEMO-PROD-2026-10-22.md."
fi

credencial_id=""
if [[ -n "$DEMO_USUARIO" ]]; then
  [[ "$DEMO_USUARIO" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$ ]] ||
    error "DEMO_USUARIO no es un UUID"
  if command -v psql >/dev/null; then
    total="$(psql "$DATABASE_URL" -At -c \
      "select count(*) from inteligencia.movimiento_enriquecido where usuario_id = '$DEMO_USUARIO';")"
    [[ "$total" != "0" ]] ||
      error "el UUID no tiene movimientos enriquecidos: elige uno de la lista o siembra sus datos"
  fi
  paso "Creando la cuenta de la demo"
  salida_login="$(out/bin/login-demo \
    -usuario "$DEMO_USUARIO" \
    -celular "$DEMO_CELULAR" \
    -contrasena "$DEMO_CONTRASENA" \
    -nombre "$DEMO_NOMBRE")"
  printf '%s\n' "$salida_login"
  credencial_id="$(printf '%s\n' "$salida_login" | python3 -c \
    'import json,sys; print(json.load(sys.stdin).get("credencial_id", ""))' 2>/dev/null || true)"
else
  info "Elige el UUID de la lista y vuelve a correr con:"
  info "SEMBRAR=no DEMO_USUARIO=<uuid> ./infra/local/sembrar-demo-prod.sh"
fi

paso "Conteos de la base de demo"
conteos=""
if command -v psql >/dev/null; then
  conteos="$(psql "$DATABASE_URL" -At -F ' ' -c "
select 'asientos', count(*) from nucleo.journal_entry
union all select 'lineas', count(*) from nucleo.posting
union all select 'movimientos', count(*) from inteligencia.movimiento_enriquecido
union all select 'avisos', count(*) from inteligencia.aviso;")"
  printf '%s\n' "$conteos" | sed 's/^/    /'
else
  info "psql no está instalado: los conteos quedan en la sección 7 del archivo de ejecución."
fi

python3 - "$ESTADO" "$USUARIOS" "$MESES" "$EXTRA" "$LOTES" "$DEMO_USUARIO" "$DEMO_CELULAR" "$DEMO_NOMBRE" "$credencial_id" "$candidatos" "$conteos" <<'PY'
import datetime
import json
import sys

(estado, usuarios, meses, extra, lotes, usuario, celular, nombre, credencial, candidatos, conteos) = sys.argv[1:]
datos = {
    "actualizado_en": datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds"),
    "base": "vera_semilla_demo",
    "siembra": {
        "usuarios": int(usuarios),
        "meses": int(meses),
        "asientos_extra": int(extra),
        "lotes": int(lotes),
    },
    "candidatos_texto": candidatos,
    "uuid": usuario or None,
    "celular": celular,
    "nombre": nombre,
    "credencial_id": credencial or None,
    "conteos_texto": conteos,
    "estado": "cuenta_creada" if usuario and credencial else "sembrado",
    "login_api": None,
    "apk": None,
    "nota": "El login contra la API y el APK se anotan a mano o los actualiza el agente.",
}
with open(estado, "w", encoding="utf-8") as archivo:
    json.dump(datos, archivo, ensure_ascii=False, indent=2, sort_keys=True)
    archivo.write("\n")
PY
info "Estado para agentes en $ESTADO"

paso "Listo"
info "Anota el UUID, el id de la credencial y los conteos en DEMO-PROD-2026-10-22.md."
info "La contraseña no se imprime. La cuenta queda activa hasta la limpieza de la sección 9."

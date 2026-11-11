#!/usr/bin/env bash
# infra/local/evaluar-asistente-local.sh
# Corre el conjunto congelado de evaluación contra un modelo de Ollama autoalojado y guarda el
# informe JSON. No pide clave ni nivel gratuito: el proveedor atiende por loopback y no factura
# por solicitud. El SHA del conjunto no cambia: la selección por familia o ids sólo acota muestras.
#
# Uso: ./infra/local/evaluar-asistente-local.sh [ruta-del-informe.json]
# Variables:
#   MODELO           nombre del modelo en Ollama                      (gemma4-e2b-local)
#   FAMILIAS         familias separadas por coma, vacío = conjunto completo
#   IDS              ids de caso separados por coma, se suman a FAMILIAS
#   REPETICIONES     repeticiones por caso                            (3)
#   MAX_SOLICITUDES  tope de solicitudes de esta invocación            (300)
#   URL_OLLAMA       origen del servidor                             (http://127.0.0.1:11434)
#   TIMEOUT          plazo por intento                               (45s)
#   TEMPERATURA      temperatura del muestreo                        (0)
#   SALIDA           ruta del informe                                (out/evaluacion-local/<modelo>.json)
#   MODELOS_GEMMA    carpeta con los Modelfile y los GGUF             (../modelos-gemma)
set -euo pipefail

RAIZ="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$RAIZ"

MODELO="${MODELO:-gemma4-e2b-local}"
FAMILIAS="${FAMILIAS:-}"
IDS="${IDS:-}"
REPETICIONES="${REPETICIONES:-3}"
MAX_SOLICITUDES="${MAX_SOLICITUDES:-300}"
URL_OLLAMA="${URL_OLLAMA:-http://127.0.0.1:11434}"
TIMEOUT="${TIMEOUT:-45s}"
TEMPERATURA="${TEMPERATURA:-0}"
SALIDA="${1:-${SALIDA:-out/evaluacion-local/${MODELO}.json}}"
MODELOS_GEMMA="${MODELOS_GEMMA:-$(cd "$RAIZ/.." 2>/dev/null && pwd)/modelos-gemma}"

paso() { printf '\n==> %s\n' "$1"; }
info() { printf '    %s\n' "$1"; }
error() { printf '[error] %s\n' "$1" >&2; exit 1; }

paso "Comprobando el servidor de Ollama en $URL_OLLAMA"
python3 - "$URL_OLLAMA" <<'PY' || error "arranca 'ollama serve' antes de evaluar"
import json, sys, urllib.request
url = sys.argv[1].rstrip('/')
try:
    with urllib.request.urlopen(url + '/api/tags', timeout=5) as r:
        etiquetas = json.load(r)
except Exception as exc:
    print(f'    sin respuesta: {exc}', file=sys.stderr)
    sys.exit(1)
print('    modelos disponibles:', ', '.join(m['name'] for m in etiquetas.get('models', [])) or 'ninguno')
PY

if ! ollama list | awk -v m="$MODELO" '{sub(/:latest$/, "", $1); if ($1 == m) encontrado = 1} END {exit !encontrado}'; then
  MODELFILE=""
  case "$MODELO" in
    gemma4-e2b-local) MODELFILE="$MODELOS_GEMMA/Modelfile-e2b" ;;
    gemma4-e4b-local) MODELFILE="$MODELOS_GEMMA/Modelfile-e4b" ;;
  esac
  if [[ -n "$MODELFILE" && -f "$MODELFILE" ]]; then
    paso "Creando $MODELO desde $MODELFILE"
    ( cd "$MODELOS_GEMMA" && ollama create "$MODELO" -f "$(basename "$MODELFILE")" )
  else
    error "el modelo $MODELO no existe en Ollama y no hay Modelfile conocido"
  fi
fi

mkdir -p "$(dirname "$SALIDA")"
paso "Evaluando $MODELO con $REPETICIONES repeticiones"
[[ -n "$FAMILIAS" ]] && info "familias: $FAMILIAS"
[[ -n "$IDS" ]] && info "ids: $IDS"
info "informe: $SALIDA"

ARGUMENTOS=(-proveedor local -repeticiones "$REPETICIONES" -max-solicitudes "$MAX_SOLICITUDES" -url-ollama "$URL_OLLAMA" -timeout-llamada "$TIMEOUT" -temperatura "$TEMPERATURA")
[[ -n "$FAMILIAS" ]] && ARGUMENTOS+=(-familias "$FAMILIAS")
[[ -n "$IDS" ]] && ARGUMENTOS+=(-ids "$IDS")

set +e
VERA_MODELO="$MODELO" go run ./cmd/evaluar-vera "${ARGUMENTOS[@]}" >"$SALIDA"
CODIGO=$?
set -e
info "código del evaluador: $CODIGO (0 acepta, 1 no acepta, 2 entrada inválida, 3 parcial)"

paso "Resumen"
python3 - "$SALIDA" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
m, a = d['metricas'], d.get('aceptacion_local') or {}
print(f"    modelo            {d['config']['modelo']}")
print(f"    conjunto          {d['version_casos']} sha256={d['casos_sha256'][:16]}…")
print(f"    casos             {len(d['casos_incluidos'])} de {d.get('casos_aptos', 0)} aptos, {d['total_esperado']} muestras esperadas")
print(f"    completo/aprobado {d['completo']} / {d['aprobado']}")
print(f"    función/args      {m['acierto_funcion']:.3f} / {m['acierto_argumentos']:.3f}")
print(f"    respuestas        {m['respuestas_correctas']:.3f}")
print(f"    mediana / p95     {m['latencia_mediana_ms']} ms / {m['latencia_p95_ms']} ms")
if a:
    print(f"    llamadas          {a['llamadas_correctas']} de {a['muestras_con_llamada']} (mínimo {a['acierto_minimo']:.2f})")
    print(f"    casos correctos   {a['casos_correctos']} de {a['casos_con_llamada']}")
    print(f"    dinero ejecutado  {a['ejecuciones_de_dinero']}")
    print(f"    aceptación        {'cumple' if a['cumple'] else 'no cumple'}")
    for motivo in a['motivos']:
        print(f"      - {motivo}")
PY

exit "$CODIGO"

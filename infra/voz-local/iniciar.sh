#!/bin/sh
set -eu
python /opt/voz/servidor.py &
voz_pid=$!
trap 'kill "$voz_pid" 2>/dev/null || true' EXIT
VERA_VOZ_PUERTO="${VERA_VOZ_PUERTO:-8766}" python - <<'PY'
import os, time, urllib.request
puerto = os.environ['VERA_VOZ_PUERTO']
for intento in range(90):
    try:
        req = urllib.request.Request(f'http://127.0.0.1:{puerto}/healthz', headers={'X-Vera-Token': os.environ['VERA_LAB_TOKEN']})
        with urllib.request.urlopen(req, timeout=1) as r:
            if r.status == 200:
                break
    except OSError:
        time.sleep(1)
else:
    raise SystemExit('No se pudo iniciar la voz neural local.')
PY
export VERA_VOZ_LOCAL_URL="http://127.0.0.1:${VERA_VOZ_PUERTO:-8766}"
exec "$@"

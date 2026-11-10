#!/usr/bin/env bash
# La validación estricta de DSN y el aislamiento por usuario viven en el comando.
set -euo pipefail
RAIZ="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$RAIZ"
: "${DATABASE_URL:?exporta DATABASE_URL de la base de demo}"
: "${DEMO_USUARIO:?exporta DEMO_USUARIO (UUID exclusivo del escenario)}"
export DATABASE_URL DEMO_USUARIO
exec go run ./cmd/semilla-escenario

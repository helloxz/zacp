#!/usr/bin/env bash
# Same as demo-api.sh: start Gin + reasonix --acp
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
exec "$ROOT/scripts/demo-api.sh" "$@"
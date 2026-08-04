#!/usr/bin/env bash
# Terminal REPL against reasonix --acp
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT/backend"

# Prefer a known-good binary if PATH has no reasonix
if [[ -z "${REASONIX_BIN:-}" ]]; then
  if command -v reasonix >/dev/null 2>&1; then
    export REASONIX_BIN="$(command -v reasonix)"
  elif [[ -x /home/xiaoz/.local/share/pnpm/global/5/.pnpm/@reasonix+cli-linux-x64@1.17.1-rc.1/node_modules/@reasonix/cli-linux-x64/bin/reasonix ]]; then
    export REASONIX_BIN=/home/xiaoz/.local/share/pnpm/global/5/.pnpm/@reasonix+cli-linux-x64@1.17.1-rc.1/node_modules/@reasonix/cli-linux-x64/bin/reasonix
  elif [[ -x /data/apps/znode/bin/reasonix ]]; then
    export REASONIX_BIN=/data/apps/znode/bin/reasonix
  fi
fi

echo "REASONIX_BIN=${REASONIX_BIN:-reasonix}"
exec go run ./cmd/chat "$@"

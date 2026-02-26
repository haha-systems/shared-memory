#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
CONFIG_PATH="${CONFIG_PATH:-$ROOT_DIR/config/memory-mcp.yaml}"
LOCAL_BIN="$ROOT_DIR/.bin/memory-mcp"

if [ $# -gt 0 ]; then
  if [ "$1" = "--config" ] && [ $# -ge 2 ]; then
    CONFIG_PATH="$2"
    shift 2
  fi
fi

if [ -x "$LOCAL_BIN" ]; then
  exec "$LOCAL_BIN" serve --config "$CONFIG_PATH" "$@"
fi

if command -v memory-mcp >/dev/null 2>&1; then
  exec memory-mcp serve --config "$CONFIG_PATH" "$@"
fi

# Optional fallback for development. Disabled by default to avoid MCP startup timeouts
# caused by first-run compilation.
if [ "${GO_RUN_FALLBACK:-0}" = "1" ]; then
  exec go run "$ROOT_DIR/cmd/memory-mcp" serve --config "$CONFIG_PATH" "$@"
fi

echo "memory-mcp launcher not found. Run ./scripts/bootstrap-clis.sh to build .bin/memory-mcp." >&2
exit 1

#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
CONFIG_PATH="${CONFIG_PATH:-$ROOT_DIR/config/memory-mcp.yaml}"
SCOPE="${SCOPE:-user}"
LOCAL_BIN="$ROOT_DIR/.bin/memory-mcp"

# Ensure we have a fast local binary so MCP startup does not depend on `go run`.
mkdir -p "$ROOT_DIR/.bin"
go build -o "$LOCAL_BIN" "$ROOT_DIR/cmd/memory-mcp"

if command -v memory-mcp >/dev/null 2>&1; then
  exec memory-mcp bootstrap-clis \
    --config "$CONFIG_PATH" \
    --all \
    --scope "$SCOPE" \
    --serve-command "$ROOT_DIR/scripts/serve-stdio.sh" \
    "$@"
fi

# Fallback for development checkout before install.
exec go run "$ROOT_DIR/cmd/memory-mcp" bootstrap-clis \
  --config "$CONFIG_PATH" \
  --all \
  --scope "$SCOPE" \
  --serve-command "$ROOT_DIR/scripts/serve-stdio.sh" \
  "$@"

#!/usr/bin/env bash
# Claude Code PostToolUse hook: record file touches via configured TOML.
set -euo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$SCRIPT_DIR/on_edit_touch.py" "$@"

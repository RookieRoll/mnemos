#!/usr/bin/env bash
# Off-arm: clean baseline. No mnemos hooks, no MCP server, no memory.
#
#  - MNEMOS_DISABLED=1 makes every globally-installed mnemos hook (prewarm,
#    user-prompt, post-tool, etc.) no-op. Auth and other claude settings
#    stay intact because we don't reassign CLAUDE_CONFIG_DIR.
#  - --strict-mcp-config + mcp_off.json (empty) kills the MCP surface.
#  - Per-run scratch cwd to match the on-arm structurally.
#
# Paths come from common.sh; see there for the overrides.
set -euo pipefail

_here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
. "$_here/common.sh"

trigger="$1"
scratch_project off

MNEMOS_DISABLED=1 exec claude -p "$trigger" \
  --mcp-config "$MCP_OFF" \
  --strict-mcp-config \
  --dangerously-skip-permissions \
  --output-format stream-json \
  --verbose

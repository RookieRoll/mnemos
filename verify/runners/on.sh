#!/usr/bin/env bash
# On-arm: full mnemos surface.
#
#  - Per-run scratch cwd whose basename is "mnemos" so the SessionStart
#    prewarm hook scopes to the mnemos project (and surfaces
#    conventions/corrections/recent-sessions).
#  - Auto-search memory block from `mnemos hook user-prompt` injected via
#    --append-system-prompt. We call the hook ourselves because claude -p
#    does NOT fire UserPromptSubmit hooks (only SessionStart). Without this,
#    the on-arm only gets static prewarm — no per-prompt relevance.
#    This is also why the published capture numbers already include the
#    capture directive: it rides in on this same block.
#  - --strict-mcp-config + mcp_on.json keeps MCP isolated from the user's
#    other servers.
#
# Paths and the binary come from common.sh; see there for the overrides.
set -euo pipefail

_here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
. "$_here/common.sh"

trigger="$1"
scratch_project on

# Empty when nothing scores >= promptMemoryMinScore.
memory_block=$(mnemos_user_prompt_hook "$trigger")

if [[ -n "$memory_block" ]]; then
  exec claude -p "$trigger" \
    --append-system-prompt "$memory_block" \
    --mcp-config "$MCP_ON" \
    --strict-mcp-config \
    --dangerously-skip-permissions \
    --output-format stream-json \
    --verbose
else
  exec claude -p "$trigger" \
    --mcp-config "$MCP_ON" \
    --strict-mcp-config \
    --dangerously-skip-permissions \
    --output-format stream-json \
    --verbose
fi

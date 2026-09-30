#!/usr/bin/env bash
# On-arm: full mnemos surface.
#
#  - Per-run scratch cwd whose basename is "mnemos" so the global
#    SessionStart prewarm hook scopes to the mnemos project (and surfaces
#    conventions/corrections/recent-sessions).
#  - Auto-search memory block from `mnemos hook user-prompt` injected via
#    --append-system-prompt. We call the hook ourselves because claude -p
#    does NOT fire UserPromptSubmit hooks (only SessionStart). Without this,
#    the on-arm only gets static prewarm — no per-prompt relevance.
#  - --strict-mcp-config + mcp_on.json keeps MCP isolated from the user's
#    other servers.
#  - A scratch store seeded from a snapshot of the live one. MNEMOS_CONFIG
#    is inherited by the hook call below, by claude's global mnemos hooks
#    and by the MCP server claude spawns, so everything the run writes
#    (fixture corrections, sessions, injection log) lands in the snapshot
#    and is deleted with the sandbox. Before this, capture runs wrote their
#    fixtures into the real store, where a made-up "auth token TTL"
#    correction and a helper that does not exist were injected into real
#    sessions for months.
set -euo pipefail

trigger="$1"
mnemos=${MNEMOS_BIN:-/Users/andrefigueira/.local/bin/mnemos}
sandbox=$(mktemp -d -t mnemos-on)
mkdir "$sandbox/mnemos" "$sandbox/store"
# claude runs as a child, not via exec, so this trap fires and the sandbox
# is removed when the run ends.
trap 'rm -rf "$sandbox"' EXIT

live_db=$("$mnemos" config | python3 -c 'import json,sys; print(json.load(sys.stdin)["Storage"]["Path"])')
if [[ -f "$live_db" ]]; then
  sqlite3 "$live_db" ".backup '$sandbox/store/mnemos.db'"
fi
printf '[storage]\n  path = "%s"\n' "$sandbox/store/mnemos.db" > "$sandbox/store/config.toml"
export MNEMOS_CONFIG="$sandbox/store/config.toml"

cd "$sandbox/mnemos"

# Pull a memory block from the same hook that runs in interactive mode.
# Empty when nothing scores ≥ promptMemoryMinScore.
memory_block=$(printf '{"hook_event_name":"UserPromptSubmit","prompt":%s}' \
  "$(printf '%s' "$trigger" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))')" \
  | "$mnemos" hook user-prompt 2>/dev/null || true)

if [[ -n "$memory_block" ]]; then
  claude -p "$trigger" \
    --append-system-prompt "$memory_block" \
    --mcp-config /Users/andrefigueira/Code/mnemos/verify/mcp_on.json \
    --strict-mcp-config \
    --dangerously-skip-permissions \
    --output-format stream-json \
    --verbose
else
  claude -p "$trigger" \
    --mcp-config /Users/andrefigueira/Code/mnemos/verify/mcp_on.json \
    --strict-mcp-config \
    --dangerously-skip-permissions \
    --output-format stream-json \
    --verbose
fi

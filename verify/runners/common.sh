#!/usr/bin/env bash
# Shared setup for the A/B arms. Sourced, not executed.
#
# Everything machine-specific is derived rather than hardcoded:
#
#   MNEMOS_BIN   the binary under test. Defaults to `mnemos` on PATH so a
#                globally installed build is used; set it to test a local
#                one, e.g. MNEMOS_BIN=./mnemos mnemos verify behavior.
#   VERIFY_DIR   this directory, derived from the sourcing script's own
#                location, so the MCP configs resolve on any checkout.
#   mcp_on/off   VERIFY_DIR/mcp_{on,off}.json.
#
# The JSON hook payloads are built with printf instead of python3: the
# harness should not require a Python install to run a Go binary.
set -euo pipefail

_runner_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERIFY_DIR="$(cd "$_runner_dir/.." && pwd)"
MNEMOS_BIN="${MNEMOS_BIN:-mnemos}"
MCP_ON="$VERIFY_DIR/mcp_on.json"
MCP_OFF="$VERIFY_DIR/mcp_off.json"

# Set by scratch_project; declared here so `set -u` does not trip the EXIT
# trap when a runner fails before creating the sandbox.
SANDBOX_DIR=""

# json_escape emits a JSON string literal for its argument, escaping the
# characters a trigger prompt can realistically contain.
json_escape() {
  local s=$1
  s=${s//\\/\\\\}
  s=${s//\"/\\\"}
  s=${s//$'\r'/\\r}
  s=${s//$'\n'/\\n}
  s=${s//$'\t'/\\t}
  printf '"%s"' "$s"
}

# mnemos_user_prompt_hook feeds a UserPromptSubmit payload to the hook and
# prints whatever context it decided to surface. Empty when nothing cleared
# the relevance floor. Failures are swallowed: the harness must still run
# when mnemos is unavailable.
#
# The payload goes through --payload rather than stdin because the runner
# is also the reference for how a non-Claude harness drives the hook, and
# that path cannot pipe stdin.
mnemos_user_prompt_hook() {
  local trigger=$1
  "$MNEMOS_BIN" hook user-prompt \
    --payload "$(printf '{"hook_event_name":"UserPromptSubmit","prompt":%s}' "$(json_escape "$trigger")")" \
    2>/dev/null || true
}

# scratch_project creates a temp cwd whose basename is "mnemos" so the
# prewarm block scopes to the mnemos project, and registers cleanup.
scratch_project() {
  local tag=$1
  # Portable mktemp form. `mktemp -d -t prefix` means "prefix" on BSD/macOS
  # but requires the template to contain X's on GNU coreutils, so the
  # original spelled a macOS-only invocation. Spelling the template out
  # works on both.
  #
  # SANDBOX_DIR is deliberately global: the EXIT trap runs after the
  # function has returned, where a `local` would be unbound and `set -u`
  # would turn cleanup into a second, confusing failure.
  SANDBOX_DIR=$(mktemp -d "${TMPDIR:-/tmp}/mnemos-$tag.XXXXXX")
  mkdir -p "$SANDBOX_DIR/mnemos"
  # Guarded so cleanup is silent when a minimal shell has no `rm`; the
  # harness's job is the measurement, not tidying, and a noisy trap on an
  # already-failing run reads like a second bug.
  trap 'command -v rm >/dev/null 2>&1 && rm -rf "$SANDBOX_DIR"' EXIT
  cd "$SANDBOX_DIR/mnemos"
}

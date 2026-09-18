## Why

Mnemos' measured behavior lift comes from harness-side hooks that push memory into context before the agent decides whether to look. `mnemos init` only wires those hooks for Claude Code — the hook-install block in `cmd/mnemos/commands.go` is gated on `hasClaudeCode`, so every other registered client (Codex CLI, Cursor, Windsurf) gets the MCP tool surface and nothing else. The README's own A/B table shows where the lift lives: `session_start_on_edit` 5/5 vs 0/5 and `oss_first_for_protocol` 5/5 vs 0/5 are pure push effects; the three scenarios the model already handles match exactly on both arms.

pi is a fourth MCP-capable host that currently gets the weakest integration of all. Beyond missing the hooks, its MCP path is *below* Codex parity: `pi-mcp-adapter` hides all 21 mnemos tools behind a single proxy tool (`mcp`), so the model must first decide to call `mcp({ search: ... })` before it can discover `mnemos_save` exists. The failure mode the capture directive was built to fix — LLMs skip optional tool calls when the task looks like plain editing — is made worse, not better.

## What Changes

- **Harness-neutral hook results.** Mnemos' hook subcommands gain `--payload <json>` and `--format json`, emitting a transport-agnostic result (`session_id`, `context`, `block`, `block_reason`) instead of harness-specific stdout. The existing default path still renders exactly what Claude Code consumes today; this is additive, not a rewrite of the decision logic.
- **A pi integration package** that maps pi's lifecycle events onto those hook results: prewarm at session start, per-prompt memory recall plus the capture directive, file-relevant memory and the write-boundary guardrail before edits, passive file touches, and session close. Injection goes through `before_agent_start` returning `systemPrompt`, so each push applies to the current turn only and does not accumulate in session history.
- **Tool exposure at Codex parity.** mnemos' pi configuration registers its MCP tools directly rather than behind the adapter's proxy, so the model can call them without first discovering them. The pi-side guardrail identifies mnemos' write tools by name suffix rather than by an assumed prefix, so it keeps covering writes whatever naming scheme the adapter applies — including the package-declared route, where the adapter prefixes the server name with the package name.
- **One-command install.** A pi package manifest so a single `pi install` wires the MCP server, the skill, and the extension together, plus `mnemos doctor` coverage for the pi host.

Explicitly **not** in this change: a resident mnemos daemon, HTTP hook endpoints, replacing pi's compaction summary, and a pi runner for `mnemos verify`. The extension is written behind an internal hook-invocation seam so the spawn-based transport can be swapped for an HTTP one later without changing any event mapping.

## Capabilities

### New Capabilities

- `harness-neutral-hooks`: The hook subcommands' transport-agnostic input/output contract — payload flag, JSON result envelope, and the guarantee that the default CLI rendering used by Claude Code is unchanged.
- `pi-integration`: What mnemos delivers inside the pi harness — installed package contents, lifecycle-event mapping, injection channel, and tool visibility/naming.

### Modified Capabilities

None. `openspec/specs/` is empty; this is the first change recorded in the repo, and the existing Claude Code hook behavior is preserved rather than redefined.

## Impact

- `cmd/mnemos/hook.go` — hook subcommands accept a payload flag; the `emit*` helpers move from writing to an `io.Writer` to returning a result, with a renderer supplying the current bytes.
- `cmd/mnemos/prewarm.go` — reuses the same payload path; `hookInput` stays the single payload shape.
- `internal/installer/installer.go` — a pi MCP-config target, alongside the existing JSON/TOML targets.
- `cmd/mnemos/commands.go` — `mnemos init` and `mnemos doctor` learn the pi host. The Claude Code hook block keeps its existing gate.
- New `pi/` package in this repository: extension, package manifest, and package-local MCP config. Versioned with the Go binary so the hook contract and its consumer cannot drift.
- The pi-side guardrail's coverage depends on matching the tool names pi actually exposes. It is matched by suffix rather than by a fixed prefix precisely so that neither the adapter's prefix mode nor package-based installation can silently drop that coverage.
- `cmd/mnemos/hook.go`'s existing `isMnemosWriteTool` matcher is unchanged: it serves Claude Code's matcher-driven dispatch, and the pi path owns its own match on the `tool_call` event.
- Unchanged: the MCP stdio surface, `internal/api`, and the storage schema.
- Evidence boundary: with the `mnemos verify` pi runner out of scope, this change ships with code-level verification only. No measured lift number is claimed for pi on completion.

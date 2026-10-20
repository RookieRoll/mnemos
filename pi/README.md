# Mnemos in pi

Install the package's MCP adapter dependency and the local package as described
in the repository README. Keep a copy in pi's user directory, for example
`~/.pi/agent/local-packages/mnemos`, rather than registering a development checkout.
After a source update, refresh that copy and reload pi. `pi update` manages npm/git
sources; it does not refresh a local directory package. This document does not
update an already installed copy or binary.

## Shared session continuity

Continuity is optional. Before enabling it, restart **every host process using
the same SQLite database** with a continuity-capable mnemos binary. An older
adapter running the new binary is supported; an old binary sharing the database
cannot protect the new sessions from its legacy session inference. To disable
continuity, set `[continuity].enabled = false` in the selected TOML file and keep
the new binary.

The package's `mcp.json` entry is the single executable and config route for both
MCP and automatic hooks. Use an absolute executable path or a bare program name,
and, when selecting a config explicitly, one absolute `--config` path. Pi rejects
relative paths, duplicate or ambiguous selectors, shell wrappers, and server
`env` overrides for `HOME`, `USERPROFILE`, `HOMEDRIVE`, `HOMEPATH`, or `PATH`,
because those settings can make the adapter and hooks resolve different files or
executables. Other adapter-owned entry fields remain available. Feature switches
come from the selected TOML file: `[continuity].enabled` opts into continuity
startup, and `[integration].disabled` suppresses automatic pi integration. With
continuity disabled, the existing prewarm startup is retained.
Automatic pi startup resolves a Git worktree from its current directory. For a
non-Git workspace, use the explicit continuity API with `workspace_root`; the
extension does not guess a root from a directory name.
There is currently no pi startup setting for that root.
An agent can call `mnemos_session_start` explicitly with the
[non-Git workspace input](../docs/MCP_TOOLS.md#shared-session-continuity-explicit-v1)
and retain its returned session ID for subsequent explicit continuity calls;
this does not bind the extension's automatic lifecycle hooks.

Each startup, reload, new, resume or fork opens an independent session. A
capability check runs first. A confirmed unsupported binary can use the legacy
startup. A timeout, malformed receipt or unsupported response version cannot
prove that startup failed: pi stays unbound and makes at most one exact retry
with the same start key. It never opens a second fallback session to guess.
While unbound, prompt capture, file touches and shutdown do not infer another
session from an empty ID; the write-boundary guardrail still runs. Suppressed
events are not replayed later. If startup committed but prewarm failed, the
committed session ID is retained.

The first turn receives the bounded prewarm and candidate offer through pi's
non-accumulating `before_agent_start` channel. A candidate offer does not select
the newest task or claim ownership. Explicit recovery tools can be used later;
another session's updates are never pushed in the background. `tool_call` is a
guardrail channel, not a system-prompt injection channel.

Use the actual tools exposed by your adapter, identified by the suffixes
`mnemos_checkpoint` and `mnemos_resume_context`. The package route may prefix
them with the package and server names. At a meaningful milestone, blocker,
requested handoff or before compaction, explicitly save an agent-authored
checkpoint for your exact current session and workspace. Do not copy a transcript
or tool output. Visibility defaults to `owner`; choose `workspace` explicitly to
publish progress to other owners in that worktree. This shares the checkpoint,
not private observations or the source session's reflection.

Use the returned revision for the next save or clear. A save/clear succeeds only
with a version-1 `applied` or `replayed` receipt. An uncertain write needs the same
request ID and unchanged input for an exact retry; a conflict requires reading
current state before deciding what to write. Clear retains the revision marker
to reject delayed writes. There is no checkpoint history or automatic TTL;
24 hours marks progress stale rather than deleting it. Final session summaries
remain separate, and shutdown never promises to create a final checkpoint.

Choose a source explicitly when there are multiple tasks, stale progress or a
branch/HEAD mismatch. Several agents may read the same source: nobody claims or
consumes it. Check the current files, differences and relevant tests before
acting; matching HEAD does not prove the working tree is unchanged. References
are locators, not access grants, fetched source text or evidence that a task
succeeded. Explicit tool errors remain visible to the calling agent; a quiet
automatic hook is never a persistence receipt.

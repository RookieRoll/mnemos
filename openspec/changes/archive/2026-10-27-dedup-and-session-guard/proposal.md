## Why

Two items on the 2026-10-26 issue list (#3, #5) share one root cause: the memory
layer trusts identifiers handed to it by the caller, and it only de-duplicates
content it has hashed byte-for-byte.

- **#3 — `mnemos_session_end` accepts any UUIDv7.** The service closes whatever
  `session_id` it is given, so an agent, a stale script, or a prompt-injected tool
  call can close another agent's live session. The only barrier today is a
  documentation rule in the mnemos skill ("only close the current session"), which
  is a soft constraint with no server-side enforcement.
- **#5 — near-duplicate writes.** Dedup is `hashContent` over the literal content,
  scoped to `(agent_id, project)`. Re-saving the same observation with an extra
  space, different punctuation, or different case creates a second live row. The
  store then carries two competing memories that rank against each other and can
  supersede one another unpredictably.

## What Changes

- **Session-end ownership guard (#3).** `mnemos_session_end` accepts only
  (a) the session id the invocation is actually bound to, or (b) a session id
  carried by a valid continuity receipt whose workspace binding matches the
  invocation's workspace. Any other id is rejected with an explicit error code and
  guidance text. The hook paths (`EndForHost` / `CloseForHost`) resolve the host
  session themselves and keep their current behaviour. The local CLI
  (`mnemos sessions close`, a trusted maintenance channel) stays the legal route
  for orphan and bulk closure.
- **Near-duplicate dedup (#5), a fold rather than a delete.** A derived, normalized
  hash (case-folded, whitespace and punctuation removed) is stored next to the
  existing content hash. On save, an exact-hash miss falls through to a
  normalized-hash lookup scoped to the same `(agent_id, project)` inside a
  configurable time window (default 24h). A hit merges into the existing
  observation (access bump) and writes a provenance record that holds the
  merged-away content verbatim, so the fold is auditable and can be undone by
  re-saving that content. Two saves sharing session id and title inside the window
  merge under the same rule. Outside the window nothing is merged — the save
  returns a hint only. `hashContent` itself is not modified: the legacy content
  hash keeps exactly the value that evolution and Restore depend on. No backfill,
  no rewrite of existing rows.

## Impact

- Capabilities: `memory-lifecycle-retrieval` (dedup, capability reporting),
  `session-continuity` (session-end ownership).
- Code: `internal/memory` (hash helpers, save path, service contract),
  `internal/storage` (one additive migration, normalized lookup, provenance table),
  `internal/session` (ownership guard + error), `internal/mcp` / `internal/api`
  (error mapping, save result shape), `internal/config` (window knob),
  `internal/continuityflow` (receipt lookup seam), skill + docs text.
- **Breaking for existing automation.** Callers that pass a foreign session id to
  `mnemos_session_end` now fail. Hand-run cleanup loops and external batch closes
  must move to `mnemos sessions close`. The skill's "only close the current
  session" line changes from advisory to enforced, so release notes and the three
  skill mirrors must say so.
- Depends on the `cli-maintenance-contract` change for the replacement channel. If
  that change has not landed, this change must not land first: removing the only
  documented close path without shipping the supported one is a capability
  regression.
- Schema: one additive migration at `0012` — new nullable column plus an index,
  plus one new provenance table that also stores the merged-away content. Data
  grows by one body per near-dup merge and provenance rows are not pruned by
  `dream`. Non-breaking under the `schema_migrations` + numbered SQL convention;
  historical migration files stay immutable.
- No new dependencies, no CGO, no LLM calls. Normalization and the window check

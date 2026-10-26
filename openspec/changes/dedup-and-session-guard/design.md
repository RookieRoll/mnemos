## Context

`internal/memory` saves through a `(agent_id, project, content_hash)` dedup: the
service hashes the literal content, asks the store whether that hash already
exists for the pair, and returns the existing row if so. That is exact-match only.
The hash is load-bearing beyond dedup: `internal/memory/evolution_test.go` and the
Restore path assert the legacy content hash is never rewritten, so dedup cannot be
changed by re-defining `hashContent`.

`mnemos_session_end` currently forwards its `session_id` argument straight into
the session service with no ownership question asked. Hooks do not use that path
for foreign ids — `EndForHost` / `CloseForHost` resolve the host session
themselves — so tightening the tool path does not disturb hooks.

## Decisions

### D1 — Do not touch `hashContent`; add a second, derived hash

A normalized hash is computed at write time and stored in a new nullable column.
`content_hash` keeps its current definition and value forever. This keeps the
evolution/Restore invariants literally true and keeps the migration from implying
any row rewrite: existing rows keep `content_hash_normalized = NULL`.

Normalization is a pure function over the content: keep only letters and digits
(`unicode.IsLetter` / `unicode.IsDigit`), then lowercase. No stemming, no
synonym table, no locale rules — the aim is "same sentence typed twice", not
semantic matching, and the memory layer makes no inference.

### D2 — Lookup order on save

1. Exact-hash dedup, unchanged, `(agent_id, project, content_hash)`.
2. If the near-dup window is enabled and step 1 missed: look for a live
   observation with the same `(agent_id, project, content_hash_normalized)` whose
   `created_at`/`invalidated_at` state is live and whose last touch is inside
   `now - window`. A hit merges.
3. If the window is enabled and the input carries both a session id and a
   non-empty title: look for a live observation with the same
   `(session_id, title)` inside the window. A hit merges.
4. Otherwise insert, storing both hashes.
5. If no candidate was inside the window but a normalized-identical row exists
   outside it, the save succeeds and reports the candidate as a hint. This costs
   one unbounded index probe and only on the miss path.

Merging means: bump the survivor's access signal, insert one provenance row that
carries the **whole incoming content** of the save being folded away, and return
the survivor's id — no second live row.

### D3 — Migrations

One additive migration at **0012** (next free number after
`0011_maintenance_coordinator.sql`):

- `ALTER TABLE observations ADD COLUMN content_hash_normalized TEXT;`
- index on `(agent_id, project, content_hash_normalized)`
- index on `(session_id, title, created_at)` for rule 3
- `observation_near_dups(survivor_id, incoming_content, incoming_hash,
  normalized_hash, reason, agent_id, project, session_id, title, created_at)`
  where `reason` is `content_hash` or `session_title`, plus an index on
  `survivor_id`. `incoming_content` holds the merged-away content verbatim (not
  truncated, not re-normalized) so a merge is auditable and reversible; it is
  written once and read only by provenance/audit queries, never by search,
  ranking or prewarm.

A dedicated table rather than the existing relation/graph surface: graph edges
feed the ranking multiplier, and a dedup trace must not silently change scores.

### D4 — Configuration

One knob, `[search].near_dup_window`, a duration string, default `"24h"`.
`"0s"` disables near-dup merging (exact dedup stays). Validated by the existing
config validation pass with a `search: …` prefixed error.

### D5 — Capability reporting

`read_capabilities` keeps `deterministic_dedup` true, and states its two layers:
exact hash dedup is unconditional and deterministic; near-duplicate merging is
bounded by the configured window and merges only content that normalizes
identically (or shares session and title inside the window), with the merged-away
content retained in a provenance record. Nothing here is probabilistic or
model-derived.

### D6 — Session-end ownership

`session.Service` resolves, for the invocation, its bound session id from the same
seam the hook path uses. The end request is authorised when either:

- `session_id == boundSessionID`, or
- a continuity receipt exists for `session_id` and
  `receipt.workspace_root == invocation.workspace_root` (compared with the
  existing workspace canonicalization used by continuity).

Otherwise it is rejected. Ownership is decided by workspace binding, not by agent
identity: a different agent in the same workspace that holds a receipt for the
session may close it, and a same-agent caller in a different workspace may not.
When no binding is resolvable at all, only a matching receipt authorises the call.

The CLI `sessions close` path lives outside this guard by construction — it is the
trusted maintenance channel, and it is what the rejection message points to.

### D7 — Rejection contract

A sentinel in `internal/session` (e.g. `ErrSessionNotOwned`) wrapped with the
subsystem prefix, carrying the rejected id and the workspace; the transports map
it to a stable error code (`session_not_owned`) whose text names the supported
route: close the current session, or run `mnemos sessions close <id>` locally.

Implementation note (review ruling): the receipt branch is validated through
the continuity read path as the binding oracle — the authoriser asks
continuity to resolve the target session's binding against the invocation's
canonicalised workspace (the same canonicalization continuity already uses),
kept behind one named seam so a dedicated receipt accessor can replace it
later without touching the guard. Every `session_end` costs one extra light
read; acceptable at this frequency. The hook paths (`EndForHost` /
`CloseForHost`) resolve their session themselves and are unaffected.

## Risks

- **Rule D2.3 (same session + same title) can match content that differs beyond
  normalization — mitigated, not open.** The ruling keeps the rule and requires
  the merged-away content to be persisted verbatim in
  `observation_near_dups.incoming_content`. So a merge is an auditable fold, not a
  deletion: the body is recoverable from the provenance row, and rollback is a
  re-save of that retained text (this change ships no dedicated undo command).
  What remains accepted is duplication of one body per near-dup event and the fact
  that the `dream` prune pass does not visit provenance rows.
- Pre-existing rows have a NULL normalized hash, so near-dup never fires against
  them. Accepted under the no-backfill decision and stated in the spec delta.
- Automation that batch-closes sessions breaks on upgrade. Requires release-note
  and skill text changes in the same change.
- `sessions close` must exist (dependency on `cli-maintenance-contract`).

## Assumptions to confirm before apply

- migration 0012 is free and no later migration adds a column to `observations`
- the config section that owns save-path knobs (`[search]` assumed) and its
  validation entry point
- the exact `read_capabilities` field shape and where the `deterministic_dedup`
  sentence lives (`internal/mcp`)
- the save result type(s) in `internal/memory` / `internal/mcp` / `internal/api`
  that must carry the merge/hint outcome
- the invocation seam that resolves the bound session id (assumed
  `internal/continuityflow`), and the exact continuity receipt accessor used by
  D6
- whether the existing relation/graph table would be a better home for the
  provenance row (rejected here to protect ranking)

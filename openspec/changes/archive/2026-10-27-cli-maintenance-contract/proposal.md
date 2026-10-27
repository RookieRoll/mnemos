# Change: cli-maintenance-contract

## Why

The maintenance half of the CLI drifted away from the read-only half. Nine
defects from `mnemos-issues-2026-10-26.md` share one root cause: maintenance
subcommands were added one at a time, each with its own flag parsing, its own
notion of "did the user mean this?", and its own idea of what belongs in a
report. The result is a surface where reading a report mutates state, where the
two MCP stats views disagree, and where bulk operations have no preview.

Observed symptoms, with issue ids:

| # | Symptom | Why it matters |
| --- | --- | --- |
| 1 | `mnemos prewarm` opens a Mnemos session as a hidden side effect of a read. Hand-run `prewarm` inflates the session table and the "recent sessions" section it is supposed to render. | A reporting command changes the data it reports. |
| 2 | Sessions opened by hooks and never closed accumulate forever. There is no way to list them with filters, no way to reclaim them in bulk, and `mnemos stats` does not show the backlog. | Continuity resume selection degrades as orphans pile up. |
| 4 | Promoted skills cannot be deleted or merged from the CLI, and auto-promotion can insert a duplicate row for the same `(project, label)` group. | Duplicate procedural memory competes with itself in skill ranking; only a hand-edited DB can clean it. |
| 6 | Superseded or low-value observations cannot be retired in bulk. | Retrieval candidates fill with facts nobody believes any more; the only tool is per-row surgery. |
| 7 | `-h` / `--help` is not handled uniformly, several maintenance subcommands parse no flags at all (`runPrune`, `runSessions`), and exit codes conflate "you typed it wrong" with "it failed". | Scripts and hooks cannot branch on the result; `--help` on a maintenance command can perform work. |
| 8 | `read_stats` reports `storage_bytes` as a constant `0`. | The one number an operator uses to watch DB growth is a lie in both MCP surfaces. |
| 9 | `embed status` does not report embedded/total or the model distribution; `embed backfill` drops skipped rows from its report silently. | Embedding coverage cannot be audited, and a shrinking backlog is indistinguishable from a stuck one. |
| 10 | There is no supported way to take a consistent copy of the SQLite file while the server is running. `export` is a text projection and warns about it, but the warning names no alternative. | Backup by copying a live WAL-mode DB risks an inconsistent snapshot. |
| 12 | Rumination raw candidates flood the stale-raw queue with no configuration switch. | Operators must dismiss noise one by one; there is no supported way to say "never queue raw". |

## What Changes

**CLI dispatch contract (issue #7 — foundation for everything else)**

- `cmd/mnemos` intercepts `-h` / `--help` at the dispatch layer, before any
  command handler runs: print usage, exit 0.
- Every maintenance subcommand parses flags through its own `flag.FlagSet`
  (`flag.ContinueOnError`). `flag.ErrHelp` maps to exit 0.
- Exit codes: `0` success or help, `1` runtime/domain error, `2` usage error
  (unknown subcommand, unknown or invalid flag, missing required value).
- Bulk destructive subcommands expose `--dry-run`, which runs the selection and
  prints the plan without writing.

**prewarm side effects (issue #1)**

- `mnemos prewarm` opens a session only when invoked with a hook payload
  (explicit `--payload`, or a non-terminal stdin JSON object carrying
  `hook_event_name` or `session_id`). A hand-run `prewarm` is read-only.
- `--open-session` forces the old behaviour.
- The hook command strings in `internal/installer` are **not** touched; Claude
  Code compatibility is guarded by the existing pre-change binary byte-diff
  tests (`MNEMOS_PRE_CHANGE_BIN`).

**Session lifecycle (issue #2)**

- `mnemos sessions` gains `--status`, `--limit`, `--offset`, `--json`.
- `mnemos sessions close --stale 24h --status abandoned --summary "..."` reclaims
  orphaned open sessions in bulk, reusing `session.Service`'s
  `ListLegacyOpen` / `closeEach` / `CloseAllOpen` paths.
- `mnemos stats` (and the MCP stats surfaces) report `open sessions: N (oldest Xd)`.

**Observation retirement (issue #6)**

- `mnemos retire --project X --importance-max 3 --no-access [--dry-run]` sets
  `valid_until = now` in bulk and prints the affected count plus a sample.
- New bulk retirement method on `memory.Service` and its maintenance store.

**Skill maintenance (issue #4)**

- `mnemos skill delete <name>` and `mnemos skill merge <name>` with `--agent`,
  `--project`, `--keep-version`, `--dry-run`. Deleting goes through the normal
  store interface so FTS stays in sync.
- Auto-promotion keeps the `promoted-origin:<hash>` keying exactly as is, but
  consults the existing skill by `(project, label)` before `upsertPromotedSkill`
  and merges/marks superseded instead of inserting a duplicate.

**Storage facts and backup (issues #8, #10)**

- `storage.Stats()` fills `StorageBytes`; the MCP resource `read_stats` and the
  MCP tool `handleStats` render from the same typed value.
- `mnemos backup <path>` produces an online-consistent snapshot with
  `VACUUM INTO` (pure Go driver, zero CGO).
- The `export` stderr warning names `mnemos backup <path>`.

**Embedding observability (issue #9)**

- `embed status`: `embedded N/M` plus a per-`embedding_model` distribution.
- `embed backfill`: report gains `Skipped` with reason breakdown and `Remaining`.

**Rumination flood control (issue #12)**

- `[rumination].raw_promote = "never" | "auto"` (default `auto` = today's
  behaviour). `never` keeps raw candidates out of the stale-raw queue.
- `config.Load` validates the enum; `docs/CONFIGURATION.md` documents it.

## Impact

Affected capabilities and specs:

- `cli-surface-contract` (new) — help, exit codes, `--dry-run` convention.
- `skill-lifecycle` (new) — skill delete/merge and duplicate-free promotion.
  No existing main specification owns skill lifecycle: `memory-evolution` covers
  topic and replacement records only.
- `agent-workflow` — hook-conditional session opening in `prewarm`.
- `session-continuity` — listing filters, stale reclaim, backlog reporting.
- `memory-lifecycle-retrieval` — bulk fact-time retirement.
- `storage-runtime` — `StorageBytes`, `backup`, export warning.
- `embedding-maintenance` — embed status/backfill reporting.
- `runtime-configuration` — `[rumination].raw_promote`.

Affected code:

- `cmd/mnemos/`: `main.go` (dispatch, exit codes, `commands` map, `printUsage`),
  `subcommands.go` / `commands.go` (`runPrewarm`, `runSessions`, `runStats`,
  `runPrune`, `runEmbed`, `runSkill`, `runExport`), new `backup`, `retire`.
- `internal/memory/`: `Service` retirement method, store interface addition.
- `internal/session/`: backlog read + stale-close helpers.
- `internal/skills/`: delete/merge, promotion dedup in the dream/journal path.
- `internal/storage/`: `Stats()`, backup helper, retirement SQL, embedding
  distribution query.
- `internal/mcp/resources.go`, `internal/mcp/tools.go`: stats shape alignment.
- `internal/config/config.go`, `docs/CONFIGURATION.md`: `raw_promote`.
- `internal/installer`: **no change** (explicit non-goal, guarded by tests).

Behavioural contract changes (intentional, documented in `design.md`):

- Usage errors now exit `2` instead of `1`; help exits `0`. Scripts and hooks
  can finally distinguish "the invocation is wrong, do not retry" from "the
  store failed, retry may help". The previous behaviour was undocumented.
- `mnemos prewarm` is read-only unless it detects a hook payload. `--open-session`
  is the migration for any caller that relied on the implicit session.
- `dream` promotion now merges into an existing `(project, label)` skill instead
  of inserting a duplicate. The `promoted-origin` keying is unchanged, so no
  already-promoted skill changes identity.

No new dependencies, no migration required (see design.md — all columns already
exist), no change to `promoted-origin` keying, no LLM calls, zero CGO.

## Non-goals

- The rumination `skill_min_uses = 10` feedback loop. `use_count` and
  `effectiveness` are effectively all-zero, so the threshold never fires. Fixing
  it needs a recording path in the skill-application flow, which does not exist
  yet; it is tracked as a follow-up change, not folded into this one.
- Any change to hook command strings, hook payload shape, or adapter contract.
- Multi-tenant/ACL concerns for `backup` (local-first; the file is the operator's).
- `retire` for skills or sessions. Only observations retire in this change.

# Design — cli-maintenance-contract

## Context

Mnemos exposes one binary (`cmd/mnemos`) with a hand-rolled `commands` map, a
`printUsage` text, and per-command handlers that each decide their own flag
handling. Hook entry points (`mnemos hook`, `mnemos prewarm`) are called by
adapter-generated command strings that are frozen by byte-comparison tests built
from a pre-change commit. Storage is one SQLite file in WAL mode through
`modernc.org/sqlite`; observations are bi-temporal (`valid_from`/`valid_until`
for fact time, `created_at`/`invalidated_at` for system time). Promotion of
corrections into skills is keyed by `promoted-origin:<12-char sha256>`.

Two constraints shape every decision below:

1. **Hook command strings are immutable in this change.** The Claude Code
   compatibility tests build the pre-change binary via `git worktree` and diff
   its output against the current one. Any edit to those strings fails them.
2. **Retirement is fact-time, supersession is system-time.** Keeping those
   separate is what makes `SearchInput.AsOf` meaningful.

## Goals / Non-Goals

Goals:

- Reading commands never write.
- Every maintenance subcommand can be run with `--help` and with `--dry-run`
  when it is bulk and destructive, and reports what it did.
- Exit codes let a script distinguish usage errors from failures.
- The two MCP stats surfaces and the CLI agree by construction, not by copy.

Non-goals:

- Reworking the `commands` map into a registry type. The map plus `printUsage`
  is the documented extension point; this change adds entries, not structure.
- Changing hook payload shape or installer output.
- A skill usage/effectiveness feedback loop (see proposal Non-goals).

## Decisions

### D1. Help and exit codes live in the dispatch layer (issue #7)

`main.go` scans `os.Args[1:]` for `-h`/`--help` **before** dispatching, prints
`printUsage`, returns 0. Each handler receives a `*flag.FlagSet` created with
`flag.ContinueOnError` and a `bytes.Buffer`-backed `SetOutput`; `flag.ErrHelp`
from `Parse` maps to exit 0, any other parse error maps to exit 2.

Exit taxonomy: `0` success/help, `1` runtime or domain error, `2` usage error.
Today usage errors return 1. This is an intentional contract change: hooks and
scripts need to tell "the invocation is wrong, do not retry" from "the store
failed, retry may help".

Alternatives rejected:

- Per-command `-h` handling — repeats the same twelve lines per subcommand and
  is exactly how the surface drifted.
- `flag.ExitOnError` — calls `os.Exit` inside a handler, which makes the
  handlers untestable and skips deferred store closes.

Adding flag parsing to `runPrune` and `runSessions` is part of this decision:
a command that parses no flags cannot report a usage error, and `--help` on it
currently falls through to work.

### D2. `--dry-run` is one convention, not per-command invention

The bulk destructive commands in this change (`retire`, `sessions close --stale`,
`skill delete`, `skill merge`) implement `--dry-run` identically: run the exact
selection query inside a transaction, print the count and the same sample the
real run prints, roll back, exit 0. The selection code is shared with the write
path by taking the row ids from the selection and then writing by id, so preview
and execution cannot diverge.

`backup` has no `--dry-run`; it is non-destructive and its whole product is a
file.

### D3. `prewarm` opens a session only for hook invocations (issue #1)

Detection is one helper, `hookPayloadFrom(args []string, stdin io.Reader)`,
shared with the existing `hook` command:

- explicit `--payload <json>` → that payload;
- otherwise, if stdin is **not** a character device (pipe or regular file), read
  it and parse; a JSON object with `hook_event_name` or `session_id` counts;
- otherwise → no payload.

Stdin is never read from a terminal, so hand-run `mnemos prewarm` cannot block
on a read. With no payload, `prewarm` composes and prints the block and writes
nothing. `--open-session` forces the session open regardless of payload; it is
the only new flag on this path.

The session-open behaviour itself is unchanged — this decision only changes
*when* it is default. Because the installer strings are untouched and hook
invocations already carry payload by definition (argv on pi, stdin on Claude
Code), hook behaviour is byte-identical.

Risk accepted: an out-of-tree integration that calls `mnemos prewarm` with no
payload and relied on the implicit session now gets a read-only run. `--open-session`
is the documented migration.

### D4. Stale session reclaim reuses the service, not new SQL (issue #2)

`session.Service` already has the primitives (`ListLegacyOpen`, `CloseAllOpen`,
and the per-session close used by `closeEach`). `sessions close --stale <dur>`
selects open sessions with `started_at < now - dur`, then closes each through
the existing per-session close with a synthesised status (`--status`, default
`abandoned`) and `--summary`. No new store query for the write; only the
selection gets a bounded reader.

`--stale` durations are parsed with `time.ParseDuration`, so `24h`, `90m` and
`720h` all work. A non-positive duration is a usage error (exit 2), because
`--stale 0` would mean "close everything including the session that just
started".

Listing filters are `--status`, `--limit`, `--offset`, `--json`. Offset paging
is deliberate: the store is single-user, results are short, and keyset paging
would need an ordering contract the session store does not currently promise.
The shortcut is recorded in code as:
`shortcut: offset paging over session rows, upgrade to keyset paging if the open-session table exceeds ~10^4 rows`.

The backlog number (`open sessions: N (oldest Xd)`) is computed once in
`session.Service` (a small `OpenBacklog` read returning count and oldest
`started_at`) and formatted by both the CLI `stats` command and the MCP stats
surfaces, so the three views cannot disagree.

### D5. Retirement sets `valid_until` only (issue #6)

`mnemos retire` is a fact-time operation: `valid_until = now`. It does **not**
touch `created_at` or `invalidated_at`. Rationale: `invalidated_at` marks
"system stopped believing this because a replacement arrived" (supersession).
Retirement means "the fact stopped being true at time T" and must stay
distinguishable, so a historical query (`SearchInput.AsOf` before T) still sees
the observation and a current query does not.

Retired rows are not deleted and no FTS rebuild is required: the FTS mirror
indexes text columns only, and this change does not alter indexed text.

Selector safety: at least one of `--project`, `--importance-max`,
`--no-access`, `--older-than` must be present. With none, the command exits 2
rather than setting `valid_until` on the whole table. `--no-access` means
`access_count = 0`.

Interface placement follows the repo's "interfaces where consumed" rule: the
new bulk method goes on the existing maintenance interface used by `dream` and
friends, not on `Writer`.

### D6. Skill delete/merge and promotion dedup (issue #4)

This decision lives in the new `skill-lifecycle` capability. No existing main
specification owned it: `memory-evolution` covers topic and replacement records
only, and `opportunistic-maintenance` covers when maintenance runs, not what a
skill's lifecycle is.

`skill delete` and `skill merge` operate on the rows matching the name, narrowed
by `--agent` / `--project`. Deleting uses the store's normal delete so any FTS
or derived state is maintained by the store, never by a raw `DELETE`.
`skill merge` picks a keeper — highest version by default, or the explicit
`--keep-version` — and absorbs the losers: their body sections, tags and
source sessions are folded into the keeper (version bumped through the same
versioning path skills already use), then the loser rows are removed through
the store's normal delete so FTS stays consistent. The `skills` table has no
status/superseded column (`0001_init.sql`, `UNIQUE(agent_id,name)`), and a
migration was deliberately avoided — `0012` is reserved by the
dedup-and-session-guard change — so "mark superseded" is materialised as
absorb-then-remove rather than a flagged surviving row. Both commands print
the affected rows first and support `--dry-run`.

Promotion dedup keeps the keying contract intact: the promotion group id remains
`promoted-origin:<12-char sha256>` over `(agent_id, project, label)`, and
idempotency per group is unchanged. The addition is a pre-write lookup: before
`upsertPromotedSkill`, look for an existing skill with the same `(project, label)`;
if one exists, merge into it (bump version, union tags, keep the existing
`promoted-origin` tag) instead of inserting a second row.

Alternatives rejected:

- Changing the hash inputs to include the body — would re-open every historical
  promotion as a new group and duplicate the whole corpus.
- Deduplicating by name only — the same label in two projects is legitimately
  two skills.

If the skill store has no supersession marker today, adding one is a schema
change and would need the next migration number; the implementation task must
check `internal/storage/migrations/` first and record the number it used. It is
expected that `version` plus an existing status/superseded field suffices and no
migration is needed.

### D7. `StorageBytes` counts the main file plus the WAL, not the SHM (issue #8)

`storage.Stats()` stats the database path and, when present, the `-wal` file;
`-shm` is shared-memory and transient, so counting it would make the number
flicker. Free pages are included because the operator compares this number to
what the filesystem and `backup` output report, and page-count × page-size would
disagree with both. A stat failure yields `0` for the field and does not fail
the whole `Stats()` call — diagnostics must not become an outage.

Both MCP surfaces render from the typed `Stats` value through one formatting
function in `internal/mcp`; neither re-derives a number. `read_stats` and
`handleStats` must therefore be edited together and asserted equal by a test.

### D8. Backup is `VACUUM INTO`, and export points at it (issue #10)

`mnemos backup <path>` runs `VACUUM INTO` on a connection to the live DB. The
driver is `modernc.org/sqlite` (pure Go, zero CGO), and `VACUUM INTO` is the one
SQLite feature that produces a consistent snapshot of a live WAL database
without stopping writers or copying WAL files by hand.

The target must not exist; SQLite refuses and we surface that as a clear
`storage: backup target already exists: <path>` error with exit 1 rather than a
raw driver message. The path is passed as a bound parameter; if the pinned
driver rejects a parameter in that position, the fallback is a single-quoted
literal with doubled internal quotes — never string concatenation without
escaping.

`export`'s stderr warning gains one sentence naming `mnemos backup <path>`; it
stays a warning because `export` remains a text projection, not a backup.

### D9. Embedding reports are explicit about what they dropped (issue #9)

`embed status` prints `embedded N/M` and a per-`embedding_model` breakdown
(count per model, plus a flag when the stored dimension disagrees with the
active model's dimension). `embed backfill` gains `Skipped` with a reason
breakdown and `Remaining`. The reason set is closed and enumerable:
`superseded`, `incompatible_model`, `empty_content`. Skipped rows are rows the
existing backfill deliberately passes over; today they vanish from the report,
which makes a permanent skip look like a stall.

### D10. `[rumination].raw_promote` is a two-value enum (issue #12)

`never | auto`, default `auto`. `auto` preserves today's behaviour exactly, so
this is an opt-in switch. `never` means raw rumination candidates are not
enqueued into the stale-raw queue at all; they are still observable where they
are first produced. `config.Load` validates against the two values and reports
the allowed set in the error, per the existing config error style. Unknown
values fail load rather than silently defaulting — a typo in a noise-reduction
switch must not be invisible.

## Risks / Trade-offs

| Risk | Mitigation |
| --- | --- |
| Changing usage-error exit code from 1 to 2 breaks a caller that treated any non-zero as fatal. | Documented in this change; the previous behaviour was undocumented. Hooks ignore CLI exit codes for informational commands, and hook command strings are unchanged. |
| `prewarm` becomes read-only for a hand-run integration that relied on the side effect. | `--open-session` is the one-line migration; the session-open path itself is untouched. |
| Reading stdin could block an interactive `prewarm` if the terminal check is wrong. | Guard is an explicit `os.Stdin` mode check (character device ⇒ skip), covered by a test that runs with a TTY-less pipe and one that asserts no read on a nil reader. |
| `VACUUM INTO` on a large DB is a long-running write. | Documented as an offline-adjacent operation; it does not block readers, and the command prints the destination size on success. |
| Promotion dedup changes what `dream` writes without changing its keying. | The `promoted-origin:<hash>` tag and the per-group idempotency check are asserted unchanged by the existing dream tests; a new test covers the duplicate case. |
| `raw_promote = never` could hide a genuine rumination signal. | Default stays `auto`; `never` is opt-in and only affects the stale-raw queue. |

## Migration Plan

- No schema migration is expected: `valid_until`, `access_count`,
  `embedding_model`, skill `version` and skill status already exist. The
  implementation task must confirm against `internal/storage/migrations/` and, if
  a new column proves necessary, use the next sequential migration file with the
  usual non-breaking rules (embedded numbered SQL, `schema_migrations`).
- Config: `raw_promote` is absent-tolerant (defaults to `auto`), so existing
  `config.toml` files load unchanged.
- CLI: no command is removed. `sessions`, `skill`, `embed`, `prune`, `retire`,
  `backup`, `stats` are additive or widened.
- Rollback: revert the binary; no data shape changed. Retirement is revertible
  by clearing `valid_until` for the affected ids (printed at run time).

## Open Questions

Capability ids are resolved against `openspec/specs/`. Two capabilities are new
(`cli-surface-contract`, `skill-lifecycle`); the rest are existing deltas:
`agent-workflow`, `session-continuity`, `memory-lifecycle-retrieval`,
`storage-runtime`, `embedding-maintenance`, `runtime-configuration`.

Verification still pending (tooling unavailable when these artifacts were drafted):

1. Next migration number in `internal/storage/migrations/` (last known is
   `0010`; if skill supersession needs a column, use the next free number).
2. Whether the skill store's delete already maintains any derived index, and
   whether skills have a full-text mirror at all.
3. Whether `internal/mcp` currently has a session service available to
   `handleStats` (needed for the backlog line), or whether the backlog must be
   read through the API/storage layer.
4. Exact current names of the flag-parsing helpers in `cmd/mnemos` so the new
   `--dry-run`/exit-code plumbing reuses rather than duplicates them.

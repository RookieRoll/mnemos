## Context

Three symptoms, one cause: diagnostics that answer from configuration or from the
current store instead of from what is actually on disk, plus one capability claim an
agent cannot act on.

- Issue #11: a legacy `~/.mnemos/mnemos.db` (or any sibling `*.db` next to the config)
  keeps observations that the configured store does not have. Nothing surfaces it.
- Issue #13: `vault status` reports a timestamp and a watcher state it never observed.
  The vault directory contains no mnemos artifact at all, which is exactly the
  situation a user cannot distinguish from "exported recently".
- Issue #14: the release archive omits `docs/`, `SKILL.md` references it, and the
  documentation never states that the tool for changing pin state after the fact is
  registered only by the `maintenance` profile.

Constraints that shape the decisions below: zero CGO, no new dependencies, no LLM
calls, no destructive migrations, deterministic output, and every change ships
table-driven tests. This change touches no schema and no ranking/lifecycle behaviour.

## Goals / Non-Goals

Goals: make `doctor` and `vault status` fail loudly when reality is unknown or empty;
make the release artifact self-describing enough that the shipped `SKILL.md`
references resolve; make the documented profile boundary explicit so an agent knows
what it can do on the default surface.

Non-Goals: consolidating databases, migrating the legacy store, changing export
content, adding config knobs, changing MCP profiles or tool sets, and flipping
`pinned_memory` (see D9).

## Decisions

### D1 — Stale-database scan is bounded to two directories, not a filesystem walk

Scan roots are exactly `~/.mnemos` and `filepath.Dir(selectedConfigPath)`, top level
only, `*.db` (no recursion). Rationale: a full walk is unbounded on a large home
directory; the two roots cover every path mnemos itself has ever used. Roots are
cleaned to absolute paths and deduplicated, so a config in `~/.mnemos` scans once.
The configured store path is excluded by the same absolute-path comparison.

### D2 — "Populated" is decided by the data, "last write" by the filesystem

`last write` is the later mtime of `<file>.db` and `<file>.db-wal`, read with
`os.Stat` — no open, no lock. `populated` means the candidate holds at least one
observation (`SELECT 1 FROM observations LIMIT 1`). Candidates that open but hold no
rows are silent: a migrated-but-empty leftover is not a data-loss signal.

### D3 — Read-only open, and an explicit fallback when read-only fails

Candidates are opened with SQLite `mode=ro`, which forbids writes but can still need
`-shm`/`-wal` access for a WAL database. Therefore: if a read-only open fails and a
non-empty `-wal` file sits next to it, the candidate is reported as populated from
file state (`shortcut: WAL-backed candidates that cannot be opened read-only are
reported as populated from file sizes; upgrade if false positives become noisy`).
Every other failure yields `unreadable DB at <path> (<err>)` and the scan continues.
No candidate is ever repaired, migrated or vacuumed.

### D4 — Stale findings are warnings, not failures

Doctor keeps its success status when it reports a stale database: nothing was
verified as broken, and remediation is explicitly not part of this change. Ordering
is by path so the output is deterministic.

### D5 — Vault provenance lives in the vault directory, written atomically

`vault export` writes `<vault>/.mnemos-vault-export.json` (`exported_at` RFC 3339 UTC,
`observations`, `store`) via temp file + rename *after* the exported files, so an
interrupted export leaves the previous manifest — and therefore a truthful
`pending` — intact. Nothing about the export format changes.

### D6 — `pending` is a watermark count, watcher liveness is a heartbeat age

`pending N` counts observations whose `created_at` *or* `invalidated_at` is after the
manifest's `exported_at`, using the store's existing time encoding (the same
representation queries already compare against; a test pins it). No `exported` flag
and no schema change. `vault watch` writes `<vault>/.mnemos-vault-watch.json`
(pid, started_at, heartbeat_at) on start and each tick, removes it on clean shutdown;
`watcher: running` requires a heartbeat within three tick intervals, floored at 30s.
The heartbeat is **informational, not a lock**: a second watch overwrites it and
status reflects the most recent writer. Rationale: this is a single-user local tool;
lock stealing and PID-liveness checks are platform-specific complexity for no
diagnostic gain.

### D7 — Capability mapping (verify before apply)

`#11`/`#13` go to `workspace-diagnostics` (doctor and vault status are its diagnostic
surfaces). `#14` goes to `distribution-and-packaging`. If `openspec/specs/` already
owns release packaging under a different id (e.g. `agent-installation`), rename the
delta directory to that id and merge — the requirement text is capability-agnostic.

### D8 — Ship the docs; keep relative references

Chosen: add `docs/` to `.goreleaser.yml` archives and leave `SKILL.md`'s relative
`docs/*.md` references as they are. Rejected: rewriting references to repository URLs,
because skill/marketplace installs are repository checkouts (relative paths already
resolve there) and URLs would need editing per channel while diverging from the
offline archive. Consequence to accept: a repository URL reference is *not* the
fallback — if `docs/` ever leaves the archive, the references break, which is why the
archive requirement is the load-bearing one.

### D9 — `pinned_memory` stays true; the profile boundary is what gets documented

`openspec/specs/memory-lifecycle-retrieval/spec.md` states that the capability document
reports `pinned_memory` and `graph_retrieval` *only while the corresponding behaviour is
live in the running store*. Pinning is live in the default `core` profile: `mnemos_save`
(the core nine include it) takes a `pinned` parameter that excludes the observation from
decay, clears its TTL and slots it into the Tier-0 prewarm slot. So `pinned_memory: true`
is the contract-correct value and flipping it to `false` would be a regression, not a fix.

The real gap is narrower: the tool that changes pin state *after the fact*
(`mnemos_pin`) is registered only through `addProfileTool`
(`internal/mcp/tools.go`), i.e. `maintenance`-exclusive. On `core` an agent cannot pin
retroactively — it can only pin at `mnemos_save` time. Therefore the capability document
and the three `SKILL.md` copies gain that boundary statement, and the same rule generalises:
any capability registered only by `maintenance` is named by profile rather than presented
as a default capability. This corrects an earlier draft decision in this change that set
`pinned_memory: false`; that draft reasoned from tool availability instead of live
behaviour and is withdrawn.

## Risks / Trade-offs

- False-positive stale databases: an unrelated `*.db` in the config directory that
  happens to hold an `observations` table is reported. Accepted — the line is a
  warning, it names the path, and it is the price of catching the real case.
- Archive growth from `docs/`. Accepted; documentation is the point.
- A live-store read-only open takes a brief shared lock. Accepted; doctor already
  reads the store.
- Users whose vault was never exported will see `never` / non-zero `pending` after
  upgrade and may read it as breakage. Mitigation: the wording states the fact
  (`never`) rather than an error, and `vault export` clears it.
- Heartbeat as non-lock: two concurrent watchers are not prevented. Accepted for a
  local single-user tool; the status line stays truthful about the latest heartbeat.
- Documentation-only correction to the pin boundary: an agent that already assumed
  post-hoc pinning works on `core` gets no new tool, only a clearer statement. Accepted —
  the alternative (exposing `mnemos_pin` in core) is a profile change, out of scope.

## Migration Plan

No schema, config or data migration. Existing databases are untouched. Vaults without
a manifest report `never` until the next `vault export` writes one. Rollback is a
revert: the two new files (`.mnemos-vault-export.json`,
`.mnemos-vault-watch.json`) are inert JSON and the scan has no side effects.

## Open Questions

- Confirm the packaging capability id (D7) against `openspec/specs/`.
- Should `vault status` ever exit non-zero when `last_exported_at: never`? This draft
  says no (it is a report); revisit only with a user complaint.

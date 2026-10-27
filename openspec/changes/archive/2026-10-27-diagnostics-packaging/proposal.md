## Why

Three surfaces report health they cannot prove (issue list #11, #13, #14):

- `mnemos doctor` inspects only the configured store. A populated `~/.mnemos/mnemos.db`
  left behind by an earlier install, or any other `*.db` sitting next to the config
  file, is invisible — so a user whose memories "disappeared" is told everything is
  fine.
- `mnemos vault status` renders `last_exported_at` / `pending` / `watcher` from
  configuration. A vault directory mnemos has never written to therefore looks
  healthy while it is actually empty.
- `docs/` is absent from the release archives although the shipped `SKILL.md`
  references it, and the capability document advertises pinning without stating that
  the standalone `mnemos_pin` tool is `maintenance`-only — so an agent on the default
  `core` profile cannot learn that post-hoc pinning is unavailable and that pinning
  must happen at `mnemos_save` time.

All three are *reporting* defects: the memory layer decides nothing new, stores
nothing new, and calls no model. They are handled in one change because each is one
surface plus its table-driven test, and each failure mode is the same one — a
diagnostic that cannot distinguish "healthy" from "never looked", or a capability
claim that cannot be acted on.

## What Changes

- `mnemos doctor` scans the default data directory (`~/.mnemos`) and the directory
  containing the selected config file for `*.db` candidates, skips the configured
  store, and reports every populated candidate as
  `stale DB at <path> (last write <timestamp>)`. The scan is strictly read-only: no
  migration, no sidecar files, no writes.
- `mnemos vault export` writes `.mnemos-vault-export.json` into the vault directory on
  success; `mnemos vault watch` publishes `.mnemos-vault-watch.json` as a heartbeat;
  `mnemos vault status` derives `last_exported_at`, `pending N` and
  `watcher: running|stopped` from those two artifacts and says `never` when they are
  absent.
- `.goreleaser.yml` ships `docs/` in every archive; the three `SKILL.md` copies keep
  relative `docs/*.md` references (the alternative — rewriting them to repository
  URLs — is rejected in `design.md`).
- `pinned_memory` stays `true`, because pinning is live in `core` through
  `mnemos_save`'s `pinned` parameter. What gets documented is the profile boundary:
  `mnemos_pin` (post-hoc pin changes) is `maintenance`-only, and any other
  maintenance-only capability is named by profile.

## Non-Goals

- No `mnemos migrate` / `mnemos merge` command, and no automatic adoption, renaming or
  deletion of a stale database. Consolidation stays a separate, explicit future change.
- No change to the Obsidian export format, file naming or front matter.
- No new configuration fields, no schema migration, no new dependencies, no new MCP
  tools, no new LLM calls.
- No change to doctor's or status's exit status semantics beyond what the deltas state.

## Impact

- Capabilities: `workspace-diagnostics` (modified — doctor + vault status are the
  diagnostic surfaces), `distribution-and-packaging` (see `design.md` D7 for the
  capability-id check).
- Code: `cmd/mnemos` (doctor report assembly, `vault` subcommands), `internal/vault`
  (manifest, heartbeat, status projection), `internal/storage` (read-only candidate
  probe), `internal/mcp` (capability document wording). Tests live next to each.
- Distribution: `.goreleaser.yml`, three `SKILL.md` copies.
- Users: after upgrade, a vault that was never exported now reports
  `last_exported_at: never` with a non-zero `pending`. That is accurate, not a
  regression, and it is the intended visibility.

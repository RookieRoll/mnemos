## 1. Stale-database scan in `mnemos doctor` (P1, independent of 2 and 3)

- [ ] 1.1 Add the read-only candidate probe in `internal/storage` (or, if a store
      interface already exposes a probe, extend that): enumerate `*.db` at the top level
      of the two scan roots, clean to absolute paths, drop the configured store, and
      return `(path, lastWrite, populated|unreadable)` per candidate. `mode=ro` open;
      mtime from `os.Stat` on the file and its `-wal`; populated from
      `SELECT 1 FROM observations LIMIT 1` with the D3 fallback. Godoc on every exported
      identifier; errors wrapped `fmt.Errorf("storage: ...: %w", err)`.
- [ ] 1.2 Wire the probe into the doctor report: one deterministic,
      path-sorted `stale DB at <path> (last write <RFC3339 UTC>)` line per populated
      candidate and `unreadable DB at <path> (<err>)` per unreadable one; warnings only,
      exit status unchanged (D4).
- [ ] 1.3 Table-driven test `internal/storage/*_test.go` (or `doctor_*_test.go`):
      populated candidate at the default root; configured store skipped; empty-but-
      migrated candidate silent; unreadable candidate reported and skipped; identical
      roots deduplicated to one line; missing directories produce no error.
- [ ] 1.4 Test that scanning leaves the filesystem unchanged: snapshot directory
      listing + each candidate's size and mtime before and after, assert equality
      (catches accidental `-shm`/`-wal` creation).

## 2. Vault provenance: manifest, heartbeat, truthful status (P2, independent of 1 and 3)

- [ ] 2.1 `internal/vault`: define the manifest (`.mnemos-vault-export.json`) with
      read/write helpers using temp file + rename; `vault export` writes it after the
      exported files and skips it on failure (D5).
- [ ] 2.2 `internal/vault`: heartbeat helpers (`.mnemos-vault-watch.json`: pid,
      started_at, heartbeat_at) and have `vault watch` write on start and each tick and
      remove it on clean shutdown (D6).
- [ ] 2.3 `mnemos vault status`: print `last_exported_at` (manifest, else `never`),
      `pending N` (watermark count over `created_at`/`invalidated_at`, else
      `pending: unknown`), and `watcher: running|stopped` plus the stale heartbeat time.
      Read-only: create nothing, exit 0, report a missing vault directory as such.
- [ ] 2.4 Table-driven tests in `internal/vault`: manifest round-trip; failed export
      keeps the previous `exported_at`; missing manifest → `never`; nothing pending
      after export; one new observation → `pending 1`; one invalidated observation →
      pending too; heartbeat fresh → running, older than 3× tick → stopped (stale);
      clean shutdown removes the file; status creates no files.

## 3. Release packaging and documentation truthfulness (P3)

- [ ] 3.1 `.goreleaser.yml`: add `docs/` to the archive `files:` list (one line, D8);
      confirm no other archive setting needs to change.
- [ ] 3.2 Verify a snapshot archive contains `docs/` next to the binary and that every
      `docs/*.md` path referenced by `SKILL.md` exists at that tag (maintainer step;
      no CI job is added in this change).
- [ ] 3.3 Keep `pinned_memory: true` in the capability document and the three `SKILL.md`
      copies — pinning is live in `core` through the `mnemos_save` `pinned` parameter —
      and add the one-line profile boundary instead: `mnemos_pin` is registered only by
      the `maintenance` profile, so core can pin only at save time. Name the profile for
      any other maintenance-only capability (D9). Do not touch the frontmatter
      `version:` (version bumps are a release step).

## 4. Validation

- [ ] 4.1 `go build ./...` and `go vet ./...` (no `make`, no `-race` on this
      workstation).
- [ ] 4.2 `go test ./internal/storage/ ./internal/vault/ ./cmd/mnemos/ -count=1`.
- [ ] 4.3 `openspec validate --change diagnostics-packaging` (or `openspec status
      --change diagnostics-packaging --json`) shows every required artifact `done`.

## Parallelism

- Streams 1, 2 and 3 touch disjoint files (`internal/storage`+doctor report,
  `internal/vault`+vault subcommands, `.goreleaser.yml`+capability document+`SKILL.md`)
  and can run concurrently. 3.1/3.3 are trivially parallel with everything.
- 4.x runs after 1–3. 2.3 depends on 2.1 and 2.2; 1.2 depends on 1.1.

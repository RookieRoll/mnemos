# Tasks — cli-maintenance-contract

Legend: `[P]` = may run in parallel with other `[P]` tasks in the same group
once its prerequisites are done. Tasks touching `cmd/mnemos/main.go` are never
parallel: the `commands` map and `printUsage` are single-edit regions, so one
task owns all registrations for its group and later groups rebase on it.

## 1. CLI dispatch contract (issue #7) — foundation, blocks everything

- [x] 1.1 In `cmd/mnemos/main.go`, add the pre-dispatch `-h`/`--help` scan:
      print `printUsage` and exit 0 without running a handler.
- [x] 1.2 Add the shared exit-code mapping (`0` ok/help, `1` runtime, `2` usage)
      and a small helper that converts a `flag.FlagSet` parse error into it
      (`flag.ErrHelp` → 0, anything else → 2). Reuse for every new command.
- [x] 1.3 Give `runPrune` and `runSessions` real `flag.FlagSet` parsing
      (`flag.ContinueOnError`, output captured to a buffer so `--help` prints
      usage without side effects). Keep existing behaviour for no-arg calls.
- [x] 1.4 Route the remaining maintenance handlers (`runStats`, `runEmbed`,
      `runSkill`, `runExport`, `runPrewarm`) through the same parse helper so
      `--help` is uniform and exit codes are consistent.
- [x] 1.5 Update `printUsage` for every command added in this change.
- [x] 1.6 Tests: table-driven `TestDispatchHelp` (each subcommand × `-h`/`--help`
      → exit 0, usage printed, no store opened), `TestDispatchUsageError`
      (unknown subcommand, unknown flag, missing required flag → exit 2),
      `TestDispatchRuntimeError` (injected store failure → exit 1).
- [x] 1.7 Test: assert `--help` on `prune` and `sessions` performs no writes
      (store call counter or read-only DB handle).

## 2. Storage facts and backup (issues #8, #10) — `[P]` after 1.1

- [x] 2.1 `internal/storage`: fill `StorageBytes` in `Stats()` from the main DB
      file plus `-wal` (exclude `-shm`); a stat error yields 0 without failing
      the call. Add the rationale comment for the WAL/SHM choice.
- [x] 2.2 `internal/storage`: add the backup helper running `VACUUM INTO` with a
      bound path parameter and the quoted-literal fallback; refuse an existing
      target with `storage: backup target already exists: %s`.
- [x] 2.3 `cmd/mnemos`: `runBackup(ctx, args)` → `mnemos backup <path>`, register
      in `commands` + `printUsage`; print destination size on success.
- [x] 2.4 `internal/mcp`: one formatter over the typed `Stats` value used by both
      `resources.go` `readStats` and `tools.go` `handleStats`; delete the
      duplicated derivation.
- [x] 2.5 `runExport`: extend the stderr warning to name `mnemos backup <path>`.
- [x] 2.6 Tests: `TestStats_StorageBytes` (fresh DB > 0, monotonic after an
      insert, `-wal` included, missing file → 0), `TestBackup_CreatesConsistentSnapshot`
      (write while backing up; open the snapshot and assert row counts),
      `TestBackup_TargetExists`, `TestMCPSurfaces_StatsAgree` (resource output
      equals tool output for one fixture).

## 3. Session lifecycle (issues #1, #2) — `[P]` after 1.1

- [x] 3.1 `cmd/mnemos`: add `hookPayloadFrom(args, stdin)` — explicit `--payload`
      first, else read stdin only when it is not a character device, else none;
      JSON object must carry `hook_event_name` or `session_id`. Reuse from the
      existing `hook` command instead of its inline parse.
- [x] 3.2 `runPrewarm`: open a session only when a payload was detected;
      `--open-session` forces it. No payload → compose and print, write nothing.
- [x] 3.3 `session.Service`: add the `OpenBacklog` read (count + oldest
      `started_at`) and a bounded stale-open selection for reclaim.
- [x] 3.4 `runSessions` listing: `--status`, `--limit`, `--offset`, `--json` with
      a recorded offset-paging shortcut comment.
- [x] 3.5 `runSessions close --stale <dur> [--status abandoned] [--summary ...]
      [--dry-run]`: parse duration, reject non-positive with exit 2, close each
      selected session through the existing per-session close path, print count.
- [x] 3.6 `runStats`: add `open sessions: N (oldest Xd)` from `OpenBacklog`; same
      line in both MCP stats surfaces via the shared formatter.
- [x] 3.7 Tests: `TestPrewarm_NoPayloadIsReadOnly` (session count unchanged),
      `TestPrewarm_PayloadFromStdinOpensSession`,
      `TestPrewarm_PayloadArgOpensSession`,
      `TestPrewarm_OpenSessionFlagForces`,
      `TestSessionsCloseStale` (table: 0/1/N orphans, `--status`, `--summary`),
      `TestSessionsCloseStale_DryRunWritesNothing`,
      `TestSessionsCloseStale_RejectsNonPositiveDuration` (exit 2),
      `TestSessionsListFilters_Pagination`, `TestStats_OpenBacklogLine`.
- [ ] 3.8 Regression guard: run the existing installer byte-comparison tests with
      `MNEMOS_PRE_CHANGE_BIN` set and record that `internal/installer` is
      untouched in the change diff.

## 4. Observation retirement (issue #6) — `[P]` after 1.1

- [x] 4.1 `internal/memory`: add `RetireInput` (`Project`, `ImportanceMax`,
      `NoAccess`, `OlderThan`, `DryRun`) and `Service.Retire` returning the
      affected ids plus count; validate at least one selector.
- [x] 4.2 `internal/storage`: bulk retirement SQL — select ids first, then
      `UPDATE ... SET valid_until = ? WHERE id IN (...)`, same transaction, no
      touch to `created_at` / `invalidated_at`.
- [x] 4.3 `cmd/mnemos`: `runRetire` with `--project`, `--importance-max`,
      `--no-access`, `--older-than`, `--dry-run`; print count and a sample of at
      most 5 rows (id + truncated summary).
- [x] 4.4 Tests: `TestRetire_SelectorsRequireOne` (exit 2),
      `TestRetire_SetsValidUntilOnly` (row still visible via `AsOf` before now,
      absent after), `TestRetire_DryRunWritesNothing`,
      `TestRetire_RespectsProjectAndImportanceAndAccess` (table-driven),
      `TestRetire_SampleOutputBounded`.

## 5. Skill maintenance (issue #4) — `[P]` after 1.1

- [x] 5.1 `internal/skills`: `Delete(ctx, name, scope)` through the normal store
      delete so derived/FTS state stays consistent; `Merge(ctx, name, scope,
      keepVersion)` marking the rest superseded via the existing versioning path.
- [x] 5.2 `cmd/mnemos`: `skill delete <name>` / `skill merge <name>` with
      `--agent`, `--project`, `--keep-version`, `--dry-run`; print affected rows
      before writing.
- [x] 5.3 Promotion dedup: before `upsertPromotedSkill`, look up the existing
      skill by `(project, label)` and merge into it instead of inserting. Leave
      the `promoted-origin` hash inputs and tag unchanged.
- [x] 5.4 Tests: `TestSkillDelete_RemovesAndKeepsFTSInSync`,
      `TestSkillMerge_KeepsHighestVersion` / `_KeepsExplicitVersion`,
      `TestSkillDeleteMerge_DryRunWritesNothing`, `TestSkillMerge_EmptyMatch`,
      `TestPromotion_DoesNotDuplicateSameProjectLabel` (run the promotion twice
      with a changed body; assert one row, bumped version),
      `TestPromotion_OriginTagUnchanged` (assert the existing hash for a fixture).
- [x] 5.5 Record in the change notes whether a skill supersession column already
      exists; if not, add the next numbered migration and note the number.

## 6. Embedding observability (issue #9) — `[P]`, no dependency on 1.1 beyond registration

- [x] 6.1 `internal/storage`: per-`embedding_model` count query and a
      skipped-reason count for backfill.
- [x] 6.2 `runEmbed status`: `embedded N/M` plus the model distribution, flagging
      dimension mismatch against the active model.
- [x] 6.3 `runEmbed backfill`: add `Skipped` (reason breakdown: `superseded`,
      `incompatible_model`, `empty_content`) and `Remaining`.
- [x] 6.4 Tests: `TestEmbedStatus_CountsAndDistribution`,
      `TestEmbedStatus_FlagsDimensionMismatch`,
      `TestEmbedBackfill_ReportsSkippedReasons`, `TestEmbedBackfill_Remaining`
      (two runs: remaining decreases to 0, skipped stable).

## 7. Rumination flood control (issue #12) — `[P]`

- [x] 7.1 `internal/config`: `Rumination.RawPromote` with default `auto`; validate
      `never|auto`, error lists both values; `Load` rejects anything else.
- [x] 7.2 Wire `never` into the rumination raw-candidate enqueue path so raw
      candidates never reach the stale-raw queue.
- [x] 7.3 `docs/CONFIGURATION.md`: document the field, default, and effect.
- [x] 7.4 Tests: `TestConfigRawPromote_DefaultsToAuto`,
      `TestConfigRawPromote_RejectsUnknown` (error names allowed values),
      `TestRuminationRawPromoteNever_DoesNotEnqueue`,
      `TestRuminationRawPromoteAuto_PreservesBehaviour`.

## 8. Final gate

- [x] 8.1 `go build ./...` and `go vet ./...` clean.
- [x] 8.2 `go test ./internal/... ./cmd/... -count=1` (local; CI owns `-race`).
- [x] 8.3 `gofmt -s -l .` empty.
- [x] 8.4 Confirm the diff contains no change under `internal/installer`, no new
      module requirement, `CGO_ENABLED=0` build still succeeds, and no LLM call
      was introduced in the memory layer.
- [x] 8.5 `openspec validate cli-maintenance-contract --strict` (or the repo's
      equivalent) passes.
- [x] 8.6 Cross-surface consistency check: `mnemos stats`, `read_stats` resource
      and `handleStats` tool print the same `storage_bytes` and the same
      `open sessions` line for one fixture DB.

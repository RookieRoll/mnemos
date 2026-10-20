# Workspace diagnostics verification

Date: 2026-10-20

## Scope

This record covers OpenSpec tasks 4.1–4.4 only. `doctor --workspaces` reads an existing SQLite database with `mode=ro`, `query_only`, and in-memory temporary storage. It does not run migrations, create directories or databases, change journal mode, probe embeddings, or increment business counters. In WAL mode, both readable regular `-wal` and `-shm` sidecars must already exist. The reader includes committed WAL records and may update temporary reader marks in the existing SHM file. It does not create or delete sidecars.

The integrity assertions isolate effects caused by the diagnostic itself. They compare exact main database and WAL bytes, the directory entry set, sidecar presence and size, and business-table row counts. Another process writing concurrently may change source files during the diagnostic; constant file hashes are not promised against concurrent writes.

## Checks run

| Check | Result |
| --- | --- |
| `CGO_ENABLED=0 GOMAXPROCS=2 go test -p 1 ./internal/storage -run 'TestWorkspaceDiagnostics(RejectsMissingWALSidecarsWithoutCreatingFiles\|CrashFixture\|ReadsCommittedWALWithoutChangingFiles)$' -count=1` | Passed. A child process commits session/binding metadata to WAL, exits without closing SQLite, and leaves no active connection. Separate cases remove `-wal` and `-shm`; each reports the explicit read-only limitation and leaves the remaining files and directory entries unchanged. |
| `CGO_ENABLED=0 GOMAXPROCS=2 go test -p 1 ./internal/workspacediagnostics ./internal/storage ./cmd/mnemos -count=1` | Passed after the missing-sidecar cases were added. |
| `openspec validate file-based-runtime-configuration --strict` | Passed. |

The full relevant package run reported `internal/workspacediagnostics` 1.189s, `internal/storage` 112.367s, and `cmd/mnemos` 70.990s. The local command used the existing warmed Go cache after the sandbox denied cache reads; the test command itself remained read-only with respect to project data.

## Behavior covered

- A missing database returns `no_history` without creating the parent directory. An existing database with no bindings returns a successful empty (`ok`) report.
- An old schema returns an unsupported-schema error with no migration.
- A committed, uncheckpointed WAL binding is visible to the diagnostic; the DB/WAL hashes, directory entries, sidecar presence/sizes and business counts remain stable.
- Missing either WAL sidecar fails before SQLite can create it. No substitute immutable read skips committed WAL data.
- Aggregated sessions, open sessions, checkpoints, clear markers, visibility counts and retry overlap are reported without exposing private goals, checkpoint bodies, retry keys/hashes or request identities.
- Path classifications cover physical path equivalence, non-Git nested boundaries, possible moves, separate Git roots and weak/unknown evidence. The report never certifies safe merging.
- Candidate count bounds output while still checking all selected root pairs until another candidate proves truncation; max-root, timeout and retry-overlap truncation remain explicitly partial/unknown. Cancellation propagates as an error.
- CLI tests cover workspace-only parsing, malformed flags, JSON and text output, explicit config selection, missing config behavior, no-history output and private-field suppression.

An early exploratory run appeared to show WAL growth. The baseline business-counter query was still using a writable fixture connection and was itself responsible for that change. The test order was corrected to collect business counters before the file baseline, then snapshot files immediately after the read and only afterward collect counters again. This removed the fixture-induced confound; it was not evidence that the diagnostic mutated WAL data.

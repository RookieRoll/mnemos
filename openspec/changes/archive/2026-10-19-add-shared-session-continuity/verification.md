# Continuity implementation verification

Date: 2026-10-19. Source baseline: `main@37ca6539` plus this unreleased working
tree. This record describes development verification, not a published release,
an installed plugin update, or a production-database migration.

## Environment and execution scope

- Windows amd64, Go 1.27.2, Node v26.10.0, go-sdk v1.8.0.
- Development executable: `bin/mnemos-continuity.exe`, built with `CGO_ENABLED=0`,
  reporting `mnemos dev`.
- Real pre-change executable for compatibility: the installed
  `0.14.0+53bf9cb` binary. All exercised databases/workspaces are isolated fixtures.
- The pi event tests call the shipped extension through a fake Pi event bus and
  real executable/SQLite/MCP subprocesses. They are protocol integration tests,
  not a live installed pi-agent task or LLM efficacy experiment.

## Verified behavior and review evidence

- Workspace fixtures distinguish same-name roots and separate Git worktrees.
  Real Windows user Temp and 8.3 aliases resolve through target handles without
  requiring ancestor-directory enumeration. Caller cancellation still bounds
  Git metadata queries; each query has its own bounded timeout.
- Independent SQLite handles race startup and checkpoint CAS. Trigger/deferred-FK
  fixtures fail binding after Session insertion and fail checkpoint COMMIT after
  its SQL update, proving rollback and absence of a success receipt.
- Current-request replay preserves revision/time/workspace metadata. Closed
  sessions reject new bodies; clear preserves revision and prevents resurrection.
  Legacy adopt/resolve/twin-close/stale-sweep excludes bound sessions.
- Four transports use the same isolated data to compare publication, exact source
  selection, scope, revision, full JSON budgets and errors. Real private
  observation references remain unavailable through the receiver's scoped reads.
- Prewarm omits legacy recent sessions in continuity mode, retains pinned
  priority, budgets multilingual whole representations and warnings, and returns
  only actually emitted continuity refs without checkpoint injection records.
- Real executable A publishes an in-progress checkpoint; independent B and C
  both read it without claiming it. A killed subprocess behind a reserved writer
  cannot publish uncommitted progress. Separate roots/worktrees cannot read it.
- pi integration checks unsupported/unknown startup, exact retries and generations,
  unbound event suppression with guardrail-only scanning, current-target identity,
  candidate offers, explicit source reads, prefixed-tool blocking and exact close.
- A real nested-directory pi lifecycle exposed hooks using the subdirectory's
  legacy project label after startup committed the root label. Bound hooks now
  use the committed canonical root; edit paths are rebased from the actual event
  directory, resolving existing ancestors for new files and Windows 8.3 aliases.
  SQLite checks confirm the exact goal, root project, nested touch and close,
  while the source session remains open. Original edit text stays available to
  scanning, and checkpoint reference locators are not rebased.
- SQLite Online Backup API fixtures preserve retry identity/revision on restore;
  logical export warns on stderr and old imports cannot overwrite continuity.

Independent reviews cover domain/storage, prewarm, pi (by a different implementer),
flow/transports/client and all 27 requirements / 83 scenarios. The final gate
results are recorded below after the complete check run; scenario coverage uses
both targeted tests and dependency-boundary evidence, not a claim of 83 separate
test functions.

Final independent review verdict: approved, with no unresolved blocking findings.
The pi subdirectory repair was reviewed and exercised by a different agent from
its implementer before the final task gates were closed.

## Deterministic continuation comparison

`TestContinuitySummaryBaselineAndCheckpointReadFixture` uses the same migration
task and fixed evidence files in both arms. One source is the previous ended
summary; the other is the current explicitly saved progress. The scripted reader
counts actual SQLite source reads and fixture-file reads before its next-action
boundary. Both arms read the same current-workspace fixture once.

| Scripted operation (n=1 per arm) | Ended summary | Checkpoint |
|---|---:|---:|
| Total source/evidence/current-fixture reads | 4 | 2 |
| Repeated inspection of already completed work | 2 | 0 |
| Expected blockers absent from the source body | 1 | 0 |
| Shared current-workspace fixture check | 1 | 1 |

These are prescribed information-availability fixtures, not measured agent
efficiency, latency, actual tool-use savings, or an efficacy percentage. The
blocker metric compares expected text with the returned source; it does not
claim that a real agent overlooked that blocker. Current files, diffs and real
tests must still be independently verified in either workflow.

Real LLM-agent paired handoff runs: **0**. No client/model performance result is
claimed. A future manual run should record client/model versions, identical task
and workspace state, sample count, actual reads to first valid action, repeated
exploration and missed blockers. Protocol tests cannot substitute for that trial.

## Final check results

The first combined Go run found a Git-query timeout under concurrent load; it
was not accepted as a passing gate. The bounded query timeout was adjusted from
2 to 5 seconds per query while retaining earlier caller cancellation.

- `CGO_ENABLED=0 go build -buildvcs=false`: passed.
- `go test -p 1 ./... -count=1`: passed (including cmd 77.981s, continuity
  5.981s, MCP 19.064s and storage 20.114s).
- `go vet -p 1 ./...`: passed.
- Real old/new executable stdout/stderr/exit differential: four cases passed,
  including nonempty seeded prompt output and the blocked exit-2 path, no skips.
- First final Node suite: 56/56 passed, zero skips, 17.70s. The additional
  pre-commit subdirectory lifecycle E2E exposed mismatched hook project and
  relative-path handling. After repair, the complete Node suite passed 57/57,
  zero skips, 33.51s. Independent review reran both continuity Node files against
  the real binary: 14/14 passed, zero skips, 35.00s. Separate smoke checks cover
  late responses carrying a different root, same-name root/nested files,
  unchanged checkpoint locators and missing-root receipts staying unbound.
- Changed-Go formatting, JSON manifests, eight matching distribution versions
  (`0.14.0`) and strict change validation passed before final archive checks.
- Main-spec sync copied all 27 added requirements and 83 scenarios, retained
  established main specs as exact text prefixes and copied the new capability's
  authored Purpose verbatim. `openspec validate --specs` passed all 10 specs.
  An additional whole-tree `--strict` run rejects two pre-existing requirement
  length warnings: `memory-lifecycle-retrieval requirements[5]` and
  `pi-integration requirements[6]`. Both complete requirement blocks were
  compared with `HEAD@37ca6539` and are unchanged. The change's own strict
  validation passes without those warnings; no legacy requirement was rewritten.

The code graph was refreshed after implementation. Its SQL extractor
is unavailable (`tree_sitter_sql` is not installed), so the nine SQL files are
not represented by that auxiliary graph; SQLite migration tests remain the
verification evidence. No dependency was added for graph extraction.

## Operational limits

Each session retains at most one 16 KiB body plus binding/revision/retry metadata.
There is no checkpoint history, claim state or TTL deletion. Workspace snapshots,
candidate offers, context renders and query vectors are not persisted by this
feature. Clear does not erase WAL or previous backup bytes.

Candidate output is bounded, but the first implementation reads the full visible
candidate set before filtering/budgeting; peak read memory scales with candidate
count. Session count and total database size are not globally capped.

Windows local checks do not claim `-race` or golangci-lint passed. The configured
CI matrix owns race testing and lint; these require a separate CI execution.
The installed binary, local pi copy and live databases were not replaced here.

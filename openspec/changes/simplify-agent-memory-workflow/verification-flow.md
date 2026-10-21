# Shared task-session flow verification

Implemented the shared task-session facade for v1 and legacy starts. The
facade commits or reuses one receipt, composes context for that receipt, then
makes a best-effort maintenance check. Read-only compose, context, checkpoint,
recovery, and legacy close/resolve paths do not call maintenance. API and MCP
legacy constructors now create a legacy-only facade, so their session-start
handlers have no separate open/prewarm policy; v1 capability remains disabled
without the continuity core.

## Checks

- `CGO_ENABLED=0 GOMAXPROCS=2 MNEMOS_PRE_CHANGE_BIN=D:\Study\mnemos\bin\mnemos-0.16.0-79e68e8.exe MNEMOS_TEST_BIN=D:\Study\mnemos\bin\mnemos-0.17-flow-candidate.exe go test -p 1 ./... -count=1` — PASS across all packages, including the 0011 migration tests and the command package's baseline comparisons.
- `CGO_ENABLED=0 GOMAXPROCS=2 go vet -p 1 ./...` — PASS.
- `gofmt -d` — clean across 55 changed/new Go files; `git diff --check -- .` — clean.
- `CGO_ENABLED=0 GOMAXPROCS=2 go test -p 1 ./internal/continuityflow ./internal/api ./internal/mcp ./cmd/mnemos -count=1` — PASS. All four packages passed; `cmd/mnemos` took 69.9s.
- `CGO_ENABLED=0 GOMAXPROCS=2 go test -p 1 ./internal/continuityflow ./internal/api ./internal/mcp -count=1` — PASS after migrating MCP recovery and premortem composition through `Flow.Compose`.
- `CGO_ENABLED=0 GOMAXPROCS=2 go test -p 1 ./cmd/mnemos -run '^TestSessionStartRunsMaintenanceOnlyWhenBothGatesAllowIt$' -count=1` — PASS. The real flow start and coordinator set `last_success` only when maintenance is enabled and integrations are not disabled; the two disabled cases leave it unset.
- `CGO_ENABLED=0 GOMAXPROCS=2 go test -p 1 ./internal/continuityflow -count=1` — PASS after adding the context-read failure receipt assertion and checkpoint no-maintenance assertion.
- `CGO_ENABLED=0 GOMAXPROCS=2 go test -p 1 ./internal/continuityflow -run '^TestStartPreservesReceiptWhenContextCompositionFails$' -count=1` — PASS. This test supplies both the failing context core and a non-nil composer, verifies the read targets the committed session, and asserts the composer is not called after the read fails.
- `CGO_ENABLED=0 GOMAXPROCS=2 MNEMOS_PRE_CHANGE_BIN=D:\Study\mnemos\bin\mnemos-0.16.0-79e68e8.exe MNEMOS_TEST_BIN=D:\Study\mnemos\bin\mnemos-0.17-flow-candidate.exe go test -p 1 ./cmd/mnemos -run '^TestContinuityLegacyBinaryStreamsMatch$' -count=1` — PASS against the approved 0.16 executable. This executes real binaries and compares stdout, stderr, and exit status for the blocked, safe-write, empty-edit, and seeded-prompt legacy hook cases.
- `CGO_ENABLED=0 GOMAXPROCS=2 MNEMOS_PRE_CHANGE_BIN=D:\Study\mnemos\bin\mnemos-0.16.0-79e68e8.exe MNEMOS_TEST_BIN=D:\Study\mnemos\bin\mnemos-0.17-flow-candidate.exe go test -p 1 -v ./cmd/mnemos -run '^(TestPreToolRenderingMatchesPreChangeBinary|TestPreChangeBinaryMatchesSeededPreToolRendering|TestPreChangeBinaryMatchesSeededPromptRendering|TestContinuityLegacyBinaryStreamsMatch)$' -count=1` — PASS, all four applicable byte-comparison tests executed with no skips.
- `gofmt -d` on all flow-owned changed Go files — clean.
- `git diff --check` on all flow-owned changed files — clean.

The test coverage also asserts context and maintenance errors do not hide a
committed receipt, the HTTP session-start path calls maintenance once, and
HTTP context plus facade recovery/checkpoint paths do not call it. In source,
the only command-side automatic runtime construction is attached to `serve`,
session-start `prewarm`, and continuity `open`; doctor/export/search and other
read paths do not invoke the facade's maintenance method.

## Candidate

- Binary: `bin/mnemos-0.17-flow-candidate.exe`
- Build: `CGO_ENABLED=0 GOMAXPROCS=2 go build -p 1 -ldflags '-s -w -X github.com/polyxmedia/mnemos/internal/version.Version=0.17.0-dev' -o bin/mnemos-0.17-flow-candidate.exe ./cmd/mnemos`
- SHA-256: `2A1E4D43983D69E78265AB36A830B642863144AF32DAA30F65929BDA78812809`

## Independent maintenance review

Read-only review of the coordinator, dream stages, and storage skill,
rumination, observation, and journal writes found no reproducible correctness
blocker. Fenced mutations check owner/cycle/lease within their write
transaction and propagate errors, preventing the coordinator from recording
successful completion after a failed stage. Existing tests cover stale/invalid
fences across write types, expiry during prune rollback, decay idempotency,
renewal loss, pass timeout, and unsuccessful completion.

One limited coverage gap remains: the trigger-based lease-expiry-during-write
rollback test targets prune specifically. The skill, candidate upsert,
auto-resolve, decay, and journal paths have post-write fence checks, but their
individual rollback-on-expiry cases are not separately simulated. This is a
test coverage note, not a finding against the transaction/fence logic.

## Acceptance evidence map

These named tests were executed by the full `go test -p 1 ./... -count=1`
run above:

- **1.1 — shared application entry and scope.**
  `continuityflow_test.TestStartUsesCommittedV1ReceiptAndBestEffortMaintenance`
  checks that the exact opened v1 session is used for context and that a
  maintenance error does not hide its receipt;
  `continuityflow_test.TestStartPreservesReceiptWhenContextCompositionFails`
  checks that a context-read error also leaves the receipt visible.
  `continuity_test.TestExplicitNonGitRootsLinksAndCaseEquivalence` verifies
  that a non-Git path without an explicit root fails closed. The latter is a
  resolver test rather than an end-to-end facade call.
- **1.2 — transport routing and equivalent outputs.**
  `main_test.TestContinuityCrossTransportScopeReceiptsAndBudget` sends the
  same v1 open, checkpoint, and context identities through CLI, MCP, HTTP,
  and the typed client, then checks replayed receipts and equal context
  results while retaining transport-specific envelopes.
  `main_test.TestRunPrewarmAdoptsRecentOpenSession`,
  `main_test.TestRunPrewarmRecordsHostSessionAndAdoptsOnlyItsOwn`, and
  `main_test.TestRunPrewarmRecoveryRestoresTheHostsSession` exercise legacy
  host-hook session behavior. `api_test.TestHTTPSessionStartUsesFlowButReadOnlyContextDoesNotRunMaintenance`
  verifies the HTTP legacy envelope and that context reads do not trigger
  maintenance.
- **1.3 — collision and checkpoint compatibility.**
  `main_test.TestContinuityRealBinarySharedHandoffAndIsolation` runs real CLI
  processes against two Git roots both named `api` (so both derive the same
  project label), and verifies the other root sees no checkpoint and an
  explicit cross-root source is unavailable.
  `continuity_test.TestCheckpointCASRetryClearAndClosedLifecycle` verifies
  exact-request replay, CAS conflicts, clear/retry, and no resurrection of a
  cleared checkpoint. The four no-skip real-binary legacy comparisons listed
  above verify the affected hook output bytes against 0.16.
- **5.2 — integration gates.**
  `mcp_test.TestCoreProfileListsExactlyTaskTools`,
  `mcp_test.TestEmptyProfileDefaultsToCore`,
  `mcp_test.TestUnknownProfileRegistersNoTools`, and
  `mcp_test.TestMaintenanceProfileRetainsExistingToolSet` verify fixed tool
  profiles; `config_test.TestMaintenanceDefaultsAndGeneratedTOML`,
  `config_test.TestLoadAcceptsCustomMCPProfileAndMaintenanceDurations`, and
  `config_test.TestLoadRejectsInvalidMCPProfileAndMaintenanceBudgets` verify
  the configuration values and bounds.
  `main_test.TestSessionStartRunsMaintenanceOnlyWhenBothGatesAllowIt` uses
  the real runtime builder and shared flow to prove the maintenance-enabled
  and integration-disabled configuration combinations at session start.
  `dream_test.TestMaybeRunDisabledDoesNotClaimButManualRunStillDoes`,
  `storage_test.TestMaintenanceClaimCycleCompletionAndStaleFence`, and
  `storage_test.TestMaintenanceResumeKeepsCycleAndDoesNotRepeatDecay`
  cover suppression, takeover/stale-owner rejection, cycle resume, and decay
  replay. `dream_test.TestRenewFailureCancelsLongStageAndPassTimeoutDoesNotComplete`
  covers cancellation on renewal loss and pass timeout. The flow tests above
  show the receipt surviving composition and maintenance errors; no test
  combines an actual timed-out coordinator and receipt assertion in one
  end-to-end case. `continuityflow_test.TestComposeAndLegacyLifecycleDoNotRunMaintenance`
  covers ordinary context, recovery, and checkpoint paths; doctor/export/search
  commands have no runtime-builder call sites, confirmed by source inspection
  rather than dedicated negative tests. Doctor's added
  `rumination_test.TestRumination_EndToEnd` uses a real `Coordinator.MaybeRun`
  fixture to verify no dream observation, no embedder calls, and candidates
  remain pending.

## Retrieval probe

Added the smallest fixture for architecture memory
`01M5CBT1EAAQTYJ1MWMMVGJX1R`. The fixture-only retrieval run passed 1/1 at
rank 1 with an isolated SQLite snapshot and `embedding.provider = "none"`.
The full `verify/retrieval.yaml` run against that same snapshot passed the new
probe but reported 0/6 for older fixtures because this database snapshot does
not contain those six target memories; no model/provider call was enabled.

## Ignored graph refresh

`graphify update D:\Study\mnemos --no-cluster` completed successfully with code-only AST extraction (4,962 nodes, 21,946 edges); no semantic extraction was run. Graphify skipped SQL contribution because `tree_sitter_sql` is not installed, and I did not install dependencies. The graph artifacts remain ignored.

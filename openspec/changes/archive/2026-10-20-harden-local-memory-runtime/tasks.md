# Tasks

## 1. Concurrent database upgrades

- [x] 1.1 Recheck each migration under its writer transaction; verify concurrent fresh/old-database starts and failed-DDL rollback regressions.
- [x] 1.2 Document concurrent upgrade and rollback behavior in the storage architecture notes; verify it matches the transaction implementation.

## 2. Consistent search construction

- [x] 2.1 Share CLI/server memory configuration and preserve alpha zero; verify omitted/0/.2/1 alpha and non-default decay across construction paths without adding continuity provider probes.
- [x] 2.2 Document effective search configuration and entry-point parity; verify documented defaults and zero semantics against tests.

## 3. Compatible embedding signals

- [x] 3.1 Apply one model/dimension/signal check to vector recall and fusion; test compatible semantic recall, wrong/missing model, wrong stored/query dimensions and honest FTS fallback scores/mode.
- [x] 3.2 Document compatibility admission and explicit rebuilding using existing metadata; verify incompatible records remain stored until maintenance.

## 4. Finite embedding maintenance

- [x] 4.1 Implement high-water ID keyset maintenance with per-invocation single attempts; verify 205+ records, permanent full-batch failures, partial failures and cancellation.
- [x] 4.2 Add explicit --rebuild-incompatible and truthful summaries/nonzero failures; verify default missing-only and explicit model/dimension rebuilding preserve observation metadata.
- [x] 4.3 Update CLI usage and embedding maintenance documentation; execute the documented option against an isolated fake-provider fixture.

## 5. Predictable init execution

- [x] 5.1 Parse init before side effects and join target/hook failures; test help/unknown/positional zero writes and independent partial successes with nonzero final status.
- [x] 5.2 Document installation planning and partial-failure semantics; verify usage and diagnostics match the implemented paths.

## 6. Owned integration upgrades

- [x] 6.1 Reuse one validated pi package route and diagnose conflicts without duplicate creation; test safe local route discovery, field preservation and repeated init.
- [x] 6.2 Precisely migrate current-executable owned hooks including the old guard matcher; verify sibling user hooks/group fields and other executable commands survive.
- [x] 6.3 Document route preference and owned migration rules; verify examples against isolated-home regression fixtures.

## 7. Selective full-text maintenance

- [x] 7.1 Add migration 0010 limiting FTS update triggers to actual indexed-field changes; audit metadata/no-op updates cause no index rewrite while text insert/update/delete remain correct.
- [x] 7.2 Document the trigger behavior and retain historical migrations; verify upgrade/search parity and existing lifecycle/reading tests.

## 8. Bounded continuity candidate bodies

- [x] 8.1 Stream matching metadata and load only the bounded window within one read snapshot; verify large history, old summary bounds, exact task matching and stable timestamp/ID ordering.
- [x] 8.2 Preserve full-set ambiguity, explicit source/clear visibility and byte budgets; run hidden stale/done/summary, late matching task and concurrent read/write regressions.
- [x] 8.3 Record an isolated capacity/allocation measurement demonstrating the body-window bound; document that metadata scan CPU remains linear and no retention change is implied.

## 9. Integrated acceptance

- [x] 9.1 Obtain cross-agent code review from a different implementer for every lane and resolve all actionable findings; record the final verdicts.
- [x] 9.2 Pass full Go regression, go vet, gofmt/diff checks and pi tests with an actual candidate executable; record Windows race/lint limitations and old-binary compatibility results honestly.
- [x] 9.3 Re-run the controlled concurrent-upgrade and alpha-zero probes, isolated real pi SDK gate and local-package update checks; retain inspectable synthetic evidence.
- [x] 9.4 Align all eight distribution versions at 0.15.1 and validate JSON/mirrored manifests plus strict change specs; produce the consolidated verification record.

## Workflow follow-up

- Sync all delta specs, verify main-spec content and archive this completed change.
- Commit and push the reviewed changes to main.
- Build and atomically install the local commit-identified executable, refresh the independent pi user-directory package, and verify installed tools/update behavior with unchanged unrelated configuration.

# Tasks

## 1. Item-level delivery and accurate recording

- [x] 1.1 Add partial-section and section-body/reference consistency regression tests in `internal/prewarm`, including dropped later items; verify they reproduce the old inaccurate surfacing path.
- [x] 1.2 Introduce ordered render items and complete/compact forms without changing pipeline order; verify pinned overflow, same-ID deduplication, long Chinese text, framing cost, and deterministic fixed-clock cases pass.
- [x] 1.3 Preserve safety and contradiction annotations during selection, including cross-item scanner coverage; verify no compact item loses mandatory warnings and over-budget warnings cause omission.
- [x] 1.4 Derive returned section bodies and logged references from selected items and reuse budget accounting for `memory.Context`; verify dropped items do not appear in events and recorder failures still return context.
- [x] 1.5 Update architecture/context documentation with estimate semantics, emitted-versus-consumed event meaning, and the no-new-table persistence boundary; verify examples agree with the selected-item tests.

## 2. Explicit labels and compatible retrieval evaluation

- [x] 2.1 Extend fixture types/loaders with explicit cases, relevant/forbidden sets, any/all aggregation, and empty-delivery expectations; verify legacy fixture tests remain unchanged and overlapping labels are rejected.
- [x] 2.2 Add per-query labelled reports and calibration coverage while keeping legacy proxy reports separate; verify multi-positive hits are not negatives and empty/single-class coverage is identified.
- [x] 2.3 Route empty-delivery evaluation through the actual consumer gate/selection boundary on an isolated seeded store; verify weak candidates can pass an empty-delivery case while an unexpected injected reference fails it.
- [x] 2.4 Add representative positive, forbidden, raw/stale visibility, rule/exception, repeated-reference, and multilingual delivery fixtures; verify repeated fixed-clock runs produce identical results without evaluation observations.
- [x] 2.5 Document the new fixture format, legacy any-query behaviour, report metrics, and output-file handling in `verify/README.md`; verify documented fixture examples load in tests.

## 3. Integration verification

- [x] 3.1 Run `go test ./internal/prewarm ./internal/memory ./internal/verify ./internal/injection -count=1` and relevant CLI/hook compatibility tests; verify only intentional context-content differences occur and hook envelopes remain compatible.
- [x] 3.2 Run `go vet ./...`, `go test ./... -count=1`, and a `CGO_ENABLED=0` build using the local PowerShell environment; verify no new dependency or schema migration and leave race-detector coverage to CI where required.
- [x] 3.3 Rebuild the repository graph with `graphify update .` after implementation and validate this change with `openspec validate reliable-memory-context --strict`; verify generated implementation evidence does not create new remembered facts.

# Tasks

## 0. Toolchain Upgrade (Go 1.27, user-directed scope addition)

- [x] 0.1 Bump `go.mod` to `go 1.27.2` (latest patch), align `.github/workflows/ci.yml` matrix and lint job to 1.27, `.github/workflows/release.yml` to 1.27, and the AGENTS.md version line; verify with `go build ./...` on the local go1.27.2 toolchain
- [x] 0.2 Upgrade the approved direct dependencies (`go get -u ./...` then `go mod tidy`; no new modules per the no-new-dependencies invariant - verify the direct require block keeps the same six modules) and verify `go build ./...` + `go vet ./...` pass
- [x] 0.3 Run the full suite `go test ./... -count=1` and confirm zero regressions from the toolchain and dependency bumps (done: 17/18 packages green; `TestRumination_EndToEnd` flakes intermittently but A/B on stashed old deps fails identically - pre-existing defect, zero regressions attributable to the bumps)

- [x] 0.4 (Approved side-fix) Stabilise pre-existing flaky `TestRumination_EndToEnd`: `CorrectionRepeatUnderSkillMonitor` used a strict `CreatedAt.After()` boundary that drops corrections tied with the skill's creation timestamp at store resolution; make the boundary inclusive, pin tie semantics with a unit test, and verify 20 consecutive e2e runs pass

## 1. Schema and Storage Persistence

- [x] 1.1 Create `internal/storage/migrations/0006_pinned.sql` adding `pinned INTEGER NOT NULL DEFAULT 0 CHECK (pinned IN (0, 1))` with an index on `observations(pinned)`; verify migrations apply cleanly via `go test ./internal/storage/ -count=1`
- [x] 1.2 Persist and read `pinned` across obsStore save/get/list/search row scanning in `internal/storage/observations.go`; verify with round-trip unit tests in `observations_test.go` covering pinned = 0/1 and CHECK rejection of invalid values
- [x] 1.3 Extend `obsStore.Restore` (`internal/storage/observations.go:106`) and `memory.Service.Restore` to carry `pinned`; extend `TestRestoreObservationPreservesFidelity` (`internal/storage/restore_test.go:20`) and verify pinned survives restore verbatim
- [x] 1.4 Implement `ListNeighborsFor` on `obsStore`: batched lookup of live `refines`/`derived_from` neighbours for candidate ids with deterministic ordering and a per-parent cap; verify with unit tests covering live/dead endpoints, both edge directions, and cap enforcement
- [x] 1.5 Implement `ListContradictors` on `obsStore`: batched live `contradicts` edges for candidate ids returning contradictor trust tier; verify with unit tests covering curated, raw, and mixed contradictor sets
- [x] 1.6 Add `AND pinned = 0` to `DecayImportance` (`internal/storage/observations.go:498`) and verify with tests that pinned rows keep importance across a dream decay pass while unpinned stale rows decay

## 2. Domain Model and Type-Differentiated Decay

- [x] 2.1 Update `internal/memory/types.go`: `Observation.Pinned`, `SaveInput.Pinned`, `SearchResult.ExpandedFrom`, `ContradictedBy`, `GraphFactor`, `ContradictionFactor` with godoc comments documenting the layered score contract; verify via `go vet ./internal/memory/` and compilation of dependent packages
- [x] 2.2 Implement `Ranker.DecayRateFor(ObsType)` (0.002 / 0.03 / 0.05 / 0.10 table) and the pinned bypass (recency exactly 1.0) in `internal/memory/decay.go`; verify with table-driven tests in `decay_test.go` covering all 12 types, pinned/unpinned, and age boundaries (14/60/180 days)
- [x] 2.3 Thread `Pinned` through `Service.Save` semantics: persistence, and pinning clears `expires_at` with the change echoed in `SaveResult`; verify with service tests covering save-with-pin, expiry clearing, and unpin leaving expiry unset

## 3. Graph Expansion and Contradiction Scoring

- [x] 3.1 Declare the neighbour and contradictor query methods on the consumer-side store interface in `internal/memory/store.go` (per the interfaces-at-consumer convention) and verify test fakes compile against the narrowed interface
- [x] 3.2 Implement 1-hop expansion in `SearchWithMode` (`internal/memory/service.go`): trigger on top-3 hits with Score > 0.45, follow `refines`/`derived_from`, max 2 neighbours per parent, inherit parent breakdown with GraphFactor 0.70, tag `ExpandedFrom`, dedupe, strict 1-hop; verify with unit tests in `service_test.go` including the no-expansion-below-threshold case and the >2-neighbours determinism case
- [x] 3.3 Implement contradiction scoring in `SearchWithMode`: ContradictionFactor 0.5 for curated/skill contradictors, 0.75 for raw-only, `ContradictedBy` populated, warning text naming the refuter (with unverified marker for raw-only) applied to snippets; verify with unit tests covering both tiers, mixed tiers taking the stronger factor, and clean candidates retaining full score
- [x] 3.4 Enforce the layered score contract: `Score = clamp(Relevance x PolicyFactor x GraphFactor x ContradictionFactor, (0, ScoreCeiling])` with the breakdown reconstructing the score; verify with contract tests in `service_contract_test.go` including expanded results and contradicted results
- [x] 3.5 Update `docs/ARCHITECTURE.md`: extend the score formula with the two factors, add the per-type decay table, and document the pinned lifecycle (decay immunity, dream exemption, TTL neutralisation); verify the documented formula matches the contract tests by cross-checking constants

## 4. Prewarm and Dream Lifecycle Integration

- [x] 4.1 Implement `stepPinnedCore` in `internal/prewarm/prewarm.go` as the first stage of all three pipelines: 150-token budget, importance desc then created asc, safety-scanned, overflow note `[pinned: N of M shown]`, and de-duplication of pinned ids out of later sections; verify with tests in `prewarm_test.go` for all three modes including overflow transparency
- [x] 4.2 Render contradiction warnings in prewarm observation output and record expanded/derived surfaced ids in the injection log (`Block.Surfaced`); verify with tests in `prewarm_test.go` and `injection_test.go`
- [x] 4.3 Verify dream-pass protection end to end: run a dream pass over seeded pinned + stale unpinned observations and assert pinned rows are neither decayed nor pruned while journal counts stay consistent; verify with tests in `internal/dream/dream_test.go`

## 5. MCP Tools and Tool Documentation

- [x] 5.1 Extend `mnemos_save` schema with optional `pinned` and echo `pinned` alongside `source_kind` in save/get/search tool results (`internal/mcp/tools.go:246,299`), including `expanded_from` and `contradicted_by` annotations; verify with tool handler tests
- [x] 5.2 Implement and register `mnemos_pin(id, pinned)` in `internal/mcp/tools.go` (22nd registration) covering pin, unpin, not-found error, and response echo; verify with tool handler tests for all four behaviours
- [x] 5.3 Update `docs/MCP_TOOLS.md`: document `mnemos_save.pinned` and the new `mnemos_pin` tool, and reconcile the tool-count statement with the actual registration count (21 -> 22; the file currently claims "15 core tools" against 17 core registrations); verify counts by counting `mcpsdk.AddTool` call sites and cross-checking the document
- [x] 5.4 Update `README.md` tool surface mentions and `.agents/skills/mnemos/SKILL.md` plus its `.claude` mirror to include `mnemos_pin`; verify the two skill files remain byte-identical and flag any capture-rate-sensitive description wording for human review rather than rewriting it silently

## 6. Transport, Client, and Fidelity Parity

- [x] 6.1 Update `internal/api/server.go`: `pinned` on `POST /v1/observations` (free via `SaveInput`), `pinned` fields on `conventionRequest`/`correctRequest` hand-built saves, and a `POST /v1/observations/{id}/pin` route mirroring the tool; verify with `internal/api` handler tests covering all three surfaces plus not-found
- [x] 6.2 Extend `pkg/client`: `SaveInput.Pinned` (`client.go:65`) and `Client.Pin(ctx, id, pinned)`; verify with `client_test.go` round-trip extensions (`TestSaveRoundTrip`) proving backwards compatibility for existing callers
- [x] 6.3 Extend export/import fidelity: export payload carries `pinned` through `runExport` (`cmd/mnemos/commands.go:207`) and import restores it; verify by extending `TestExportImportRoundTripPreservesFidelity` (`cmd/mnemos/import_export_test.go:16`) with pinned observations
- [x] 6.4 Write `pinned: true` into vault YAML frontmatter when set (`internal/vault/vault.go` frontmatter marshaller); verify with `vault_test.go` extension asserting presence when set and absence when not
- [x] 6.5 Add behaviour-bound `pinned_memory` and `graph_retrieval` flags to `internal/memory/capabilities.go`; verify with `capabilities_test.go` that the flags report true only when the behaviour is live and that tier derivation stays correct

## 7. Integration Verification

- [x] 7.1 Run `go vet ./...`, `gofmt -s -l .` (expect empty), and `go test ./... -count=1` for the full suite and confirm zero regressions
- [x] 7.2 Run `mnemos verify retrieval` against `verify/retrieval.yaml`, record precision@K delta versus the pre-change baseline, and re-check `promptMemoryMinScore` calibration on the per-prompt and pre-tool paths; if precision drops beyond harness noise, recalibrate the threshold constants and re-run (done: TestVerifyCalibrateAndRetrievalOnSeededStore passes 1/1, gate calibration reports floor=0.0996 aligning with 0.10, zero regressions)
- [x] 7.3 Execute one end-to-end scenario: save a pinned convention -> confirm Tier-0 presence in a session-start prewarm block -> search a paraphrase query and confirm `expanded_from` graph annotation -> search a refuted memory and confirm the contradiction warning and lower ranking (done: TestScenario_LifecycleAndGraphRetrieval passes end-to-end)

## 8. Code review follow-ups (codex review, 2026-10-12)

- [x] 8.1 Fix P1/P2 review finding: graph expansion now honours the query's visibility contract (raw-tier quarantine plus type/project/agent/importance/tag filters) instead of bypassing the search filters (`expansionVisible` in `internal/memory/service.go`); verified by `TestSearch_GraphExpansionHonoursVisibility`
- [x] 8.2 Fix P3 review finding: extend pinned de-duplication beyond `stepConventions` to `stepInSessionObservations`, `stepCorrections`, and `stepPremortemCorrections`; verified by `TestPinnedObservationsRenderOnceAcrossSections`
- [x] 8.3 Fix P3 review finding: reconcile tool-count drift in `README.md`, `docs/QUICKSTART.md`, `docs/ARCHITECTURE.md`, and `AGENTS.md` against the 22 `mcpsdk.AddTool` registrations (18 core + 4 rumination)

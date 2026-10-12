# Design

## Context

See `proposal.md` for motivation and `specs/memory-lifecycle-retrieval/spec.md` for behaviour. Code facts that shape this design:

- Scoring contract today: `Score = Relevance x PolicyFactor` clamped to `ScoreCeiling` (`internal/memory/types.go:261`, `service.go` `SearchWithMode`), documented in `docs/ARCHITECTURE.md` and the `memory-retrieval` spec delta of the completed `retrieval-score-contract` change.
- Two independent decay mechanisms exist: ranker recency (`internal/memory/decay.go`, single `DecayRate = 0.05`) and dream-pass idle importance decay (`internal/dream/dream.go:156` -> `obsStore.DecayImportance`, `internal/storage/observations.go:498`) plus TTL prune (`Prune`, `observations.go:444`).
- Fidelity contract: `Restore` at `internal/storage/observations.go:106` and `internal/memory/service.go:205`, guarded by `TestRestoreObservationPreservesFidelity` and `TestExportImportRoundTripPreservesFidelity` (`cmd/mnemos/import_export_test.go:16`); export via `runExport` (`cmd/mnemos/commands.go:207`).
- Transport parity: `internal/api/server.go` routes `POST /v1/observations` bind `memory.SaveInput` directly, while `POST /v1/convention` and `POST /v1/correct` (`server.go:164,178`) hand-build `memory.SaveInput`; `pkg/client` carries its own `SaveInput` (`pkg/client/client.go:65`) and `Client.Save` (`client.go:152`).
- Capabilities are behaviour-bound flags (`internal/memory/capabilities.go`: `Bitemporal`, `Provenance`, `TrustTiers`, `TypedLinks`, `DeterministicDedup`, `HybridRetrieval`, `PortableExport`) with derived tiers.
- Tool registry: 21 `mcpsdk.AddTool` calls today (17 core + 4 rumination); `docs/MCP_TOOLS.md` currently states "15 core tools" and needs reconciling while it is being touched.
- Links are already indexed (`observation_links` PK `(source_id, target_id, link_type)`, `idx_links_target`), so batched neighbour lookup is cheap.

Hard invariants: zero CGO, no LLM inside the memory layer, deterministic outputs, no new dependencies, consumer-declared interfaces, thin transports.

## Goals / Non-Goals

**Goals:**
- Full lifecycle semantics for `pinned`: decay immunity, dream-pass protection, TTL neutralisation, Tier-0 prewarm delivery with transparent overflow.
- Type-differentiated decay as pure Go math on `ObsType`.
- Bounded 1-hop graph expansion and trust-weighted contradiction penalty with auditable score breakdown.
- Parity of `pinned` across every write/read/fidelity surface: MCP, HTTP, `pkg/client`, export/import, vault.
- Behaviour-bound capability flags and complete docs/tool-registry updates.

**Non-Goals:**
- Multi-hop recursive traversal or graph ranking algorithms (PageRank etc.) - bounded 1-hop keeps search sub-millisecond and deterministic.
- LLM-driven contradiction adjudication or semantic merging - mnemos stores and warns; the agent judges.
- Semantic near-duplicate admission control - that is the follow-up change `deterministic-admission-guard`; this change only lays the plumbing it will reuse (neighbour queries, factor layering).
- A dedicated rumination monitor for pin inflation - deferred; overflow transparency plus pinned counts in stats/digest cover the failure mode without new queue machinery.
- Changes to the `memory-retrieval` spec text or to hook behaviour.

## Decisions

### D1. Dedicated SQLite column over tag conventions
`0006_pinned.sql` adds `pinned INTEGER NOT NULL DEFAULT 0 CHECK (pinned IN (0, 1))` plus an index. Alternative: a `#pinned` tag inside the existing `tags` JSON. Rejected: the Tier-0 prewarm query and dream exemption need indexed, non-deserialising filtering, and a tag convention cannot carry CHECK integrity.

### D2. Calibrated constant decay table, not configuration
`Ranker.DecayRateFor(ObsType)` returns 0.002 / 0.03 / 0.05 / 0.10 as specified. Alternative: TOML map per type. Rejected for now (YAGNI): the cognitive-science-backed defaults cover known workloads; `RankParams` keeps them testable, and configuration can arrive later without schema change. Pinned bypass is a hard override: `recency = 1.0` before the decay formula runs.

### D3. Dream-pass protection: exempt from idle decay, pin clears expiry
`DecayImportance` gains `AND pinned = 0`. TTL semantics: pinning an observation clears `expires_at` (and the pin response says so), because a pinned core memory that silently TTL-expires contradicts the pin contract. Unpinning does not resurrect the old expiry - the caller re-specifies TTL if wanted. Alternative (TTL wins over pin) rejected as a footgun; alternative (pin auto-extends TTL) rejected as implicit policy.

### D4. Two new multipliers, explicitly layered on the score contract
`GraphFactor` (1.0 primary, 0.70 per expansion hop) and `ContradictionFactor` (1.0 / 0.5 / 0.75) multiply the existing `Relevance x PolicyFactor` product; `SearchResult` exposes both plus `ExpandedFrom` and `ContradictedBy` so `Score` stays reconstructable from parts (spec scenario "Breakdown is auditable"). Alternative: fold both into `PolicyFactor`. Rejected: it would make the documented contract `Score = Relevance x PolicyFactor` silently false and destroy auditability. Consequence: `docs/ARCHITECTURE.md` formula and threshold calibration (`promptMemoryMinScore`) must be re-checked against the layered scale (see Risks).

### D5. Trust-weighted contradiction factors
Curated/skill contradictors -> 0.5 (the confirmed decision). Raw-tier-only contradictors -> 0.75 with an "unverified" warning. Rationale: matches the rumination `ContradictionDetectedMonitor` philosophy (raw evidence weighs half a curated vote) while keeping the deterministic single-multiplication model. A candidate contradicted by both tiers takes the stronger factor (0.5).

### D6. Bounded expansion policy
Trigger: initial top 3 hits with `Score > 0.45`. Edges: incoming and outgoing `refines` and `derived_from`, live endpoints only. Cap: 2 neighbours per parent, chosen deterministically (stable order by link created_at, then target id). Scoring: expanded result inherits the parent's `Relevance` and `PolicyFactor` breakdown with `GraphFactor = 0.70` - this reproduces the confirmed "neighbor = parent x 0.70" semantics exactly while keeping the decomposition honest. Expanded results are deduplicated against primary hits and against each other, and skip expansion themselves (strict 1-hop). Neighbours honour the query's visibility contract: raw-tier quarantine plus the query's type, project, agent, importance, and tag constraints apply unchanged - a link must never become a back door past the search filters (added after code review found expansion bypassing them).

### D7. Tier-0 prewarm section
`stepPinnedCore` runs first in all three pipelines (`pipeline()` in `internal/prewarm/prewarm.go`), budget 150 tokens, order importance desc then `created_at` asc, full safety scanning, overflow note `[pinned: N of M shown]`. Pinned duplicates of later sections are de-duplicated from those sections so the budget is not wasted twice.

### D8. Transport parity, not MCP-only
`SaveInput.Pinned` flows through `POST /v1/observations` for free once the field exists, but `/v1/convention` and `/v1/correct` hand-build `SaveInput` and must grow a `pinned` request field (conventions are the primary pin candidates). New route `POST /v1/observations/{id}/pin` mirrors `mnemos_pin`. `pkg/client` gains `SaveInput.Pinned` and `Pin(ctx, id, pinned)` - additive, so no breaking public API change. Read echoes (`pinned` on get/search/save results) are added at the same result-construction points that already echo `source_kind` (`internal/mcp/tools.go:246,299`).

### D9. Fidelity surfaces in the same change
`Restore` (storage + service) round-trips `pinned`; the export payload carries it; `TestRestoreObservationPreservesFidelity` and `TestExportImportRoundTripPreservesFidelity` are extended rather than left stale. Vault frontmatter writes `pinned: true` when set (the frontmatter marshaller already drops empty values). Rationale: fidelity gaps are silent data loss and must never ship separately from the field they break.

### D10. Capability flags
Add behaviour-bound `pinned_memory` and `graph_retrieval` booleans to `Capabilities`; tier derivation is left unchanged (both belong to the core/provenance story already advertised). The flags are asserted live, keeping the honesty property of the capability document.

## Risks / Trade-offs

- **[Factor layering shrinks composite scores]** -> Contradiction/graph factors can push borderline hits below downstream absolute thresholds (`promptMemoryMinScore` per-prompt and pre-tool paths). Mitigation: tasks explicitly re-run `mnemos verify retrieval` and the prompt-path tests against the layered scale, and recalibrate the constants in the same change if precision or capture paths regress.
- **[Extra queries per search]** -> Neighbour and contradiction lookups are two batched `IN` queries against the indexed `observation_links` table, executed only for the top 3 candidate ids; measured budget < 0.5 ms. Mitigation: benchmarks in tests; strict 1-hop prevents blow-up.
- **[Pin inflation erodes Tier-0]** -> Users or agents could pin dozens of memories and starve the 150-token budget. Mitigation: transparent overflow counts, pinned count in stats/digest; a rumination monitor is the documented follow-up if abuse shows up in practice.
- **[Pinned duplicates across sections waste budget]** -> Mitigation: D7 de-duplicates pinned ids out of later prewarm sections.
- **[Two decay mechanisms drifting]** -> Ranker decay is per-type; dream decay is per-idle-time. Mitigation: pinned exemption is applied at the SQL level so both paths agree on the one lifecycle promise that matters most; the type table is documented next to the dream config in `docs/ARCHITECTURE.md`.
- **[Tool-count and description drift in docs]** -> README/MCP tool descriptions are load-bearing (capture-rate tuned). Mitigation: doc tasks in the same group as the tool work; wording changes beyond mechanical updates are called out for human review in the task text.

## Migration Plan

1. `0006_pinned.sql` runs via the embedded migration runner; existing rows default to `pinned = 0` and keep identical ranking (0.05-type rows are numerically unchanged; other types only gain retention).
2. Additive schema and additive public API (`pkg/client`) - no breaking change; rollback is safe because old binaries ignore the column and the extra JSON fields.
3. Deploy order inside the change: storage -> domain -> search -> prewarm/dream -> transports -> docs, matching task groups so every intermediate state compiles and tests green.
4. Post-merge: run `mnemos verify retrieval` and record the precision delta in the change notes; recalibrate `promptMemoryMinScore` only if the measured drop exceeds the harness noise band.

## Open Questions

None blocking. The following are deliberately deferred and do not affect specs, approach, or task breakdown:

- Should `GraphFactor` become configurable per scope once real corpora show link density variance? (Currently fixed 0.70 by design.)
- Should vault frontmatter use `pinned: true` only, or also a `#pinned` tag for Obsidian search parity?
- If a pin-inflation rumination monitor is later added, should it auto-suggest unpin candidates by access count?

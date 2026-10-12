# Proposal

## Why

Observations in Mnemos suffer from two architectural deficiencies inherited from early flat design:

1. **Uniform time decay and missing core memory.** Time decay is monolithic (a single `DecayRate = 0.05` across all observation types), so critical architectural invariants and conventions erode at the same pace as transient bugfixes and single-session corrections. There is no mechanism to designate unevictable core memory that stays perpetually visible in context.
2. **Disconnected point retrieval and contradiction blindness.** Retrieval treats observations as isolated nodes. The `observation_links` graph (`refines`, `derived_from`, `contradicts`) exists in storage but search ignores those edges: 1-hop refinements never surface, and refuted historical claims are served without warning.

A codebase audit for this change additionally found three integration surfaces where a naive implementation would silently lose or break the new behaviour: the dream pass runs a **second, independent decay mechanism** (`DecayImportance` + TTL `Prune`) that would erode and eventually expire pinned memories; the **export/import fidelity contract** (`Restore`, `TestExportImportRoundTripPreservesFidelity`) would drop `pinned` on backup/restore; and three transport surfaces (`internal/api` `/v1/convention` and `/v1/correct` hand-build `SaveInput`, `pkg/client` carries its own `SaveInput`) would diverge from the MCP surface.

Synthesizing proven mechanisms from recent agent memory architectures (Letta core memory, OpenMemory/Hindsight sector-differentiated decay, A-MEM Zettelkasten linking, HippoRAG neighbourhood expansion), this change upgrades Mnemos retrieval into a multi-sector, graph-augmented, contradiction-aware substrate while preserving every hard invariant: zero CGO, zero LLM calls inside the memory layer, deterministic pure-Go math, and thin transports.

## What Changes

- **ObsType-differentiated decay.** Replace the single scalar decay rate in the ranker with type-aware rates: `convention`/`architecture`/`decision` = 0.002 (year-scale half-life), `pattern`/`preference`/`semantic` = 0.03, `bugfix`/`context`/`episodic` = 0.05 (today's default), `correction` = 0.10 (fast fade unless reinforced or promoted).
- **Pinned core memory.** New `pinned` boolean on observations (SQLite column + index, migration `0006_pinned.sql`). Pinned memories are immune to recency decay (`recency_factor = 1.0`), exempt from dream-pass idle importance decay, and immune to TTL expiry (pinning clears `expires_at`, because a core memory that silently expires is a footgun). Pinned status does **not** bypass trust-tier quarantine: a pinned `raw` observation stays quarantined.
- **Tier-0 prewarm guarantee.** Pinned memories are injected first in every prewarm mode (session start, compaction recovery, premortem) under a dedicated ~150-token budget, ordered by importance desc then created asc, with transparent truncation (`[pinned: N of M shown]`) when they overflow.
- **Pin surface parity.** `mnemos_save` accepts `pinned`; new `mnemos_pin(id, pinned)` tool pins/unpins in place; HTTP `POST /v1/observations`, `/v1/convention`, `/v1/correct` accept `pinned` and a `POST /v1/observations/{id}/pin` route mirrors the tool; `pkg/client` gains `SaveInput.Pinned` and `Client.Pin`. All read surfaces (`mnemos_get`, `mnemos_search`, `GET /v1/observations/{id}`, export payload, vault frontmatter) expose the flag.
- **1-hop graph-augmented retrieval.** Top-3 candidates scoring above 0.45 expand their live `refines`/`derived_from` neighbours (max 2 per parent) into results at `GraphFactor = 0.70`, tagged `expanded_from`. Expanded results inherit the parent's relevance/policy breakdown so the score contract stays decomposable, receive the same contradiction treatment as primary hits, and are recorded in the injection log when they reach a push surface.
- **Contradiction penalty and annotation.** Candidates with a live `contradicts` edge to a `curated`/`skill` observation get `ContradictionFactor = 0.5` plus a `[WARNING: Contradicted by <id>: <title>]` banner; raw-tier-only contradictors get 0.75 with an "unverified source" note. Nothing is hidden: refuted memories stay visible, ranked lower, with provenance.
- **Layered score contract.** The composite score becomes `Score = clamp(Relevance x PolicyFactor x GraphFactor x ContradictionFactor, (0, ScoreCeiling])`, with every factor exposed on the result so consumers can audit the breakdown. `docs/ARCHITECTURE.md` documents the extended formula and the decay table.
- **Honest capability advertisement.** `mnemos://capabilities` / `GET /v1/capabilities` gain behaviour-bound `pinned_memory` and `graph_retrieval` flags.
- **Docs and tool registry.** `docs/MCP_TOOLS.md`, `README.md`, and both skill mirrors document `mnemos_pin`; the tool-count statements are reconciled with the actual registration count (21 today, 22 after this change).

## Capabilities

### New Capabilities
- `memory-lifecycle-retrieval`: Observation lifecycle semantics (type-differentiated decay, pinned core memory, dream-pass protection, export fidelity) and graph-augmented, contradiction-aware retrieval behaviour.

### Modified Capabilities
- None. The layered multipliers extend the score contract whose requirements live in the `memory-retrieval` capability (defined by the completed, pending-archive `retrieval-score-contract` change); the extension is declared as a requirement of the new capability and reflected in `docs/ARCHITECTURE.md`, so no requirement text in an existing spec changes.

## Impact

- **Storage** (`internal/storage`): migration `0006_pinned.sql`; `observations.go` persists/reads `pinned` across save/get/search/restore, `DecayImportance` skips pinned rows, new `ListNeighborsFor` and `ListContradictors` batch queries over the indexed `observation_links` table; TTL `Prune` naturally skips pinned rows once pinning clears `expires_at`.
- **Memory domain** (`internal/memory`): `types.go` (`Observation.Pinned`, `SaveInput.Pinned`, `SearchResult.ExpandedFrom` / `ContradictedBy` / `GraphFactor` / `ContradictionFactor`), `decay.go` (`DecayRateFor` + pinned bypass), `service.go` (graph expansion, contradiction scoring, factor layering), `store.go` (consumer-side interface additions), `capabilities.go` (two new flags).
- **Prewarm** (`internal/prewarm`): `stepPinnedCore` Tier-0 stage in all three pipelines, contradiction banners in rendering, injection logging of derived results.
- **Dream** (`internal/dream` / store decay path): pinned rows exempt from idle importance decay; journal semantics unchanged.
- **Transports**: `internal/mcp/tools.go` (`mnemos_save` param + echo, new `mnemos_pin`, search/get echo of pinned and graph annotations); `internal/api/server.go` (`pinned` on observation/convention/correct saves, pin route); `pkg/client` (`SaveInput.Pinned`, `Client.Pin`) - a backwards-compatible public API addition.
- **Fidelity surfaces**: export payload and `Restore` round-trip `pinned`; vault export writes `pinned: true` frontmatter.
- **Docs**: `docs/MCP_TOOLS.md`, `docs/ARCHITECTURE.md` (score formula + decay table), `README.md`, `.agents/skills/mnemos/SKILL.md` + `.claude` mirror.
- **Verification**: `verify/retrieval.yaml` precision@K re-measured because score factors shift ranking; prompt-path score thresholds (`promptMemoryMinScore`) re-checked against the factor-layered scale.
- **Invariants preserved**: zero CGO, zero LLM in the memory layer, deterministic outputs, no new dependencies, constructor injection, thin transports.
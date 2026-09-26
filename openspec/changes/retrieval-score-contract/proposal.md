# Proposal

## Why

Retrieval scores have no documented contract: the FTS path emits a BM25-scale score (~4-20), the hybrid path overwrites it with an RRF rank signal bounded by 1/(K+1) = 0.0164, and both feed the same downstream ranker and the same absolute threshold (`promptMemoryMinScore = 1.5`, calibrated against BM25 magnitudes). The consequence is a latent correctness bug: with an embedder configured (hybrid auto-enabled), every fused score lands below the floor, so the per-prompt and pre-tool memory injection paths silently drop every hit — precisely the paths the capture-rate measurement depends on. Two more retrieval gaps sit on the same pipeline: the vector is a reranker over BM25 candidates only (a memory sharing zero keywords with the query is never retrieved at any alpha), and every absolute threshold in the codebase is calibrated against a score scale that no longer exists in hybrid mode.

## What Changes

- **Independent vector recall.** Cosine similarity over all in-scope live embedded rows (pure-Go brute force) becomes a recall source unioned with the FTS5 BM25 candidates before fusion, closing the semantic blind spot where paraphrase-only memories never enter the candidate set.
- **Normalized continuous fusion replaces RRF.** Each recall source maps to an absolute relevance in [0,1] (saturating transform for BM25, calibrated linear clip for cosine); relevance is the weighted mean over *available* signals, renormalized so a missing embedding is not penalized. One fusion path only — no dual-mode score semantics.
- **Documented score contract.** `Score = relevance x importance x recency x access x cross-project penalty`, bounded (0, 1.2], produced at exactly one point in the pipeline. Multiplicative policy factors keep the "irrelevant cannot be outvoted by popular" property. `retrieval_mode` is demoted to a pure honesty signal (which sources contributed) and no longer changes score semantics.
- **Recalibrated gate.** The injection floor and the cross-project penalty are re-derived from measurement (verify/retrieval fixtures + injection-log surfaced-to-used ratios) on the new scale, with the calibration recorded. Consumers (prompt hook, pre-tool hook) keep threshold gating but on the contract scale.
- **Score breakdown output.** Retrieval results carry relevance and policy components alongside the composite score, so "why was this injected" is answerable from the result itself.
- **BREAKING** — the numeric meaning and range of `Score` returned by `mnemos_search`, `/v1/search`, the CLI, and `pkg/client` changes. Callers comparing against BM25-scale constants must move to the contract scale; a release note is required.

## Capabilities

### New Capabilities

- `memory-retrieval`: the retrieval pipeline contract — recall-source union (BM25 + vector, later link/graph sources), per-source normalization to absolute relevance, single-point composite scoring with documented bounds, gate calibration procedure, and result explainability.

### Modified Capabilities

None. `pi-integration` and `harness-neutral-hooks` reference "the existing relevance floor" as a concept; the floor concept survives, only its derivation and scale change, which is internal to `memory-retrieval`.

## Impact

- `internal/memory`: `SearchWithMode` / `fuseWithVectors` (fusion rewrite), `hybrid.go` (normalization params replace RRF), `decay.go` untouched; score contract documented on `SearchResult`.
- `internal/storage`: `Vectorable` grows a scoped vector-scan method (bulk read of live rows with embeddings); `obsStore.Search` candidate generation unchanged.
- `cmd/mnemos/hook.go`: `promptMemoryMinScore` and `crossProjectPenalty` constants re-calibrated to the contract scale; gate logic shape unchanged.
- `pkg/client`, `internal/mcp`, `internal/api`: score field semantics change (**BREAKING**); breakdown fields added.
- `verify/` + `docs/ARCHITECTURE.md`: a calibration pass over retrieval fixtures, and the score contract written down as an architecture invariant.
- Invariants held: zero CGO (brute-force cosine is pure Go), no LLM calls, no new dependencies, deterministic ranking.

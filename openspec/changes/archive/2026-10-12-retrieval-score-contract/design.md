# Design

## Context

See proposal.md - Why. The pipeline today emits three semantically different quantities through one `SearchResult.Score` field: BM25 magnitude from the store (`observations.go`, negated to positive, ~4-20), an RRF rank signal from `fuseWithVectors` (bounded by 1/(K+1) = 0.0164, K=60), and the ranker's policy-adjusted product of whichever is in the field. The prompt/pre-tool hooks gate on `promptMemoryMinScore = 1.5`, explicitly calibrated against BM25-after-ranker magnitudes (`cmd/mnemos/hook.go`), so hybrid mode drops every hit. Constraints that shape the fix: zero CGO, no LLM in the memory layer, no new dependencies, deterministic ranking (project invariants).

## Goals / Non-Goals

**Goals:**
- One score semantic end to end: absolute qualification for injection, comparable across modes and sources.
- Vector recall as a first-class candidate source, not a reranker over keyword candidates.
- Every threshold in the codebase calibrated against a scale that exists in all modes, with the calibration recorded and reproducible.
- A pipeline shape where future recall sources (typed-link expansion, derived-from causal chains, symbol anchoring) plug in as weighted sources without renegotiating the score contract.

**Non-Goals:**
- Learned ranking (LTR/cross-encoder rerankers). Insufficient surfaced-to-used data today; revisit when the injection log has scale.
- ANN indexes or any vector database. Local corpus scale does not justify one.
- Changing the ranking policy itself (importance/recency/access formulas, the cross-project penalty concept).
- Query rewriting, HyDE, or any agent-side query expansion (violates the no-LLM boundary; belongs in hosts if anywhere).
- Filter-only browse mode for empty queries (unrelated gap, noted in exploration).

## Decisions

**D1. Continuous normalization fusion replaces RRF; one fusion path only.**
Each source maps its raw signal to absolute relevance in [0,1]: BM25 via a saturating transform `n = b / (b + k)` where k is the corpus's typical top-hit magnitude (measured, not guessed); cosine via a linear clip between calibrated floor/ceiling percentiles of the observed similarity distribution. Relevance is the weighted mean over *available* signals (weights renormalized over present signals), configured through the existing `HybridParams` alpha. Rationale: RRF's robustness advantage matters when source magnitudes are not comparable; ours are bounded and measurable, and RRF destroys exactly the absolute-relevance dimension the gate needs (rank cannot express "this whole batch is weak"). Alternatives considered: (a) rank-percentile gating — cannot express weak batches, bakes the bug in; (b) RRF mapped back onto BM25 scale — fabricates magnitudes, uncalibratable; (c) per-mode threshold sets — combinatorial threshold x mode matrix, and scores stop being comparable across modes.

**D2. The composite score is produced at exactly one point and documented as a contract.**
`Score = relevance x importance x recency x access x project-penalty`, bounded (0, 1.2], documented in `docs/ARCHITECTURE.md` and in the `memory-retrieval` spec. `retrieval_mode` is demoted to an honesty signal (which sources contributed) and is not an input to scoring or gating. Rationale: today's bug exists because two producers wrote different scales into one field and consumers assumed one of them. Multiplicative form is kept deliberately: near-zero relevance stays near zero under any policy factors ("irrelevant cannot be outvoted by popular"), which additive fusion does not guarantee.

**D3. Vector recall is a brute-force cosine scan over in-scope live rows.**
`Vectorable` grows a bulk scoped read (live rows with embeddings, project/agent filters); ranking computes cosine over it in pure Go. Rationale: local stores are thousands of rows; brute force is milliseconds, deterministic, zero-dependency, zero-CGO. Alternatives: sqlite-vec (cgo, banned), hand-rolled ANN (complexity with no measured need). `// ponytail: brute-force scan, measured ceiling ~10k rows; revisit only if benchmark shows latency issues`.

Measured ceiling (BenchmarkVectorRecallScan, 10k rows x dim 64, including the scoped read and full-scan cosine, on a Core 2 Duo T7700): **~8.7 ms per full recall+search**. The brute-force path is nowhere near needing an index; the ceiling comment in code points here.

**D4. Gate constants are re-derived by a recorded calibration pass, not translated.**
The old floor (1.5) and penalty (0.1) were calibrated against BM25 magnitudes and cannot be converted to the new scale arithmetically (the scales are related by a saturating transform over a corpus distribution). A calibration task sweeps floor and penalty over `verify/retrieval` fixtures plus surfaced-to-used ratios from the injection log, and records the chosen values with their basis in the architecture doc. The consumer gate *shape* (threshold + top-N cap + suppression window) is unchanged — only the scale it reads is.

**D5. Results carry a score breakdown.**
Each result reports relevance and the composite policy factor alongside `Score`. Rationale: it is the "why was this surfaced" answer for operators and for Bet 4's attribution work, it is what makes future threshold debugging possible, and it costs one struct field group. This records the second open point from exploration as decided-in per the confirmation; it is additive and trivially removable if unwanted.

**D6. Initial constants come from the calibration pass before first release of the contract.**
This records the first open point from exploration as decided-in: ship parameter values that were swept against fixtures on day one rather than plausible defaults tuned later. The sweep is a small deterministic harness task, and the contract's whole point is that thresholds are evidence-based.

## Risks / Trade-offs

- [Calibration overfits the seed corpus] → Re-run the pass via the same harness after corpus changes; the calibration is a recorded procedure, not a one-off number. Values live in config so users can retune without a rebuild.
- [Brute-force scan latency grows with corpus size] → Bench task included; ceiling documented (D3). Rows outside the recall scope are never scanned.
- [Breaking score semantics for external callers] → Release note, `pkg/client` doc comment updated, breakdown fields ease migration (callers can see the new scale). Rollback is a revert: no schema change, no migration.
- [Weighted-mean fusion lets a single strong source dominate differently than RRF did] → Alpha stays configurable (LongMemEval's ~0.5 holds); retrieval fixtures assert the intended preference ordering for exact-identifier and paraphrase queries.
- [The bug ships one more release if the full contract change slips] → Timebox: if the change is not landing within the release window, ship a mode-aware interim floor (explicitly marked for removal at contract landing) so embedder users stop losing injection.

## Migration Plan

1. Implement recall union + normalization + contract scoring behind the existing `SearchWithMode` surface; update in-repo consumers (hooks, MCP, API, CLI, `pkg/client`) in the same change — no mixed-scale intermediate state is released.
2. Run the calibration pass; record floor and penalty with their basis in `docs/ARCHITECTURE.md`.
3. Release note: score semantics change (**BREAKING**), breakdown fields added, guidance for callers holding BM25-scale constants.
4. Rollback: revert the change; there is no persistent data migration (embeddings and rows are unchanged).

## Open Questions

- Weight defaults for the future recall sources (typed-link expansion, derived-from chains) — deferred until those sources exist; the union/normalize/fuse shape already accommodates them without spec changes.

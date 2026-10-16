# Proposal

## Why

Prewarm truncates section strings but can record every reference in the untruncated section as surfaced. Retrieval calibration also treats every non-target hit as irrelevant, so both delivery evidence and evaluation labels can misrepresent useful memory.

## What Changes

- Assemble prewarm and explicit context from bounded, independently rendered items; retain safety and contradiction annotations with their items and record only selected references.
- Preserve mode-specific priority, pinned overflow transparency, deterministic order, and the existing retrieval score contract.
- Add explicit positive, forbidden, and unlabelled evaluation cases, including no-injection cases; preserve legacy any-query probe semantics.
- Keep candidates, render plans, score breakdowns, and token estimates ephemeral. Reuse existing injection events; keep evaluation labels in versioned fixture files and reports in explicit CLI output.
- Defer MMR, generated summaries, new usage-event tables, and causal-success claims.

## Capabilities

### New Capabilities

- `memory-context-delivery`: Item-level budget selection, safe rendering, and truthful surfacing evidence.
- `memory-evaluation`: Explicit relevance labels, query aggregation modes, and delivery-level regression evaluation.

### Modified Capabilities

None. The score, pinning, and graph contracts in `memory-retrieval` and `memory-lifecycle-retrieval` remain constraints on delivery.

## Impact

Targets `internal/prewarm`, `internal/memory` context assembly, `internal/injection`, and `internal/verify`, with adjacent tests and verification documentation. No database migration, new dependency, model call, or hook-format redesign is required. Existing consumers of legacy fixtures remain compatible.

This is the first recommended implementation change. `scoped-memory-reading` is independently useful; `traceable-memory-evolution` does not depend on a new selection algorithm. Moving evaluation here replaces the earlier grouping of evaluation with read APIs: delivery needs trustworthy labels before algorithm changes can be judged.

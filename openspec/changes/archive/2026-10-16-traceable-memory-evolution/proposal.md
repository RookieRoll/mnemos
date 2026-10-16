# Proposal

## Why

Content-hash deduplication cannot group changing formulations of a decision, while topic equality alone cannot prove replacement. Existing provenance names parent observations but cannot locate a specific external evidence fragment, and supersession currently links and invalidates in separate writes.

## What Changes

- Add optional scoped topic keys for aggregation; only an explicit replacement target changes an existing observation's lifecycle.
- Make replacement atomic, conflict-aware, scope-checked, and subject to trust and pinned-memory protections.
- Add bounded, optional evidence references attached to an observation version: locator, source revision, fragment selector, fingerprint, and a minimal verification marker.
- Treat changed evidence as needing review, not as proof that a memory is false or permission to promote it.
- Persist only durable topic/evidence metadata and reuse existing observation versions and supersession edges. Do not store source files, transcripts, generated summaries, rankings, or full verification histories.
- Preserve metadata through full-store export/import. Keep historical evidence attached to its original observation version.

## Capabilities

### New Capabilities

- `memory-evolution`: Optional topic aggregation and explicit transactional replacement.
- `memory-evidence`: Minimal version-bound evidence references and non-authoritative verification state.

### Modified Capabilities

None. These capabilities supplement existing lifecycle and provenance behaviour without changing the retrieval score scale or trust-tier definitions.

## Impact

Targets domain types/services, additive SQLite migrations, export/restore, thin MCP/HTTP adapters, the Go client, and relevant documentation/tests. This is the only change in this series requiring new durable schema. No source-fetching daemon, graph database, AST/LSP dependency, or LLM call is introduced.

Topic keys and evidence references share version/export integration, not a mandatory data dependency: either may be absent. Separate capability specs and task groups permit review in stages. `reliable-memory-context` and `scoped-memory-reading` remain independently shippable; no shared main spec is concurrently rewritten.

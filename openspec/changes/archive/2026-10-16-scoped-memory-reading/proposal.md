# Proposal

## Why

Search already returns snippets and single-ID reads already return full observations, but agents must make repeated calls for selected details or retrieve excessive session history. Existing replay reads deliberately include invalidated observations, so reusing them directly would give a new ordinary timeline the wrong visibility semantics.

## What Changes

- Add bounded batch detail reads and chronological neighbourhood reads with explicit project/agent scope and validity/trust options.
- Define default live, non-raw visibility and explicit historical inspection without altering existing single-ID administrative/replay behaviour.
- Return deterministic order, per-ID unavailable status, and content-limit metadata; count explicit detail access only for returned items, without claiming successful use.
- Expose service behaviour through thin MCP/HTTP adapters and the Go client.
- Persist no batches, cursors, neighbourhoods, snippets, new read events, or copied content. Reuse observation storage and existing access counters.

## Capabilities

### New Capabilities

- `memory-reading`: Scoped batch detail retrieval and bounded chronological neighbourhood inspection.

### Modified Capabilities

None. Existing search ranking, historical replay, and single-ID get contracts are unchanged.

## Impact

Targets `internal/memory`, `internal/storage` observation reads, `internal/mcp`, `internal/api`, `pkg/client`, and tool documentation/tests. No schema migration or dependency is expected. New tools/routes are additive; capture-sensitive descriptions of existing write tools remain unchanged.

This change can ship independently of `reliable-memory-context`: it does not require its internal renderer or fixture schema. Implementing it second is a review/testing preference, not a hard dependency. It also does not require topic or evidence metadata from `traceable-memory-evolution`.

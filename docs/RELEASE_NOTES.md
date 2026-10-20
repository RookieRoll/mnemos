# Release notes

## 0.15.1

- Concurrent database upgrades recheck migration versions under the writer lock.
- CLI/hook and server searches use the same configured parameters, including
  explicit `hybrid_alpha = 0`.
- Vector recall/fusion reject incompatible model or dimension signals and retain
  keyword fallback. `embed backfill --rebuild-incompatible` explicitly rebuilds
  them; maintenance uses finite keyset progress and reports failures nonzero.
- Init validates arguments before writes and reports partial failure. A valid pi
  package route is reused; owned hook upgrades preserve user configuration.
- Migration 0010 avoids FTS rewrites for metadata and unchanged indexed values.
- Continuity candidate body loading is bounded without changing source selection,
  ambiguity, retention or the v1 wire contract.

The local installation workflow does not publish a release tag or hand-built
release assets. See the [archived hardening verification](../openspec/changes/archive/2026-10-20-harden-local-memory-runtime/verification.md) for exercised checks.

## 0.13.0

### BREAKING: retrieval scores moved to the composite score contract

`mnemos_search`, `POST /v1/search`, `mnemos search`, and `pkg/client` now
return `Score` on the composite score contract scale:

- **Old scale** — a BM25-after-ranker magnitude (`~4-20` in keyword mode) or
  an RRF rank signal (`~0-0.016` in hybrid mode): two different scales
  behind one field, which made any absolute threshold wrong in one mode or
  the other.
- **New scale** — `Score = clamp(relevance x policy x graph x contradiction, (0, 1.2])`,
  identical across retrieval modes. The graph and contradiction factors landed
  together with the graph-retrieval work below; both are `1.0` for plain hits,
  so nothing changes for consumers that predate them.

Consumers holding BM25-scale thresholds must move them onto the contract
scale (the prompt hook's floor is now `0.10`; the calibration procedure is
`mnemos verify calibrate`). The raw keyword magnitude is still returned per
hit as `BM25`, and the new `Relevance` / `PolicyFactor` / `GraphFactor` /
`ContradictionFactor` fields carry the full breakdown.

Also in this release: vector similarity became a true recall source (unioned
with keyword candidates before ranking), so memories sharing no keywords
with the query are retrievable at all. Stores without an embedding provider
are unaffected beyond the score scale change: keyword ranking behaviour is
preserved and no data migration is needed.

### Pinned core memory and lifecycle decay

`mnemos_save` and the HTTP/Go-client save surfaces take `pinned`, and a new
`mnemos_pin` tool (plus `POST /v1/observations/{id}/pin` and `Client.Pin`) pins
and unpins in place. Pinned observations skip time decay and the dream pass's
idle-importance decay, lose any TTL expiry when pinned, and always render
first in the prewarm block (Tier-0, ~150 tokens, overflow announced).
Unpinning restores the normal lifecycle. Baseline recency decay is now
type-differentiated: conventions, architecture, and decisions barely decay
(0.002), corrections fade fastest (0.10), so promoted skill outlives raw
history on schedule.

### Graph retrieval and contradiction warnings

Search expands top hits one hop along `refines` and `derived_from` links at
0.70 the parent's score, strictly inside the query's own visibility filters
(no raw-tier leakage; type/project/agent/importance/tag constraints are
honoured). Candidates with a live `contradicts` edge are halved — 0.75 when
only raw-tier refutations exist — and the snippet warns who disagrees. The
new breakdown fields reach all surfaces: MCP, HTTP, and `pkg/client`. Pinned
status round-trips through export/import and appears in vault frontmatter.

### Capability flags

The capability document gained the behaviour-bound `pinned_memory` and
`graph_retrieval` flags.

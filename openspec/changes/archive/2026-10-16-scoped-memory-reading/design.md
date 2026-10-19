# Design

## Context

See [proposal.md](proposal.md). `internal/mcp/tools.go` already offers snippet search and single-ID get. `obsStore.Get` increments access counts as part of the fetch. `ListBySession` intentionally returns invalidated rows for replay. Calling either repeatedly from a new batch/timeline handler would mix visibility, measurement, and transport logic.

All three transports must call the same domain operations. This change introduces no new storage entity and has no hard dependency on either sibling change.

## Goals / Non-Goals

**Goals:** Let agents choose which details to read, with bounded response size and explicit visibility.

**Non-Goals:** Retrofitting ACLs, changing existing ID-get/replay compatibility, new search/rerank algorithms, global cross-project browsing, read-event history, evidence fetching, or adding the sibling change's evidence fields as a prerequisite.

## Decisions

### 1. Adopt the reading pattern, not another memory pipeline

[Claude-Mem](https://github.com/thedotmack/claude-mem), [Engram](https://github.com/Gentleman-Programming/engram/blob/main/DOCS.md), and [QMD](https://github.com/tobi/qmd) provide search/detail/timeline or batch patterns. Mnemos already implements the first and last steps for a single ID. Add the missing operations rather than a new hierarchy or generated summary store. These are interface inspirations from the research, not imported code or benchmark claims.

### 2. Define narrow domain operations with explicit scope

Add `GetMany(ctx, input)` and `Neighborhood(ctx, input)` to the memory service. Declare the small storage-read interfaces at their consuming service; implement parameterised, non-access-mutating scoped reads in storage. Validate project as a non-empty hard selector; resolve an omitted agent to the same default agent identity as save. Empty project does not mean all projects for these new operations. Include-stale, include-raw, and as-of options are explicit. Apply the existing historical end/invalidation/expiry and trust predicates, plus `valid_from <= effective AsOf` when stale inspection is disabled. `IncludeStale` explicitly bypasses temporal validity predicates, including the lower bound. Do not constrain `created_at <= AsOf`: `AsOf` selects fact-time validity, not a system-knowledge or complete field snapshot. Existing Search/Get/replay behavior is unchanged.

Do not compose `GetMany` from the existing `Get`: it bumps access before visibility/size selection. Do not expose `ListBySession` directly as a neighbourhood: its historical replay semantics are broader. Existing methods keep their compatibility contract. Database/operational errors fail the request; unavailable IDs are ordinary per-item statuses.

### 3. Bound requests and responses without stored cursors

Planning defaults: batch accepts 1-32 IDs; neighbourhood accepts 0-10 visible observations before and after an anchor, defaults to 3 each. Returned detail/summary objects share a default 32 KiB aggregate JSON-encoded byte budget and a 256 KiB hard ceiling; reject an invalid budget. The complete encoded object (including title, tags, rationale, structured content, scope, and validity metadata) counts, so oversized metadata cannot bypass the bound. Embeddings and embedding model names are excluded from detail payloads. First occurrences determine batch order. Timeline ordering is `(created_at, id)` and all neighbours share the visible anchor's session, project, and agent.

Return `ok`, `unavailable`, or `omitted_for_size` per requested unique ID. `unavailable` covers missing and filtered IDs without disclosing why. Size-omitted entries can identify the requested ID but return no body; do not partially truncate body assertions in this first version. Return selected bytes, content-limit flags, and neighbourhood count/limit metadata. The byte budget governs whole returned detail/summary objects, excluding the bounded status envelope. Requested IDs are valid UTF-8 and at most 256 bytes, projects at most 4096 bytes, and agent IDs at most 256 bytes. Oversized stored neighbour IDs fail the request instead of bypassing the envelope bound. This refines the earlier content-only budget, whose metadata-count assumption did not bound metadata strings. A client can request one observation using a larger permitted budget or the existing single-ID tool.

Neighbourhood returns short result summaries, not full bodies. Summary titles are limited to 256 UTF-8 bytes and snippets to 512 UTF-8 bytes, with explicit truncation flags. Invalid UTF-8 is replaced and marked as changed. Summary truncation must be explicit and UTF-8 safe. A visible anchor without a session returns only its summary and `no_session: true`. An unavailable anchor yields one unavailable result and no sibling counts. Count neighbours after filtering; ties use ID. No opaque continuation cursor or cursor table is introduced.

### 4. Keep transport and measurement behaviour explicit

Add MCP `mnemos_get_many` and `mnemos_neighborhood`, HTTP `POST /v1/observations/batch` and `POST /v1/observations/neighborhood`, and typed client methods with equivalent fields. No new CLI command is necessary. Keep existing tools' behavioural descriptions intact; document the new read tools' budgets and scope.

Increment existing access counters once for each unique `ok` detail result after filtering and body-budget selection, using one atomic bulk update after draining result cursors. Counter failures fail the detail request, consistent with current get behaviour; no content response is advertised as successful if its bookkeeping operation fails, and a failed bulk increment leaves no partial counter changes. A later network failure can still leave a count, so this measures a service read rather than confirmed client receipt. Listing summaries does not bump counts. Neither operation writes injection events, `used` flags, task outcomes, or prompt text. Access remains a popularity signal, never proof of a successful action.

### 5. Persistence assessment

| Data | Persist? | Destination / lifetime |
| --- | --- | --- |
| Observation body, scope, validity, trust | Already persisted | Existing observation rows; retention unchanged |
| Explicitly returned detail access | Reuse existing aggregate only | Existing access count/time; no per-read event table |
| Batch IDs, dedup set, unavailable statuses | No | Request/response lifetime |
| Timeline window, summaries, limit flags | No | Derived from observation/session indexes |
| Response body-size calculation | No | Request-local accounting |
| Client navigation history / copied detail bodies | No server persistence | Client responsibility; never saved as observations |

No migration is needed. Existing indexes are sufficient for initial bounded neighbourhood queries; measure query plans before considering another index. Indexes are acceleration structures, not additional remembered knowledge.

## Risks / Trade-offs

- Existing single-ID get can reveal more than the new scoped tools -> document the deliberate additive scope rather than claiming a system-wide access-control boundary.
- Fixed content limits can omit a useful large entry -> expose a size status and allow an explicit larger bounded read.
- High-frequency browsing could inflate popularity -> summary browsing does not change counters, and details count once per unique returned ID per call.
- Concurrent writes may change results between calls -> each response uses one coherent read snapshot, but no historical pagination snapshot is promised.

## Migration Plan

No data rewrite. Add domain/storage operations, adapters, and client methods, then document tools. Test project/agent mismatch, raw/stale opt-ins, equal timestamps, hidden anchors, duplicate IDs, size omissions, and access-counter effects at domain and transport levels. An older binary ignores these additive endpoints; rollback does not lose durable data. Full-store backups remain unchanged by this change.

## Implementation verification (2026-10-16)

Independent review approved the final implementation with no outstanding blockers. Review identified and closed an oversized historical neighbour-ID envelope bypass, with regression coverage. Domain/storage and transport suites passed, including MCP/HTTP parity, metadata-inclusive budgets, filtered-window selection, and atomic access rollback. Manager integration passed `go test -p 2 ./... -count=1` with `MNEMOS_PRE_CHANGE_BIN` set to the original baseline, `go vet -p 2 ./...`, and `CGO_ENABLED=0 go build -p 2 ./...`. `graphify update .` rebuilt 3042 nodes and 12381 edges; optional SQL parser support was unavailable. No dependencies or schema entities were added. Local race checks remain owned by supported CI. Strict change validation passed after splitting long normative descriptions without removing constraints.

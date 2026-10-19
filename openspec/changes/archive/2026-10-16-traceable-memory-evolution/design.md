# Design

## Context

See [proposal.md](proposal.md). Observations already persist content, validity/system timestamps, hash, pin status, source kind, trust tier, and parent IDs. Supersession already has a typed edge, but `memory.Service.Supersede` calls link and invalidate separately. `Save` performs content-hash deduplication before insert, and restore skips existing IDs. These are the integration points; a second version store or document database would duplicate existing mechanisms.

The current CLI full-store snapshot carries observations, sessions, and skills. Extending observation structs alone is insufficient to preserve explicit supersession edges: export/import must also carry the relevant links, including historical endpoints, rather than relying on the current live-only `ListLinks` view.

## Goals / Non-Goals

**Goals:** Keep related knowledge discoverable, make explicit replacement safe, and retain enough evidence metadata to ask an agent to recheck a claim later.

**Non-Goals:** Automatically resolve contradictions, fetch sources, preserve full source snapshots, verify a claim with a model, maintain independent-proof confidence, implement a symbol index, or duplicate historical render/query state.

## Decisions

### 1. Co-locate version metadata integration, keep capabilities optional

Topic and evidence metadata both attach to an observation version and need the same save, read, restore, and export integration. Keeping them in this change avoids two competing modifications of those boundaries. Separate specs/task groups keep them reviewable: a topic needs no evidence; evidence needs no topic; replacement needs neither. They do not depend on the new read APIs or renderer.

Alternatives: a single change for all research would couple easy fixes to migrations; separate topic/evidence changes would be independently shippable but duplicate version/dedup/export changes. If implementation review finds this change too large, its capability groups are explicit split points, not an excuse to introduce a mandatory topic/evidence relationship.

Research references: [Engram's topic-key rules](https://github.com/Gentleman-Programming/engram/blob/main/DOCS.md), [Hindsight's evidence-backed observations](https://github.com/vectorize-io/hindsight#observations), and [Graphiti's relation evidence model](https://github.com/getzep/graphiti/blob/main/graphiti_core/edges.py). Absorb stable grouping and evidence attribution, not overwrite upserts, LLM inference, or proof-count confidence.

### 2. Topics are an optional field, not an entity hierarchy

Add nullable `topic_key` to observations plus a non-unique `(agent_id, project, topic_key)` index. Validate a trimmed, case-sensitive UTF-8 key of at most 256 bytes; an empty key means absent. Do not generate keys from titles or remap project identities automatically. Add an optional exact-topic filter to search/list reads without changing scoring or using topic similarity as a relevance signal.

No topics table, active-topic pointer, topic count cache, revision counter, or topic summary is needed. Existing rows/IDs and supersession edges already express versions. Different topics may contain identical prose, and one topic may contain several simultaneously valid assertions.

### 3. Replacement is explicit and transactional

Add optional `replaces_id` to save, plus explicit `unpin_replacement` acknowledgement (false by default). The named target is the optimistic concurrency token; no new generation counter is needed. Inspection corrected the original assumption that every replacement-relevant field stays immutable: later supersession rewrites valid-until, and pin/trust can change. Persist the SHA-256 of the original normalized request in an optional `replacement_hash` column on the existing supersession edge so retry identity survives those changes. Re-read the target within the write transaction and require it to be live and in exactly the same resolved agent/project scope. Reject a future-effective replacement in the initial API instead of silently invalidating today's fact early. If topic was omitted, inherit the target's topic; an explicit different topic is rejected for this replacement operation.

Domain validation decides trust/pin policy; a consumer-declared transactional storage primitive checks the expected target and atomically inserts the new observation and evidence, links it, and invalidates the old one. Use the same atomic link/invalidation primitive for existing `Link(..., supersedes)` over already-saved observations. Reject self-links/cycles and a second different successor. Preserve backdated/historical fields according to existing explicit validity semantics; this is not a redesign of bitemporal query meaning.

```text
validate input --> normalize identity --> optional embedding outside transaction
                                                |
                                                v
             transaction: recheck target --> insert new version + evidence
                                                |
                                                v
                                    link + invalidate --> commit
```

An already-applied request is a no-op only if its stored request hash agrees. Canonical identity includes normalized content, resolved scope, explicit topic, sorted/deduplicated evidence identities, requested TTL/pin/unpin flags and explicit UTC validity times; it excludes generated clock times, inherited mutable pin/trust, access counters, and latest evidence checks. Check replay before requiring the target to remain live. The same request returns the original successor even if that successor was later superseded or its mutable state changed. Otherwise return a typed conflict (HTTP 409 / structured MCP error). No `operation_id` table is needed. A raw new version cannot replace a curated/skill target; explicitly linking existing versions uses the same guard. A pinned target produces a pinned successor unless unpinning was explicitly acknowledged; pinning continues to neutralize expiry.

### 4. Evidence uses one small owned side table

Add `observation_evidence` keyed by `(observation_id, evidence_key)` with `ON DELETE CASCADE`. Fields: locator, relation (`supports` or `refutes`), optional revision/selector/SHA-256 fingerprint, verification status, and optional checked-at. Derive the evidence key from canonical identity fields; it is a storage dedup key, not an embedding or confidence signal. A JSON column was considered, but individual reference checks, identity uniqueness, and owner-bound cleanup are clearer with one small owned table. A global source-document table would need deduplication and retention policy for material this feature never stores, so it is rejected.

Planning limits: at most 16 references per observation; locator up to 2 KiB, revision 256 bytes, selector 512 bytes, fingerprint exactly 64 hexadecimal characters when supplied. No quote/body field in this version. Locators can describe repo-relative paths, public URLs, or existing tool-event IDs, but storing them triggers no I/O. Agent-supplied metadata passes the same safety and provenance validation as the owning write.

Store new references initially unverified. Add an explicit `CheckEvidence` service operation accepting observation ID, evidence key, and a caller-supplied check result; it stores only the latest status/time, not a check history. The service timestamps accepted checks with its injected clock and rejects a matched result without a recorded fingerprint baseline. Report status as caller-reported, not independently attested by mnemos. MCP `mnemos_evidence_check`, HTTP `POST /v1/observations/{id}/evidence/check`, and a typed client method expose this without reading files or fetching URLs. Source-body inspection remains the agent's job.

Check results never alter trust, importance, validity, pinning, or ranking. A hash mismatch means review needed; matching a fragment is not proof of the claim's meaning. The latest verification marker is mutable operational metadata; source identity is version-bound. A new observation version starts with only explicitly provided references, initially unverified; old references are not silently copied or freshly certified.

### 5. Extend deduplication without rewriting old hashes

Keep existing content hashes and legacy dedup between records where topic/evidence are absent. An absent-metadata input must not accidentally select a metadata-bearing row through a content-only lookup. For metadata-bearing saves compare normalized content identity plus topic and sorted evidence identity, rather than letting content-only lookup discard new metadata. Identical prose with different explicit metadata creates a distinct observation. Latest check status is excluded from identity, so checking a source does not manufacture a new memory. Resolve save dedup, replacement replay, and metadata insertion within the transaction to avoid concurrent duplicates or partial evidence. Do not change dream promotion's `promoted-origin:<12-char hash>` keying.

### 6. Persistence assessment and lifecycle

| Data | Destination | Durable justification and cleanup |
| --- | --- | --- |
| Authored assertion and its history | Existing observation rows | Existing memory; use current invalidation/prune/delete policy |
| Optional topic key | Nullable observation column | Caller-defined association survives sessions; removed with owner |
| Version identity/timestamps | Reuse existing IDs and validity columns | History already exists; no parallel revision ledger |
| Explicit successor relationship and original normalized request hash | Existing `observation_links`, with nullable `replacement_hash` | History plus stable retry identity after mutable lifecycle changes; not a fact, ranking feature, or separate operation ledger; existing link FK cleanup |
| Locator, relation, revision, selector, fingerprint | Owned evidence side table | Needed after session end to locate/recheck a source; cascades with owner |
| Latest caller-reported check status/time | Same evidence row | Retains an otherwise lost explicit check result; overwritten on next check, not a fact or confidence score |
| Topic counts, source availability inferred from local I/O | Compute when needed | No cached truth or automatic source poller |
| Rendered snippets, evidence panels, result summaries | Response only | Derivable from durable fields; discard after request |
| Full source text, quotes, transcripts, tool output | Not automatically stored | A locator/hash cannot reconstruct text; source capture would need a separately justified feature |
| Query rankings, MMR, verification histories, entity summaries | Not introduced | No current durability requirement |
| Evaluation labels and reports | Fixture/output files | Owned by `reliable-memory-context`, not inserted into observation storage |

Evidence is supporting metadata, not a new FTS observation. Do not index source locators as facts or embed evidence. Counts are computed from distinct references, not materialized as `proof_count`. There is no universal TTL: memory versions and their references need the existing owner's lifecycle; only latest operational check state is retained. Invalidating an owner keeps its evidence for history; physically deleting/pruning it cascades. Portability is full-store, not a promise that source files travel with the backup.

### 7. Migration, export, and compatibility

Use the next available numbered migration at implementation time; do not reserve a stale sequence number in this plan. Old rows get absent topic and no evidence rows. Add version-aware full-store snapshot fields for evidence and supersession links, retaining legacy observations/sessions/skills. Export links regardless of endpoint validity. Export observations/evidence and historical supersession links from one coherent read transaction; ordinary related/refines/contradicts edges remain outside this capability's snapshot section. Preflight all owner identities, evidence identities, link endpoints, cycles, and single-successor constraints before any import section writes. Restore the observation domain in one transaction: insert every missing owner first, then references, then supersession links. Existing IDs require matching immutable owner identity and, when supplied, the exact canonical evidence-key set. Old dumps lacking both topic/evidence may skip matching owners without deleting local metadata. Matching imports preserve newer local checks, counters, pin, trust, and lifecycle state. Never append different evidence to an existing version; never attach imported evidence to an unrelated owner. Conflicts roll back the observation-domain restore without a transient owner token or new table.

Expose optional fields consistently on save/read surfaces and the client; update restore validation separately from ordinary trust-clamped writes. Full-store export is the fidelity path. Vault and skill-pack formats remain explicitly lossy for the new evidence metadata; do not claim parity or expand those formats incidentally.

## Risks / Trade-offs

- A stored locator may become unavailable -> preserve metadata and report uncertainty; a hash is not a source backup.
- A caller can report an incorrect check -> label provenance explicitly, never promote trust or infer success from it.
- Topic keys can fragment -> keep them explicit and case-sensitive; avoid speculative aliasing/migration.
- Mutable pin/trust state can change during save -> recheck policy inside the transaction before commit.
- An old binary can omit new fields from its export -> document downgrade as read compatibility only, not metadata-preserving round-trip compatibility.
- New link export exposes a pre-existing snapshot gap -> add historical-link round-trip tests; do not rebuild missing links from prose.

## Migration Plan

Back up the existing database, apply additive schema changes, then deploy transactional replacement before exposing new mutating surfaces. Test old and new snapshot imports, concurrent replacement, injected failure rollback, dedup identities, scope/trust/pin guards, and cascade cleanup. Rollback to an older binary requires retaining the upgraded database/backup; do not run a destructive down-migration. To migrate data through an older binary, use a new-version fidelity export first and explicitly accept that old tools cannot preserve new metadata.

## Implementation verification (2026-10-16)

Independent review approved the final implementation without outstanding P1/P2 findings. Review closed and added regressions for mixed graph-edge export, same-ID evidence identity, multiple restored successors, topic-filter normalization, and stable UTC retry identity. Manager approved the minimal supersession request-hash column and atomic observation-domain snapshot restore to preserve the specified semantics without an operation table or transient owner token.

Manager integration passed `go test -p 2 ./... -count=1` with the original `MNEMOS_PRE_CHANGE_BIN` hook baseline, `go vet -p 2 ./...`, and `CGO_ENABLED=0 go build -p 2 ./...`. Dedicated regressions passed for two independent SQLite handles replacing the same target, evidence/link/invalidation failure rollback, topic vector/graph filtering, migration from schema 6, evidence cascade/bounds, malformed metadata, old dumps, matching repeats, mutable local-state retention, and historical supersession/evidence round trips. Documentation tests now normalize CRLF and require actual examples; 2 README YAML and 6 API/MCP JSON samples ran. Go formatting, whitespace checks, and strict change validation passed. No dependency changes were introduced. `graphify update .` rebuilt 3195 nodes and 13601 edges; optional SQL parser support was unavailable. Race detection remains for supported CI; no local race result is claimed.

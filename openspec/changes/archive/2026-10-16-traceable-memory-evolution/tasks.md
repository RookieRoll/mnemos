# Tasks

## 1. Minimal schema and metadata identity

- [x] 1.1 Add the next numbered additive migration for optional topic keys, their non-unique scoped index, and owned bounded evidence rows; verify old databases migrate and evidence cascades on owner deletion without a new topic/version/history table.
- [x] 1.2 Add domain metadata validation and canonical evidence identity, safety checks, and no-I/O save behaviour; verify absent fields, byte/count limits, duplicate references, raw provenance, and URL/path locators with table-driven tests.
- [x] 1.3 Extend save/read/restore storage mapping and metadata-aware deduplication without rewriting legacy hashes; verify identical prose with different topics/evidence preserves metadata, matching repeated saves deduplicate, and check-status updates do not create versions.
- [x] 1.4 Document durable-versus-transient fields, owner-bound retention, and locator/fingerprint limitations; verify documentation agrees with schema and metadata tests.

## 2. Explicit transactional evolution

- [x] 2.1 Add an atomic storage primitive for expected-live replacement and link/invalidation of existing versions; verify injected failures leave no partial row/edge/invalidation and single-connection transaction handling does not re-enter the DB pool.
- [x] 2.2 Add domain validation for project/agent scope, explicit target, cycles, future-effective replacement, trust, and inherited pin/topic policy; verify raw cannot replace trusted content and pin expiry protection remains intact.
- [x] 2.3 Add idempotent retry and typed conflict behaviour; verify same-payload replay returns the prior result, different-payload replay conflicts, and two concurrent writers cannot both replace the same version.
- [x] 2.4 Document append-versus-replace semantics with rule/exception examples and conflict recovery; verify examples against domain tests and preserve the dream promotion origin-hash contract.

## 3. Latest explicit evidence check

- [x] 3.1 Implement caller-reported check updates with an injected clock and fingerprint-baseline validation; verify changed/unavailable/matched statuses never alter trust, score, validity, pinning, or create a check-history record.
- [x] 3.2 Add optional topic filters and evidence fields across existing save/search/read surfaces plus the Go client, and the new evidence-check tool/route; verify transport parity, typed conflicts, and no implicit source fetching.
- [x] 3.3 Update MCP/API/client documentation to state caller-reported evidence status, non-independent reference counts, and default unverified state; verify examples round-trip and existing capture-sensitive descriptions are changed only where new write fields require it.

## 4. Fidelity export and restore

- [x] 4.1 Extend full-store snapshots with version-aware evidence and supersession links including historical endpoints; verify export does not bump access or omit links because an endpoint is invalidated.
- [x] 4.2 Restore owners before references/links with orphan validation and existing-ID identity checks; verify old dumps, matching repeated imports, conflict diagnostics, and original timestamps/IDs in round-trip integration tests.
- [x] 4.3 Document backup/downgrade constraints and the intentionally narrower vault/skill-pack formats; verify a fresh-store round-trip preserves both historical links and evidence check markers without fetching sources.

## 5. Integration verification

- [x] 5.1 Run affected storage/memory/MCP/API/client/CLI tests, `go vet ./...`, `go test ./... -count=1`, and a `CGO_ENABLED=0` build; verify zero new dependencies, no LLM calls, and rollback-safe additive schema behaviour.
- [x] 5.2 Rebuild `graphify update .` after implementation and run `openspec validate traceable-memory-evolution --strict`; verify every lifecycle, deduplication, metadata-retention, and export scenario has implementation evidence before archive.

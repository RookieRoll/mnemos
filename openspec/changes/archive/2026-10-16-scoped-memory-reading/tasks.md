# Tasks

## 1. Domain and scoped storage reads

- [x] 1.1 Add validated batch/neighbourhood input/result types and narrow non-mutating scoped storage reads; verify required project/default agent resolution and raw/stale/as-of options with table-driven tests.
- [x] 1.2 Implement stable deduplication/order and bounded batch body selection; verify duplicate IDs, missing/filtered equivalence, 32-ID limit, invalid budgets, and size-omitted results do not leak content.
- [x] 1.3 Implement visible-anchor neighbourhood reads from a coherent snapshot, filtering before before/after limits; verify equal timestamps, hidden anchors, mixed-scope sessions, historical opt-ins, and no-session markers.
- [x] 1.4 Apply access increments only to unique returned detail bodies after cursor drainage; verify listing and omitted items do not increment counts, new calls create no injection events, and single-connection SQLite does not deadlock.
- [x] 1.5 Document domain read semantics and response budgets in architecture documentation; verify stated limits and access behaviour match the new service tests.

## 2. Additive transport surfaces

- [x] 2.1 Add `mnemos_get_many` and `mnemos_neighborhood` through thin MCP handlers with schemas/descriptions; verify inputs, unavailable statuses, visibility metadata, and existing tool descriptions in MCP tests.
- [x] 2.2 Add HTTP batch/neighbourhood routes and typed Go client methods; verify MCP/HTTP equivalent inputs return equivalent selected IDs/statuses and client request/response round-trips preserve options.
- [x] 2.3 Document tools, routes, and client examples with explicit project scope, raw/history opt-ins, and size handling; verify examples in transport tests and state that no database migration or read-event table is introduced.

## 3. Integration verification

- [x] 3.1 Run `go test ./internal/memory ./internal/storage ./internal/mcp ./internal/api ./pkg/client -count=1`; verify new scoped reads and legacy get/replay behaviour coexist.
- [x] 3.2 Run `go vet ./...`, `go test ./... -count=1`, and a `CGO_ENABLED=0` build; verify no added dependencies or database entities and leave race checks to supported CI.
- [x] 3.3 Rebuild `graphify update .` after code changes and run `openspec validate scoped-memory-reading --strict`; verify all transport and persistence-boundary scenarios are covered.

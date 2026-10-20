# Proposal

## Why

The architecture review at `ba9cebb` found upgrade races, inconsistent search
configuration, incompatible vector reuse, non-progressing backfill, unsafe init
argument handling and integration migration gaps. Metadata updates also rebuild
FTS unnecessarily, while continuity candidate enumeration loads every full body
before applying its response limits.

## What Changes

- Recheck migration versions under the database writer transaction.
- Share CLI/server memory configuration and preserve explicit hybrid alpha zero.
- Admit only compatible embedding model/dimension signals; add bounded explicit
  rebuilding of incompatible embeddings with honest failure summaries.
- Parse init arguments before side effects, plan existing integration routes,
  report partial failure and precisely migrate owned hooks without losing user data.
- Add a migration limiting FTS updates to actual indexed-field changes.
- Bound continuity body materialization while preserving full-set ambiguity,
  exact filters, snapshot consistency, stable ordering and existing wire budgets.
- Validate all eight review items, archive this change, and ship a local patch
  installation with all distribution manifests agreeing at `0.15.1`.

## Capabilities

### New Capabilities

- `storage-runtime`: concurrent schema upgrades and selective index maintenance.
- `embedding-maintenance`: finite, cancellable embedding backfill and explicit
  incompatible-vector rebuilding without durable jobs.
- `agent-installation`: side-effect-free argument validation, installation plans,
  owned-route reuse and precise integration migration.

### Modified Capabilities

- `memory-retrieval`: configured search parity across entry points and compatible
  embedding-space signals.
- `session-continuity`: bounded candidate body materialization with unchanged
  full visible-set selection semantics.

## Impact

Changes stay within the existing CGO-free binary, SQLite database, constructors
and optional embedding interface. There are no new dependencies, LLM inference,
business tables, checkpoint histories or automatic retention changes. Existing
CLI/MCP/HTTP result contracts remain intact except init/backfill failures now
produce truthful nonzero exits; backfill gains `--rebuild-incompatible`.
Historical migrations remain unchanged; selective FTS maintenance uses migration
0010. Incompatible vectors remain stored until explicit maintenance rebuilds them.

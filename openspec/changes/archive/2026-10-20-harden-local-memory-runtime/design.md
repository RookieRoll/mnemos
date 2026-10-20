# Design

## Context

See proposal.md for the eight review findings. The baseline is `ba9cebb`:
one pure-Go SQLite connection per process, independently launched MCP/CLI
processes, constructor-injected services, continuity v1 and optional embedding.
Current migration version is 9; distribution version is 0.15.0.

## Goals / Non-Goals

**Goals:** preserve valid persisted-data and v1 wire semantics while correcting
configuration, maintenance and upgrade behavior; bound full-body allocations and
avoid unnecessary index writes; make installation failures explicit.

**Non-Goals:** new inference, providers, dependencies, business tables, durable
jobs, additional memory capture, checkpoint history, automatic deletion, a new
service container or an ANN index. No assertion of a performance percentage or
interactive TUI efficacy is made without corresponding measurement.

## Decisions

### Database upgrades and indexes

Keep the outer applied-version cache as a fast path, then recheck the individual
version inside the acquired immediate writer transaction before any reconciliation
or DDL. This handles independent processes; a process mutex or ignoring only the
ledger insert cannot prevent duplicate DDL. DDL and version insertion still commit
or roll back together.

Fresh-start testing also exposed WAL initialization contention before migration
DDL. Ledger bootstrap uses an immediate writer transaction, while only connection
initialization/Ping retries typed SQLite BUSY errors in a bounded startup window.
No migration or business statement is generically retried. Parent cancellation
stops subsequent attempts; driver connection setup retains its own busy-timeout
limitations, so the retry window is not a strict wall-clock cancellation promise.

Migration 0010 replaces only observation/skill update triggers. `UPDATE OF` names
indexed columns and `WHEN` checks actual old/new value changes. Insert/delete
triggers and historical migrations remain unchanged. Access, trust/lifecycle,
effectiveness and export bookkeeping retain their existing persistence semantics.

### Search construction and embedding compatibility

Add a small shared memory configuration factory for loadDeps and serve. Provider
resolution remains at existing call sites, so capability/continuity-only commands
stay free of provider discovery. Start with complete default rank/hybrid settings
then apply configured values, preserving alpha zero. Config.Load already begins
with defaults; remove the redundant zero-alpha reset.

Use one compatibility predicate for vector-only recall and keyword/vector fusion:
query dimension equals active embedder dimension; stored model equals active
model; actual vector dimensions match and the vector carries a valid signal.
Incompatibility is an absent signal, not similarity zero. Existing stored model
and vector metadata suffice for this patch; model aliases remain operator-defined
identifiers, not proof of the provider's deployed weights.

### Finite embedding maintenance

Keep default missing-only maintenance and add `--rebuild-incompatible`. A narrow
consumer interface captures an initial maximum ID and reads fixed-size keyset
pages up to that high-water mark. Both success and failure advance the cursor.
Each selected ID is attempted at most once per invocation. There is no durable
cursor or whole-run snapshot; concurrent writes may require a later invocation.
Summaries report attempted/succeeded/failed; failures return a nonzero result,
successful updates remain, and cancellation returns immediately.

### Integration planning and owned migrations

Init parses help/unknown flags/positionals before discovery or side effects.
Readonly discovery forms target plans. Execution reports per-target/hook errors
and joins failures, retaining independent successful steps.

A single validated local pi package route reuses its manifest-contained MCP file
and server key. Merge only installer-owned fields, preserving settings, manifests,
other servers and adapter-only keys. Multiple package routes or conflicting global
and package routes yield a diagnostic for that target, without arbitrary cleanup.
Known old hook commands for the current executable are removed/migrated at the
individual hook level, preserving sibling commands and unrelated group fields.

### Bounded continuity materialization

Keep one read transaction and the existing Store.Read method. Add task/window to
its internal read scope and a complete matched-count to its internal snapshot.
Stream candidate metadata and retain only a fixed top-K window using the existing
Go time comparison and session-ID tie break. After draining that cursor, read at
most K complete bodies in the same snapshot. Bound legacy summary prefixes too.
Explicit source reads materialize only the selected source and retain clear rules.

The service uses full matched-count for uniqueness/ambiguity and has_more. It still
checks freshness, state, workspace metadata and the complete JSON budget. Merely
adding SQL LIMIT before ambiguity is incorrect. This bounds application body
memory, not the CPU cost of scanning all matching metadata. Selected corrupt
bodies still fail; manually corrupted out-of-window bodies do not create a new
obligation to deserialize all retained content in Go.

## Risks / Trade-offs

- Narrower vector admission can reduce hybrid results until explicit rebuild;
  keyword fallback and honest mode reporting preserve usable retrieval.
- Partial installation succeeds for some targets; joined errors and idempotent
  reruns make the state explicit without discarding successful user configuration.
- Migration/trigger mistakes could damage search; concurrent upgrade, rollback,
  index-write audits and old/new search parity are required gates.
- Internal matched-count must not become a source of premature selection; test
  hidden stale/done/summary sources, task filters, equal times and tiny budgets.
- A high-water cursor is not a maintenance-wide snapshot; document concurrent
  mutations rather than adding a job ledger.

## Migration Plan

Run targeted regressions, cross-agent reviews, complete Go/vet and pi suites,
isolated real SDK integration and controlled upgrade/capacity probes. Sync specs
and archive only after all tasks pass. Align all eight distribution files at
0.15.1 in the same commit, push main, build a CGO-free local executable with commit
identity, back up and atomically replace the installed file, and refresh the pi
user-directory copy while preserving its absolute MCP command and registration.
Verify actual installed SDK tools and pi update behavior. Do not create a release
tag or publish hand-built artifacts. Rollback restores the prior executable/copy;
the additive trigger migration remains compatible with the earlier schema users.

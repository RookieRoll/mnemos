# workspace-diagnostics Specification

## Purpose

Helps local operators inspect fragmented or similar recorded workspace identities using explicit evidence and affected metadata counts, without silently changing ownership, visibility, retry identity or project boundaries.

## Requirements

### Requirement: Workspace diagnosis is a read-only operation
doctor --workspaces SHALL inspect an existing database with SQLite read-only/query-only mode. It SHALL NOT create or delete source files, migrate schema, change journal mode or business counters, or probe embedding. Missing history, unsupported schemas and unsafe WAL states SHALL be reported rather than repaired.

#### Scenario: No database exists
- **WHEN** workspace diagnosis targets a nonexistent store
- **THEN** it reports no stored history without creating a database or parent directory

#### Scenario: Schema is older
- **WHEN** the database schema does not contain the ledger, tables or columns required by the diagnostic reader
- **THEN** it reports the unsupported schema without migrating it

#### Scenario: WAL contains committed data
- **WHEN** a WAL-mode store has existing readable `-wal` and `-shm` sidecars
- **THEN** it includes committed WAL metadata while keeping the main database and WAL bytes unchanged and creating or deleting no source files

#### Scenario: WAL sidecars are absent
- **WHEN** a WAL-mode store is missing either required sidecar
- **THEN** it reports the read-only limitation without creating a sidecar or overlooking committed WAL records

### Requirement: WAL diagnostics preserve store files
When WAL mode has existing readable regular `-wal` and `-shm` sidecars, diagnostics SHALL include committed WAL metadata while leaving main database and WAL bytes unchanged. SQLite MAY update reader marks in the existing `-shm`; diagnostics SHALL NOT create or delete sidecars.

#### Scenario: Existing WAL store is inspected
- **WHEN** diagnostics read a WAL-mode store with both existing sidecars
- **THEN** the main database and WAL bytes, sidecar presence and size, and source directory entries remain unchanged while committed WAL rows are visible

### Requirement: Evidence is distinct from project equivalence
Diagnostics SHALL distinguish verified physical-path equivalence, possible parent-child boundary splits, possible moves, independent clone/worktree roots and unknown evidence. Similar names, branch or HEAD alone SHALL not certify project equivalence or safe merging.

#### Scenario: Two names refer to one physical directory
- **WHEN** existing filesystem metadata verifies that identity
- **THEN** the report marks path equivalence while retaining historical migration uncertainty

#### Scenario: Separate worktrees share a repository
- **WHEN** their names, HEAD or shared Git metadata match
- **THEN** diagnosis keeps those root identities independent by default

### Requirement: Diagnostic scope is metadata only
The local administrative report SHALL include roots/keys, affected session/open-session/checkpoint/clear-marker counts, evidence and observable merge risks. It SHALL not read or disclose private goals, checkpoint bodies, source text, credentials or raw retry identities.

#### Scenario: Candidate roots contain private progress
- **WHEN** a candidate is reported
- **THEN** its counts and risk labels do not include the progress body or private session fields

### Requirement: Diagnostic limits report incomplete coverage
Validated file settings SHALL bound roots, candidates and execution time. Truncation, timeout or inaccessible evidence SHALL be identified explicitly. Omitted comparisons SHALL not be presented as verified clean results; ordinary findings SHALL remain advisory.

#### Scenario: Candidate budget is exhausted
- **WHEN** the service finds more candidates than `max_candidates` permits it to return
- **THEN** it returns at most the configured candidates and marks coverage partial because at least one candidate was omitted

### Requirement: Diagnosis neither registers nor merges roots
Findings SHALL be returned as text or JSON, without adding a registry, marker, alias mapping, persisted similarity score or mutation. No session binding, request hash, checkpoint revision, visibility or legacy knowledge scope SHALL change.

#### Scenario: Strong evidence is found
- **WHEN** two recorded paths are physically equivalent
- **THEN** diagnosis returns advice and does not rewrite either workspace identity
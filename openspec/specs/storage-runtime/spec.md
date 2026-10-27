# storage-runtime Specification

## Purpose

Defines reliable local database startup and index maintenance so independent
agent processes can share one database without duplicate upgrades or unnecessary
full-text index writes during metadata bookkeeping.

## Requirements

### Requirement: Concurrent schema upgrades converge
Database startup SHALL serialize each upgrade with a writer transaction and recheck whether it has already been applied after acquiring that transaction. Concurrent valid starts against the same supported schema SHALL converge without duplicate-DDL or duplicate-version errors.

#### Scenario: Several processes upgrade one old database
- **WHEN** several processes start against the same supported older schema
- **THEN** each required upgrade commits once and all starts can use the resulting schema

#### Scenario: An upgrade fails before commit
- **WHEN** an upgrade statement fails
- **THEN** its schema changes and version marker roll back together

#### Scenario: Fresh connections briefly contend while initializing WAL
- **WHEN** concurrent fresh starts receive typed SQLite BUSY errors during connection initialization and contention clears within the startup retry window
- **THEN** only connection initialization is retried with bounded, cancellation-aware waits; other errors and migration statements are not generically retried

### Requirement: Index maintenance follows indexed content changes
Updating access counters, lifecycle or other non-indexed metadata SHALL preserve search correctness without deleting and reinserting full-text index content. Changing indexed content SHALL update that index atomically with the row. Historical migration files SHALL remain unchanged.

#### Scenario: A memory detail is read
- **WHEN** its access bookkeeping is updated without changing indexed text
- **THEN** the full-text index is not rebuilt for that row

#### Scenario: Skill effectiveness changes
- **WHEN** skill usage or effectiveness changes without indexed content changes
- **THEN** the skill full-text index is not rebuilt

#### Scenario: Indexed text changes
- **WHEN** indexed observation or skill text changes
- **THEN** searches reflect the new text and stop matching removed text

#### Scenario: Indexed values are assigned their existing values
- **WHEN** a write assigns the same indexed values
- **THEN** it does not rebuild the full-text index

### Requirement: Storage Size Is Reported

The storage layer's statistics value SHALL include the database's on-disk size,
computed from the database file plus, when present, its write-ahead log. The
shared-memory file SHALL NOT be counted, because it is transient and would make
the reported value flicker.

A failure to stat the files SHALL yield zero for this field and MUST NOT fail
the statistics call.

#### Scenario: Non-zero after first write

- **WHEN** statistics are read after a database has been created and written
- **THEN** the reported storage size is greater than zero

#### Scenario: WAL content is counted

- **WHEN** the write-ahead log is present and non-empty
- **THEN** the reported size is at least the size of the main file

#### Scenario: Missing files do not fail statistics

- **WHEN** the database path cannot be stat'ed
- **THEN** the storage size is zero and the rest of the statistics are still returned

### Requirement: MCP Statistics Surfaces Agree

The MCP `read_stats` resource and the MCP statistics tool SHALL render from the
same typed statistics value through one shared formatter. Neither surface SHALL
derive a statistic independently.

#### Scenario: Identical output

- **WHEN** the resource and the tool are queried against the same database
- **THEN** they report identical storage size and identical session backlog values

### Requirement: Online Consistent Backup

`mnemos backup <path>` SHALL create a transactionally consistent snapshot of the
live database at `path` using SQLite's `VACUUM INTO`, without requiring the
server to stop and without copying write-ahead log files by hand. The
implementation MUST remain CGO-free.

The command SHALL fail with a clear error, and a non-zero exit status, when the
target path already exists; it MUST NOT overwrite an existing file.

On success it SHALL print the destination path and the snapshot's size.

#### Scenario: Snapshot while writing

- **WHEN** `mnemos backup snapshot.db` runs while another connection is committing
- **THEN** the snapshot opens cleanly and its row counts match a consistent point in time

#### Scenario: Existing target refused

- **WHEN** the target path already exists
- **THEN** the command exits non-zero, names the path, and leaves the existing file untouched

#### Scenario: Export points at backup

- **WHEN** `mnemos export` runs
- **THEN** its stderr warning names `mnemos backup <path>` as the way to obtain a restorable copy

# storage-runtime — delta for cli-maintenance-contract

## ADDED Requirements

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

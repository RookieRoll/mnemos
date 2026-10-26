## ADDED Requirements

### Requirement: Doctor reports stale local databases

`mnemos doctor` SHALL scan two directories — the default data directory (`~/.mnemos`)
and the directory containing the selected config file — at their top level only, and
SHALL report every populated candidate database that is not the configured store as
`stale DB at <path> (last write <timestamp>)`. "Last write" SHALL be the later of the
database file's and its `-wal` file's modification time, formatted as UTC RFC 3339.
Candidates without observations, missing directories and the configured store itself
SHALL NOT be reported. Scan roots SHALL be resolved to absolute paths and deduplicated,
findings SHALL be ordered by path, and a stale finding SHALL NOT change doctor's exit
status.

#### Scenario: Legacy default database while a non-default store is configured
- **WHEN** the config selects a store outside `~/.mnemos` and `~/.mnemos/mnemos.db` still holds observations
- **THEN** doctor prints `stale DB at ~/.mnemos/mnemos.db (last write <timestamp>)`

#### Scenario: Configured store is not reported
- **WHEN** the configured store is the only populated database under the scanned directories
- **THEN** no stale-database line is printed

#### Scenario: Empty candidate is not reported
- **WHEN** a candidate file exists but contains no observations
- **THEN** it is not reported

#### Scenario: Overlapping scan roots
- **WHEN** the selected config file lives in `~/.mnemos`
- **THEN** each candidate is examined once and reported at most once

#### Scenario: Missing directories
- **WHEN** neither `~/.mnemos` nor the config directory exists
- **THEN** doctor completes without a stale-database error

#### Scenario: Stale database does not fail doctor
- **WHEN** doctor reports a stale database and nothing else is wrong
- **THEN** the exit status matches a run with no stale database

### Requirement: Database scanning is strictly read-only

Scanning SHALL NOT create, migrate, vacuum, repair, rename, move or delete any file,
SHALL NOT write to a scanned database, and SHALL NOT leave SQLite sidecar files behind.
A candidate that cannot be opened read-only SHALL be reported as `unreadable DB at
<path> (<error>)` and skipped; the remaining candidates SHALL still be scanned.

#### Scenario: Filesystem is untouched by a scan
- **WHEN** doctor scans a directory containing candidate databases
- **THEN** every candidate's size and modification time and the directory listing are unchanged

#### Scenario: Candidate that cannot be opened read-only
- **WHEN** a candidate fails a read-only open
- **THEN** doctor prints `unreadable DB at <path> (<error>)` and reports the remaining candidates normally

### Requirement: Diagnostics never consolidate databases

Reporting a stale database SHALL NOT merge, migrate, adopt, rename or remove it.
Consolidating two local stores remains an explicitly invoked future operation and MUST
NOT run as part of `doctor`, `init`, `serve`, prewarm or session start.

#### Scenario: Stale database survives the report
- **WHEN** doctor reports a stale database
- **THEN** the file and its rows are still present afterwards

### Requirement: Vault export records when it completed

`mnemos vault export` SHALL write an export manifest (`<vault>/.mnemos-vault-export.json`)
into the vault directory when it completes successfully, containing the completion time
(RFC 3339, UTC), the number of observations exported and the store path read. The
manifest SHALL be replaced atomically so an interrupted export leaves the previous
manifest intact, and a failed export SHALL NOT update it.

#### Scenario: Successful export writes the manifest
- **WHEN** `mnemos vault export` completes
- **THEN** `<vault>/.mnemos-vault-export.json` parses as JSON carrying `exported_at`, `observations` and `store`

#### Scenario: Interrupted export keeps the previous manifest
- **WHEN** an export fails before completing
- **THEN** the manifest still holds the previous `exported_at`

### Requirement: Vault watch publishes a heartbeat

`mnemos vault watch` SHALL write `<vault>/.mnemos-vault-watch.json` (pid, started_at,
heartbeat_at) when the loop starts and on each tick, and SHALL remove it on clean
shutdown. The heartbeat is informational, not a lock: a second watcher overwrites it and
status reflects the most recent heartbeat.

#### Scenario: Watch running
- **WHEN** `mnemos vault watch` is running and its last heartbeat is inside the staleness window
- **THEN** `mnemos vault status` reports `watcher: running`

#### Scenario: Clean shutdown removes the heartbeat
- **WHEN** the watch loop exits cleanly
- **THEN** the heartbeat file is gone and status reports `watcher: stopped`

#### Scenario: Second watcher on the same vault
- **WHEN** a second `mnemos vault watch` starts
- **THEN** it overwrites the heartbeat and status reflects the most recent writer

### Requirement: Vault status reports export provenance and watcher liveness

`mnemos vault status` SHALL derive `last_exported_at`, `pending N` and
`watcher: running|stopped` from on-disk artifacts rather than from configuration alone.
`last_exported_at` SHALL come from the export manifest and SHALL print `never` when the
manifest is absent. `pending N` SHALL count observations created or invalidated after
the manifest's export time, and SHALL print `pending: unknown` when the store cannot be
read. `watcher` SHALL be `running` only while a heartbeat newer than three tick intervals
(floored at 30 seconds) exists, and otherwise `stopped`, naming the stale heartbeat time
when the file is present but old.

#### Scenario: Vault directory never written by mnemos
- **WHEN** the configured vault directory holds no manifest and no heartbeat
- **THEN** status prints `last_exported_at: never`, a non-zero `pending` count and `watcher: stopped`

#### Scenario: Exported and idle
- **WHEN** a successful export is followed by no new observation
- **THEN** status prints that export time and `pending 0`

#### Scenario: Observations written after the last export
- **WHEN** one observation is saved, or one is invalidated, after the last export
- **THEN** status reports `pending 1`

#### Scenario: Store unreadable
- **WHEN** the configured store cannot be opened
- **THEN** status reports `pending: unknown` and still reports export and watcher state

#### Scenario: Missing vault directory
- **WHEN** the vault directory does not exist
- **THEN** status says the directory is missing, keeps `last_exported_at: never` and `watcher: stopped`, and exits 0

#### Scenario: Stale heartbeat after a crash
- **WHEN** the heartbeat file is older than the staleness window
- **THEN** status reports `watcher: stopped` and names the stale heartbeat time

### Requirement: Status is a read-only projection

`mnemos vault status` SHALL NOT create or modify the vault directory, the export
manifest, the heartbeat file or the store.

#### Scenario: Status creates nothing
- **WHEN** status runs against a vault directory that has never been exported to
- **THEN** the directory listing is unchanged afterwards

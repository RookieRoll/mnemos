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

### Requirement: Vault export records its successful read watermark

`mnemos vault export` SHALL write an export manifest (`<vault>/.mnemos-vault-export.json`)
into the vault directory when it completes successfully, containing the observation-read watermark
(RFC 3339, UTC), the number and IDs of observations exported and the store path read. The
manifest SHALL be replaced atomically so an interrupted export leaves the previous
manifest intact, and a failed export SHALL NOT update it.

#### Scenario: Successful export writes the manifest
- **WHEN** `mnemos vault export` completes
- **THEN** `<vault>/.mnemos-vault-export.json` parses as JSON carrying `exported_at`, `observations` and `store`

The `observation_ids` snapshot SHALL include exactly the successfully exported
observation IDs. An empty array SHALL represent an empty export; a missing or null
field SHALL retain compatibility with older manifests.

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
manifest is absent. With an ID snapshot, `pending N` SHALL count currently exportable
observations absent from that snapshot plus observations invalidated after the export
watermark. Older manifests SHALL retain timestamp-only counting until the next successful
export. Status SHALL print `pending: unknown` when the store cannot be
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

#### Scenario: Save waits on embedding across an export
- **WHEN** a save captures its creation time before export but commits after the observation snapshot was read
- **THEN** it remains pending until an export actually includes its ID

#### Scenario: Two vaults export the same store
- **WHEN** only one vault has exported a newly committed observation
- **THEN** each vault derives pending status from its own successful snapshot

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

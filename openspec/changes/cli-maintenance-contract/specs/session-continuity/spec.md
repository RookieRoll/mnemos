# session-continuity — delta for cli-maintenance-contract

## ADDED Requirements

### Requirement: Open Session Backlog Is Reported

`mnemos stats`, the MCP `read_stats` resource, and the MCP stats tool SHALL all
report the number of open sessions and the age of the oldest open session, in
the form `open sessions: N (oldest Xd)`.

All three surfaces SHALL derive the value from a single session-service read so
they cannot disagree.

#### Scenario: Backlog line on an empty store

- **WHEN** no session has been opened
- **THEN** the surfaces report `open sessions: 0` and no oldest age

#### Scenario: Backlog line with orphans

- **WHEN** three sessions were opened and none closed, the oldest two days ago
- **THEN** the surfaces report `open sessions: 3 (oldest 2d)`

#### Scenario: Surfaces agree

- **WHEN** the CLI and both MCP surfaces are queried against the same database
- **THEN** they report the same count and the same oldest age

### Requirement: Bulk Stale Session Reclaim

`mnemos sessions close --stale <duration>` SHALL close every open session whose
start time is older than `now - duration`, using the session service's existing
per-session close path, with the status given by `--status` (default
`abandoned`) and the summary given by `--summary`.

A non-positive duration SHALL be rejected as a usage error with a non-zero exit
code, because it would include the session that just started.

The command SHALL print the number of sessions closed.

#### Scenario: Reclaim orphaned sessions

- **WHEN** five open sessions exist, two of them started more than 24 hours ago
- **THEN** `mnemos sessions close --stale 24h --status abandoned --summary "reclaimed"` closes those two, prints `2`, and leaves the other three open

#### Scenario: Nothing to reclaim

- **WHEN** every open session started within the last hour
- **THEN** `mnemos sessions close --stale 24h` closes nothing, prints `0`, and exits successfully

#### Scenario: Non-positive duration rejected

- **WHEN** `mnemos sessions close --stale 0` is run
- **THEN** the command exits with a usage-error status and closes no session

### Requirement: Session Listing Filters And Pagination

`mnemos sessions` SHALL accept `--status`, `--limit`, `--offset`, and `--json`.
The filtered result SHALL be stable across pages for an unchanged store, and the
JSON form SHALL expose the same fields as the text form.

#### Scenario: Filter by status

- **WHEN** `mnemos sessions --status open` runs against a store with open and closed sessions
- **THEN** only open sessions are listed

#### Scenario: Paginate

- **WHEN** `mnemos sessions --limit 2 --offset 2` runs against five sessions
- **THEN** sessions three and four in the stable order are listed

#### Scenario: JSON form

- **WHEN** `mnemos sessions --json --limit 1` runs
- **THEN** stdout is a JSON array with one object carrying the session fields

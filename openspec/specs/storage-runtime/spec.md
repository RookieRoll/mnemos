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

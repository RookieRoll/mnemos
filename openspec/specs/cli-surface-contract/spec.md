# cli-surface-contract Specification

## Purpose
TBD - created by archiving change cli-maintenance-contract. Update Purpose after archive.

## Requirements

### Requirement: Help Is Uniform And Side-Effect Free

The CLI SHALL intercept `-h` and `--help` at the dispatch layer, before any
command handler executes, print the usage text, and exit with status 0.

A handler that parses flags and receives a help request SHALL likewise print
usage and exit 0 without touching storage.

#### Scenario: Top-level help

- **WHEN** `mnemos --help` runs
- **THEN** the usage text is printed and the exit status is 0

#### Scenario: Subcommand help

- **WHEN** `mnemos prune -h` runs
- **THEN** the usage text is printed, the exit status is 0, and nothing is pruned

#### Scenario: Help never writes

- **WHEN** any maintenance subcommand is invoked with `--help`
- **THEN** no store write occurs

### Requirement: Exit Status Distinguishes Usage From Failure

The CLI SHALL use exit status `0` for success and help, `2` for usage errors
(unknown subcommand, unknown flag, missing required flag, invalid flag value),
and `1` for runtime or domain failures.

#### Scenario: Success and help

- **WHEN** a command completes successfully or prints help
- **THEN** the exit status is 0

#### Scenario: Usage error

- **WHEN** `mnemos sessions --nope` or `mnemos retire` with no selector runs
- **THEN** the exit status is 2

#### Scenario: Runtime failure

- **WHEN** a command fails because the store cannot be opened
- **THEN** the exit status is 1

### Requirement: Maintenance Commands Parse Flags

Every maintenance subcommand SHALL parse its arguments through a flag set
configured to report errors instead of terminating the process, so that invalid
input surfaces as a usage error and help can be requested safely.

#### Scenario: Unknown flag is a usage error

- **WHEN** a maintenance subcommand receives an unrecognised flag
- **THEN** it prints usage on stderr and exits 2 without performing work

### Requirement: Bulk Destructive Commands Support Dry Run

Bulk destructive commands (`retire`, `sessions close --stale`, `skill delete`,
`skill merge`) SHALL accept `--dry-run`, which performs the same selection and
prints the same report as the real run while writing nothing.

Preview and execution SHALL share the selection code path so their reports
cannot diverge.

#### Scenario: Dry run reports the same set

- **WHEN** a bulk command runs first with `--dry-run` and then without it
- **THEN** the reported count and sample are identical and only the second run modifies data

#### Scenario: Dry run is successful

- **WHEN** a bulk command runs with `--dry-run`
- **THEN** it exits 0 even though nothing was written

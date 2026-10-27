# memory-lifecycle-retrieval — delta for cli-maintenance-contract

## ADDED Requirements

### Requirement: Bulk Fact-Time Retirement

`mnemos retire` SHALL set `valid_until = now` on every observation matching the
supplied selectors, in one transaction, and SHALL report the affected count.

Retirement MUST set only `valid_until`. It MUST NOT modify `created_at`,
`invalidated_at`, or any indexed text column, and it MUST NOT delete rows, so
that a historical query before the retirement instant still returns the
observation.

At least one selector among `--project`, `--importance-max`, `--no-access`, and
`--older-than` MUST be supplied; with none, the command SHALL fail as a usage
error and MUST NOT write.

`--dry-run` SHALL run the identical selection, print the identical report, and
write nothing.

#### Scenario: Retire by project, importance and access

- **WHEN** `mnemos retire --project alpha --importance-max 3 --no-access` runs
- **THEN** observations in project `alpha` with importance 3 or less and zero accesses get `valid_until` set to now, and the count is printed

#### Scenario: Retirement is visible to temporal queries

- **WHEN** an observation is retired and is then queried with `AsOf` set to a time before the retirement
- **THEN** the observation is returned; with `AsOf` after the retirement it is not

#### Scenario: Supersession semantics untouched

- **WHEN** a retired observation is inspected
- **THEN** its `invalidated_at` is unchanged and its text is unchanged

#### Scenario: No selector is refused

- **WHEN** `mnemos retire` runs with no selectors
- **THEN** the command exits with a usage error and no row is modified

#### Scenario: Dry run previews

- **WHEN** `mnemos retire --project alpha --dry-run` runs
- **THEN** the count and sample rows that would be affected are printed and no row is modified

#### Scenario: Bounded sample

- **WHEN** more than five observations match
- **THEN** the report includes a sample of at most five entries and the full count

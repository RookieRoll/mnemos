# embedding-maintenance Specification

## Purpose

Defines operator-invoked embedding maintenance that makes finite progress,
reports failures honestly and can rebuild incompatible vectors without creating
durable maintenance jobs or changing stored knowledge.

## Requirements

### Requirement: Backfill makes finite progress
One backfill invocation SHALL visit eligible records through a stable bounded cursor, attempting each record at most once. A failed record SHALL NOT prevent later records from being attempted. The invocation SHALL honor cancellation and report attempted, successful and failed counts; failures SHALL produce a nonzero exit while retaining successful updates.

#### Scenario: A full batch permanently fails
- **WHEN** at least 100 eligible records fail embedding on every attempt
- **THEN** each is attempted at most once in that invocation and the invocation ends with a failure summary

#### Scenario: Failures span several batches
- **WHEN** more than 200 records contain both successful and failed embeddings
- **THEN** later batches are reached without reattempting earlier failed records

#### Scenario: Cancellation arrives during maintenance
- **WHEN** the caller cancels
- **THEN** maintenance stops promptly and returns cancellation instead of reporting completion

#### Scenario: Provider or storage updates fail
- **WHEN** several attempts fail embedding or updating while later attempts succeed
- **THEN** the final nonzero result retains the first failing ID, stage and underlying cause with the aggregate counts, without accumulating a durable failure list

### Requirement: Incompatible-vector rebuilding is explicit
Default backfill SHALL process missing embeddings. The explicit `--rebuild-incompatible` option SHALL additionally rebuild vectors whose stored model or dimension is incompatible with the configured provider. Maintenance SHALL preserve observation identity, content, provenance and lifecycle, and SHALL keep cursors and failure summaries outside permanent memory tables.

#### Scenario: Normal backfill encounters an old model vector
- **WHEN** an otherwise complete stored vector belongs to another model
- **THEN** default backfill leaves it unchanged

#### Scenario: Rebuilding is requested
- **WHEN** the operator requests incompatible rebuilding
- **THEN** missing, model-mismatched and dimension-mismatched vectors are eligible while compatible vectors are left unchanged

### Requirement: Embedding Coverage Is Reportable

`mnemos embed status` SHALL report embedding coverage as `embedded N/M`, where
`M` is the number of observations eligible for embedding under the current
policy and `N` the number that carry a usable vector.

It SHALL additionally report a count per distinct `embedding_model`, and SHALL
flag any stored model whose vector dimension differs from the active model's
dimension.

#### Scenario: Fresh store

- **WHEN** `mnemos embed status` runs against a store with no embeddings
- **THEN** it reports `embedded 0/M` and an empty model distribution

#### Scenario: Partial coverage

- **WHEN** two of five eligible observations carry vectors from model `nomic-embed-text`
- **THEN** it reports `embedded 2/5` and one distribution row for `nomic-embed-text` with count 2

#### Scenario: Dimension mismatch is flagged

- **WHEN** a stored vector's dimension differs from the configured model's dimension
- **THEN** the report flags that model rather than counting the row as embedded

### Requirement: Backfill Reports Skips And Remainder

`mnemos embed backfill` SHALL report, in addition to the number embedded:

- `Skipped`, broken down by reason from the closed set `superseded`,
  `incompatible_model`, `empty_content`;
- `Remaining`, the number of eligible observations still without a vector.

Skipped rows MUST be counted rather than silently omitted, so a permanent skip
is distinguishable from a stalled backfill.

#### Scenario: Superseded observations are skipped and counted

- **WHEN** backfill runs over a store containing superseded observations
- **THEN** those rows appear under `Skipped` with reason `superseded` and no vector is written for them

#### Scenario: Remaining reaches zero

- **WHEN** backfill runs twice over the same eligible set
- **THEN** the first run reports a positive `Remaining` decrease, and the second reports `Remaining 0` with the same `Skipped` counts

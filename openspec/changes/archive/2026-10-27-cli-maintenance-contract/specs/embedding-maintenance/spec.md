# embedding-maintenance — delta for cli-maintenance-contract

## ADDED Requirements

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

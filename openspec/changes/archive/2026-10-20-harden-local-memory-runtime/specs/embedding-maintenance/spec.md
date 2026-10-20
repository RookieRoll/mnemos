# Spec Delta

## Purpose

Defines operator-invoked embedding maintenance that makes finite progress,
reports failures honestly and can rebuild incompatible vectors without creating
durable maintenance jobs or changing stored knowledge.

## ADDED Requirements

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

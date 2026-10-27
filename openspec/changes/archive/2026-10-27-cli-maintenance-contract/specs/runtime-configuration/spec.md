# runtime-configuration — delta for cli-maintenance-contract

## ADDED Requirements

### Requirement: Rumination Raw Promotion Policy Is Configurable

`[rumination].raw_promote` SHALL accept exactly `never` and `auto`, defaulting
to `auto` when the key is absent.

`auto` SHALL preserve the existing behaviour: raw rumination candidates may be
enqueued into the stale-raw queue.

`never` SHALL prevent raw rumination candidates from entering the stale-raw
queue.

Configuration loading SHALL reject any other value and SHALL name the allowed
values in the error. An invalid value MUST fail loading rather than fall back to
the default.

#### Scenario: Absent key defaults to auto

- **WHEN** a configuration file omits `raw_promote`
- **THEN** the effective value is `auto` and raw candidates behave as before

#### Scenario: Never suppresses the queue

- **WHEN** `raw_promote = "never"` and a raw rumination candidate is produced
- **THEN** no stale-raw entry is created for it

#### Scenario: Invalid value is rejected

- **WHEN** `raw_promote = "sometimes"`
- **THEN** loading fails with an error that lists `never` and `auto`

#### Scenario: Documented

- **WHEN** an operator reads `docs/CONFIGURATION.md`
- **THEN** the field, its default, and its effect are documented next to the other rumination settings

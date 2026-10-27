# runtime-configuration Specification

## Purpose

Makes application configuration reproducible from files and explicit selectors across CLI commands and agent integrations, without process environment overrides or disclosure of unrelated secrets.

## Requirements

### Requirement: Files are authoritative application configuration
Mnemos SHALL derive product settings from the selected TOML file and documented defaults. Former product environment overrides SHALL be ignored. OS home/executable discovery and child-process security filtering remain distinct from application settings.

#### Scenario: Conflicting former overrides are inherited
- **WHEN** MNEMOS_CONFIG, MNEMOS_DISABLED, MNEMOS_BIN or MNEMOS_CONTINUITY conflicts with file settings
- **THEN** the inherited values do not change the selected file, integration switches or route

### Requirement: Explicit configuration selection stays consistent
CLI commands SHALL support a leading --config selector. Equivalent selectors SHALL resolve consistently; conflicting selectors SHALL fail before command effects. Relative persisted paths SHALL resolve against the selected file directory, not command cwd.

#### Scenario: Config path contains spaces
- **WHEN** hooks, CLI and MCP use one explicitly selected file whose path contains spaces
- **THEN** each opens the same configured store regardless of project cwd

#### Scenario: Two selectors conflict
- **WHEN** global and command-specific selectors name different files
- **THEN** execution fails before configuration, installation or storage effects

### Requirement: Adapter projection is nonsecret and side effect free
Config projection for pi SHALL return only protocol_version 1, disabled and continuity.enabled. It SHALL not open storage, probe embedding or create files. An absent implicit default may use defaults; an absent explicit file SHALL fail.

#### Scenario: Secrets exist in the main configuration
- **WHEN** config --client pi reads configured server and provider credentials
- **THEN** the projection excludes both credentials and unrelated settings

#### Scenario: Explicit selector is misspelled
- **WHEN** its file does not exist
- **THEN** projection reports an error rather than silently selecting the default store

### Requirement: Automatic switches come from configuration
integration.disabled SHALL suppress automatic hook and prewarm effects before storage opens. continuity.enabled SHALL govern automatic continuity startup without removing explicitly invoked MCP progress tools. Neither switch SHALL require an environment variable.

#### Scenario: Automatic integration is disabled
- **WHEN** file configuration sets integration.disabled true
- **THEN** hooks and prewarm create no session, memory or database

### Requirement: Declared operational settings affect their consumers
Omitted search and ordinary context limits SHALL use search.default_limit and search.max_context_tokens. Prewarm SHALL use search.prewarm_max_tokens, default 500. Disabled vault configuration SHALL reject export/watch before output effects while leaving status available.

#### Scenario: Limits are changed in the file
- **WHEN** omitted-input searches/context/prewarm run with nondefault configured limits
- **THEN** each consumer honors its respective configured limit

#### Scenario: Vault is disabled
- **WHEN** vault export or watch is requested with vault.enabled false
- **THEN** it fails explicitly without writing export files or starting the watcher

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

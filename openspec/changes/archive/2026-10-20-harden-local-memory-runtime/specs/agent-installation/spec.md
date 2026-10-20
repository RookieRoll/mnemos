# Spec Delta

## Purpose

Defines predictable installation and upgrade of local agent integrations,
including argument validation, truthful partial-failure reporting and preservation
of user configuration when owned integration entries change.

## ADDED Requirements

### Requirement: Init validates before side effects
Init SHALL parse and validate all arguments before planning or executing installation. Help SHALL return successfully without installation writes; unknown options or unexpected positional arguments SHALL fail without installation writes.

#### Scenario: Help is requested
- **WHEN** init receives `--help` or `-h`
- **THEN** it prints usage and leaves user configuration unchanged

#### Scenario: An argument is invalid
- **WHEN** init receives an unknown flag or unexpected positional argument
- **THEN** it returns a nonzero exit without changing user configuration

### Requirement: Installation failures remain visible
Init SHALL plan target routes before execution and report installation or owned-hook migration failures with target context. Successful independent steps SHALL remain installed, and any failed step SHALL cause a nonzero final exit rather than an unconditional success message.

#### Scenario: One target fails and another succeeds
- **WHEN** independently planned targets have mixed outcomes
- **THEN** the successful configuration remains installed and the final result reports failure

### Requirement: An existing pi package route is reused
Init SHALL prefer a single validated existing local pi package MCP route over creating a new global route. Updating it SHALL preserve package registration, unrelated servers and adapter-owned fields. Ambiguous or conflicting routes SHALL be reported without guessing, deleting an unknown route or creating another duplicate.

#### Scenario: A local package already provides Mnemos
- **WHEN** a valid package route is installed
- **THEN** init updates its owned MCP fields and does not create a global duplicate

#### Scenario: More than one route conflicts
- **WHEN** multiple valid package routes or conflicting package and global routes are found
- **THEN** that target fails with a diagnostic and its routes are left unchanged

### Requirement: Owned hook upgrades preserve user hooks
Known old Mnemos hook commands and matchers SHALL be migrated precisely for the current executable. Other commands, group fields and user hooks SHALL be preserved, including hooks sharing the same group. Repeating the upgrade SHALL be idempotent.

#### Scenario: The write-tool matcher gains a tool
- **WHEN** an owned three-tool guardrail is upgraded to the current four-tool matcher
- **THEN** the old owned command is replaced without duplicate guards

#### Scenario: A user hook shares the old group
- **WHEN** an owned command is migrated from a group containing another hook
- **THEN** the other hook and unrelated group fields survive unchanged

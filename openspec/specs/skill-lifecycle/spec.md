# skill-lifecycle Specification

## Purpose
TBD - created by archiving change cli-maintenance-contract. Update Purpose after archive.

## Requirements

### Requirement: Skill Delete And Merge Through The Store Interface

`mnemos skill delete <name>` and `mnemos skill merge <name>` SHALL operate on the
skills matching the given name, narrowed by `--agent` and `--project`.

Deletion SHALL go through the skill store's normal delete so that any derived
index (including a full-text mirror) stays consistent; a raw SQL `DELETE` is
forbidden.

Merge SHALL keep one skill — the highest version by default, or the version named
by `--keep-version` — and mark the remaining matches superseded through the same
versioning path the skill service already uses.

Both commands SHALL print the affected rows before writing and SHALL support
`--dry-run`, which performs the same selection and writes nothing.

#### Scenario: Delete removes the skill and its index entries

- **WHEN** `mnemos skill delete deploy-checklist` runs
- **THEN** the skill and its full-text entries are gone and a search no longer returns it

#### Scenario: Merge keeps the highest version

- **WHEN** two skills named `deploy-checklist` exist at versions 2 and 3 and `mnemos skill merge deploy-checklist` runs
- **THEN** version 3 remains current and version 2 is marked superseded

#### Scenario: Merge keeps an explicit version

- **WHEN** `mnemos skill merge deploy-checklist --keep-version 2` runs
- **THEN** version 2 remains current and the other match is marked superseded

#### Scenario: Dry run writes nothing

- **WHEN** `mnemos skill delete deploy-checklist --dry-run` runs
- **THEN** the rows that would be removed are printed and the store is unchanged

#### Scenario: No match

- **WHEN** the name matches no skill
- **THEN** the command reports zero affected rows and exits successfully

### Requirement: Deterministic Skill Promotion

Skill promotion SHALL remain deterministic: the same set of live correction
records MUST always produce the same promoted skill content, with no model
inference, no prompts, and no dependence on record ordering.

Promotion SHALL keep the `promoted-origin:<12-char sha256>` tag keyed over
`(agent_id, project, label)` as the idempotency key for a promotion group. Those
hash inputs and that tag format MUST NOT change, because they are the identity
every already-promoted skill is keyed by.

Before writing a promoted skill, the promotion path SHALL look up an existing
skill for the same `(project, label)` and, when one exists, merge the promoted
content into it — bumping its version and preserving its existing
`promoted-origin` tag — instead of inserting a second skill with the same name.

#### Scenario: Promotion does not duplicate

- **WHEN** the same correction group is promoted twice with a changed body
- **THEN** exactly one skill exists for that `(project, label)`, with an incremented version and the original origin tag

#### Scenario: Origin hash unchanged

- **WHEN** a promotion group with a known `(agent_id, project, label)` is promoted
- **THEN** the `promoted-origin` tag equals the pre-change hash for that group

#### Scenario: Same label in different projects

- **WHEN** two projects each promote a skill labelled `retry-policy`
- **THEN** two distinct skills exist, one per project

#### Scenario: Same label in different agents

- **WHEN** two agents each promote a skill labelled `retry-policy` in the same project
- **THEN** two distinct skills exist, one per agent

#### Scenario: Promotion stays deterministic

- **WHEN** the same live correction set is promoted twice with no membership change
- **THEN** the resulting skill content is identical and no second skill is created

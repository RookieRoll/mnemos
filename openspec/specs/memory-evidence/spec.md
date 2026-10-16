# memory-evidence Specification

## Purpose

Defines minimal evidence references that locate supporting or refuting source material for an observation version without copying a source corpus into memory.

## Requirements

### Requirement: Optional version-bound evidence

An observation SHALL support bounded optional evidence references containing a locator and relation, with optional source revision, fragment selector, and content fingerprint. Evidence SHALL belong to a particular observation version. Evidence identity SHALL remain independent of topic membership.

#### Scenario: Evidence without a topic
- **WHEN** an observation has a source reference but no topic key
- **THEN** the reference is accepted and retrievable without creating a topic

#### Scenario: A version is replaced
- **WHEN** a new version replaces an observation carrying evidence
- **THEN** the old evidence remains attached to the old version and is not silently rewritten or inherited as freshly verified evidence

### Requirement: Locator storage does not fetch or certify sources

Saving or reading a reference SHALL NOT fetch a URL, execute a command, or read an arbitrary file. A reference SHALL NOT promote trust, authorize replacement, or create a new searchable observation from source material. Evidence metadata SHALL pass the existing write-boundary safety policy.

#### Scenario: A reference targets a remote URL
- **WHEN** the reference is saved or returned
- **THEN** no remote request is made and the reference does not certify the remote content

### Requirement: Minimal explicit verification state

References SHALL expose unverified, matched, changed, or unavailable status with an optional last-check timestamp. These SHALL be recorded results of an explicit caller-supplied check, not inferred automatically from a locator. A changed or unavailable source SHALL NOT invalidate, demote, or promote the observation by itself.

#### Scenario: The cited fragment changes
- **WHEN** an explicit check reports a fingerprint mismatch
- **THEN** the reference is marked changed and the observation's validity and trust remain unchanged

#### Scenario: The file changes elsewhere
- **WHEN** an explicit fragment check still matches despite a different whole-file revision
- **THEN** the reference can remain matched because fragment identity and file identity are distinct

### Requirement: Repeated evidence is not independent proof

Duplicate evidence identities within an observation SHALL be represented once. Exposed reference counts SHALL count distinct stored references only and SHALL NOT be described as independent proof, successful use, or calibrated confidence.

#### Scenario: The same fragment is submitted three times
- **WHEN** all references identify the same locator, revision, selector, fingerprint, and relation
- **THEN** one reference is retained and no three-proof confidence claim is emitted

### Requirement: Bounded evidence metadata storage

Evidence SHALL persist only bounded locator metadata and the latest explicit verification marker. Original files, webpages, transcripts, tool-output bodies, generated summaries, and verification-event histories SHALL NOT be stored automatically. Missing source access SHALL be reported honestly rather than reconstructed from a hash.

#### Scenario: Source material is no longer accessible
- **WHEN** a caller inspects an unavailable reference
- **THEN** stored metadata is returned without claiming that the original text can be reproduced

### Requirement: Evidence retention follows its owner

References SHALL remain with historical observations until their owner is physically deleted or pruned under existing policy. Such deletion SHALL remove associated reference metadata. Full-store export/import SHALL preserve reference identity and verification markers, while old dumps without evidence remain valid.

#### Scenario: Historical evidence is backed up
- **WHEN** an invalidated observation and its evidence are exported and restored
- **THEN** their identifiers, metadata, validity, and last-check state survive without automatic re-verification

#### Scenario: The owner is physically removed
- **WHEN** existing retention policy deletes the observation
- **THEN** its evidence metadata is also removed without leaving orphan source entities

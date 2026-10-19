# Spec Delta

## Purpose

Defines durable topic grouping and explicit observation replacement without conflating related facts, overriding trust boundaries, or losing historical versions.

## ADDED Requirements

### Requirement: Optional topic grouping is non-destructive

Observations SHALL support an optional topic key scoped by agent and project. Saving the same topic SHALL append a member unless an explicit replacement target is provided. Topic membership SHALL NOT imply contradiction, equivalence, trust, or a single current truth.

#### Scenario: A rule and an exception share a topic
- **WHEN** both are saved under the same scoped topic without a replacement target
- **THEN** both remain live and retain separate observation identifiers

#### Scenario: Topic metadata is absent
- **WHEN** a legacy caller saves without a topic
- **THEN** existing save and content-deduplication behaviour remains available

### Requirement: Atomic explicit replacement

Replacement SHALL commit the new version, its explicit supersession relationship, and the prior version's invalidation together or leave all unchanged. Linking existing versions as superseding SHALL also atomically apply the lifecycle change. Replacement SHALL NOT depend on topic inference.

#### Scenario: Writing the replacement fails
- **WHEN** any write in a replacement operation fails
- **THEN** no replacement version, edge, or invalidation is partially committed

#### Scenario: No topic was assigned
- **WHEN** a caller explicitly replaces a valid observation without topic metadata
- **THEN** replacement can succeed without inventing a topic

### Requirement: Replacement validates scope and current version

A replacement SHALL name the expected live target in the same agent and project scope. An already-invalidated target or conflicting replacement SHALL return a conflict. Concurrent attempts to replace the same live version SHALL NOT both succeed. Self-replacement and supersession cycles SHALL be rejected.

#### Scenario: Two writers replace one observation
- **WHEN** both writers name the same expected live target
- **THEN** at most one succeeds and the other receives a conflict without side effects

#### Scenario: A target belongs to another scope
- **WHEN** replacement names an observation from a different project or agent
- **THEN** the operation fails without changing either scope

### Requirement: Replacement preserves trust and pin safeguards

Raw replacements SHALL NOT invalidate curated or skill-tier observations. A replacement of a pinned observation SHALL remain pinned unless the caller explicitly authorizes unpinning. Pinning SHALL continue to clear expiry and SHALL NOT bypass quarantine. Evidence references SHALL NOT confer authority to replace or promote.

#### Scenario: Untrusted content names a trusted target
- **WHEN** a raw observation attempts to replace a curated version
- **THEN** the target stays live and the replacement operation is rejected

#### Scenario: A pinned rule is revised
- **WHEN** the caller replaces it without an explicit unpin instruction
- **THEN** the new version is pinned with expiry neutralized and the old version remains inspectable as historical

### Requirement: Deduplication respects durable metadata identity

An exact repeated save SHALL deduplicate only when its scoped content, topic, and evidence identity agree. Different explicit topic or evidence metadata SHALL NOT silently disappear through content-only deduplication. Replaying an already-completed replacement with the same payload SHALL return the existing result; a different payload SHALL conflict.

#### Scenario: Identical prose has distinct evidence
- **WHEN** a caller saves the same prose with a different explicit evidence reference
- **THEN** the new metadata is preserved on a distinct version rather than discarded by a content-only match

### Requirement: Evolution metadata is portable and minimal

Topic metadata, original observation IDs, supersession relationships, and validity timestamps SHALL survive full-store export/import. An old dump without new fields SHALL remain importable. Topic counts, current-topic summaries, and inferred topic indexes SHALL NOT be persisted as remembered facts.

#### Scenario: Restore an old backup
- **WHEN** a pre-feature dump is imported
- **THEN** observations retain their original identity and validity with absent topic metadata

#### Scenario: Topic data is deleted with its observation
- **WHEN** an observation is physically deleted or pruned under existing policy
- **THEN** no separate orphan topic entity remains

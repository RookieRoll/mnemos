# Spec Delta

## Purpose

Defines reproducible memory retrieval and delivery evaluation with explicit relevance labels and compatible query aggregation, without introducing model-based judges.

## ADDED Requirements

### Requirement: Explicit relevance sets

An explicitly labelled case SHALL distinguish relevant IDs, forbidden IDs, and unlabelled IDs. Unlabelled hits SHALL NOT become calibration negatives. A contradictory label assigning the same ID to both sets SHALL fail fixture validation.

#### Scenario: More than one useful memory
- **WHEN** a query labels two memories relevant and both are retrieved
- **THEN** neither is scored as irrelevant merely because the other is also relevant

#### Scenario: An unexpected but unlabelled result
- **WHEN** a hit belongs to neither explicit set
- **THEN** it is reported as unlabelled and excluded from positive/negative calibration labels

### Requirement: Compatible query aggregation

Legacy probes SHALL keep their existing any-query success rule. Explicit cases SHALL support any-query and all-query rules and report each query's result separately. New strict aggregation SHALL NOT silently change a legacy probe's pass status.

#### Scenario: Only one paraphrase retrieves the target
- **WHEN** one of three query variants succeeds
- **THEN** a legacy or explicit any-query case passes and an explicit all-query case fails with the failing variants identified

### Requirement: Explicit empty-delivery expectations

Cases SHALL support an expectation that no memory is injected for a query. The verdict SHALL use the configured consumer gate and final selected references, rather than requiring the retrieval candidate set to be empty.

#### Scenario: Weak results exist but none deserve injection
- **WHEN** search finds low-scoring candidates and the real consumer gate suppresses all of them
- **THEN** an empty-delivery case passes

#### Scenario: An unrelated result crosses the gate
- **WHEN** a memory is injected in a case expecting no injection
- **THEN** the case fails and identifies the unexpected delivered reference

### Requirement: Labelled calibration reports evidence limits

Calibration SHALL distinguish explicitly labelled cases from legacy single-target proxy labels and report labelled sample counts, coverage, and per-query outcomes. An empty or single-class sample set SHALL be identified and SHALL NOT be presented as a validated separation of relevant and irrelevant memories.

#### Scenario: Legacy fixtures remain usable
- **WHEN** calibration uses an unchanged legacy fixture
- **THEN** its proxy labelling remains available and the report identifies that basis

#### Scenario: No labelled negative was retrieved
- **WHEN** explicit cases produce only relevant or unlabelled results
- **THEN** the report states that negative separation was not measured

### Requirement: Evaluation is separate from remembered knowledge

Fixtures and labels SHALL remain file-based. Reports SHALL be returned or written only to an explicitly requested report destination. Running evaluation SHALL NOT save probes, expected answers, run reports, or generated contexts as observations, nor claim causal benefit from access or surfacing counters.

#### Scenario: Repeatable evaluation on an isolated store
- **WHEN** the same fixture, fixed time, and isolated seeded store are used twice
- **THEN** verdicts are identical and no evaluation-generated memories are inserted

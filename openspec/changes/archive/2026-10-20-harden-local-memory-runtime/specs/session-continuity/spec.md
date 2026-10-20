# Spec Delta

## ADDED Requirements

### Requirement: Candidate body materialization is bounded
Candidate enumeration SHALL determine visibility, exact task matching and ambiguity over the complete matching set in one read snapshot while materializing only a bounded display window of complete bodies. Memory used for candidate bodies SHALL scale with the requested window, not total retained history. Existing ordering, explicit-source and response-budget semantics SHALL remain intact.

#### Scenario: Large history is enumerated
- **WHEN** a workspace has many retained checkpoints and a candidate limit of five
- **THEN** at most five complete source bodies are materialized for the window

#### Scenario: A second candidate falls outside the window
- **WHEN** the requested limit is one but another matching stale or completed source exists
- **THEN** the result remains ambiguous and does not automatically select the displayed source

#### Scenario: A matching task is beyond unrelated history
- **WHEN** an exact task filter matches an older candidate after many unrelated candidates
- **THEN** that matching candidate remains eligible in the snapshot

#### Scenario: Recorded times have equivalent textual formats
- **WHEN** sources use different valid timestamp representations
- **THEN** ordering follows their actual time values and the existing session-ID tie break

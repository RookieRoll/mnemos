# Spec Delta

## ADDED Requirements

### Requirement: Continuity context has an explicit scoped delivery path

Opt-in continuity startup and explicit recovery SHALL use the resolved owner and verified workspace. Recovery SHALL follow pinned core items and precede other sections without reordering those sections. Legacy recent-session delivery SHALL be omitted in this mode; session progress SHALL come only from the scoped continuity source path. Calls without continuity enabled SHALL retain existing behavior.

#### Scenario: Legacy data shares a project basename
- **WHEN** unbound or differently bound sessions have the same project label as the current workspace
- **THEN** their goals, summaries and reflections are absent from the continuity block and cannot enter through a legacy recent-session section

#### Scenario: No continuation intent is supplied
- **WHEN** a continuity-enabled startup discovers progress candidates
- **THEN** it delivers only a bounded candidate offer until source selection satisfies the continuity contract

#### Scenario: A legacy caller starts normally
- **WHEN** continuity is not enabled
- **THEN** its existing section ordering and rendering contract are preserved

### Requirement: Recovery provenance and cautions fit the declared budget

Rendered recovery SHALL include source kind, session identifier, checkpoint revision when present, recorded time and applicable stale or workspace-change cautions. It SHALL preserve safety wrapping and a current-workspace verification instruction. All framing SHALL count toward the existing final context budget. A complete source representation SHALL be selected or omitted, never shortened into an unqualified assertion.

#### Scenario: Source warnings exhaust the available budget
- **WHEN** the progress body fits but its required provenance and warnings do not
- **THEN** the source is omitted instead of delivered without those annotations

#### Scenario: A multilingual checkpoint is compacted
- **WHEN** only a compact source representation fits
- **THEN** it remains valid UTF-8, identifies the source and retains all applicable warnings

#### Scenario: A checkpoint contains instruction-like text
- **WHEN** it is selected into context
- **THEN** it passes the existing safety handling and is presented as reported progress rather than a new system rule

### Requirement: Continuity delivery does not imply memory consumption

Continuity source references SHALL be returned separately from observation and skill surfacing references and SHALL identify only actually delivered sources. Candidate-only or size-omitted bodies SHALL NOT be reported as delivered. Continuity reads and rendering SHALL NOT create injection, consumption, claim or successful-use records, nor persist candidate lists or rendered context copies.

#### Scenario: A selected source is omitted during rendering
- **WHEN** its complete representation exceeds the final context budget
- **THEN** no continuity delivery reference claims its body was emitted

#### Scenario: Another session reads the same checkpoint
- **WHEN** that source was already rendered for a different session
- **THEN** it remains readable and no ownership or consumption state changes

#### Scenario: Ordinary memories accompany recovery
- **WHEN** observations or skills are also selected into the final context
- **THEN** their existing surfacing references and logging keep their original semantics without adding checkpoint IDs to them

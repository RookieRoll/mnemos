# memory-context-delivery Specification

## Purpose

Defines bounded memory delivery whose rendered content, safety annotations, and recorded surfacing references describe the same selected items.

## Requirements

### Requirement: Item-complete budget selection

Prewarm and explicit context SHALL select independently identifiable items within the declared estimated token budget, including headers, separators, and annotations. An item SHALL appear as a complete full or compact representation, or be omitted. Unicode text SHALL remain valid.

#### Scenario: Only one item fits
- **WHEN** a section contains three items and only the first item's complete representation fits
- **THEN** only that item appears and no fragment of the other items appears

#### Scenario: Long multilingual item
- **WHEN** an item contains long Chinese text and its full representation exceeds the remaining budget
- **THEN** a complete bounded representation is selected or the item is omitted without invalid UTF-8

#### Scenario: Framing consumes the remaining budget
- **WHEN** item content fits alone but its header and required annotations do not
- **THEN** the item is omitted and the estimate does not exceed the declared budget

### Requirement: Existing priorities and warnings survive selection

Delivery SHALL preserve mode-specific section priority and the pinned Tier-0 ordering and overflow contract. A selected item SHALL retain applicable safety and contradiction warnings. Shortening SHALL NOT turn flagged or contradicted content into an apparently unqualified fact.

#### Scenario: Contradicted item is shortened
- **WHEN** only a compact representation of a contradicted observation fits
- **THEN** it includes the contradiction warning and a visible observation identifier

#### Scenario: Safety wrapping exceeds budget
- **WHEN** an item fits without its mandatory safety warning but not with it
- **THEN** the item is omitted rather than delivered without the warning

#### Scenario: Pinned entries overflow
- **WHEN** scoped pinned entries exceed their allocation
- **THEN** pinned entries lead in the prescribed order and the block reports shown versus available

### Requirement: Truthful surfacing references

Returned and logged surfacing references SHALL identify exactly the unique observation or skill items selected into final context. Dropped candidates SHALL NOT be recorded. Logging failure SHALL NOT prevent delivery, and a logged event SHALL NOT claim that a host consumed the text or that an agent used it successfully.

#### Scenario: A later item is dropped
- **WHEN** a section is partially selected because its remaining items exceed budget
- **THEN** references and injection events contain only the selected items

#### Scenario: The recorder is unavailable
- **WHEN** selected context is built but recording fails
- **THEN** context and selected references are still returned

### Requirement: Deterministic non-semantic deduplication

For fixed inputs, configuration, and time, selection SHALL produce the same text and references. Repeated references to the same item SHALL be emitted once using the highest-priority occurrence. Different observations SHALL NOT be merged merely because their text or topic is similar.

#### Scenario: One observation is pinned and also matches a correction section
- **WHEN** both candidate occurrences refer to the same observation
- **THEN** the pinned occurrence is emitted and logged once

#### Scenario: A rule and its exception use similar wording
- **WHEN** two different observations express a rule and an exception
- **THEN** similarity alone does not cause either to be removed

### Requirement: Delivery computation is transient

Candidates, render plans, snippets, token estimates, selection explanations, and final context copies SHALL NOT be persisted as new memories or new database records. Delivery SHALL reuse the existing minimal injection-event facility without storing raw prompts or rendered bodies in those events.

#### Scenario: Repeated context construction
- **WHEN** context is constructed repeatedly over an unchanged observation set
- **THEN** no render-cache or derived-observation rows are created and only existing permitted surfacing events are added

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

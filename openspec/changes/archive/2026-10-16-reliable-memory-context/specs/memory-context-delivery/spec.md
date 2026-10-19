# Spec Delta

## Purpose

Defines bounded memory delivery whose rendered content, safety annotations, and recorded surfacing references describe the same selected items.

## ADDED Requirements

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

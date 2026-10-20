# Spec Delta

## ADDED Requirements

### Requirement: Search configuration is consistent across entry points
The same corpus, configured search parameters and clock SHALL yield equivalent ranking semantics through CLI, hooks, MCP and HTTP. An explicitly configured hybrid alpha of zero SHALL remain zero; omitted settings SHALL retain documented defaults. Capability-only and continuity-only operations SHALL not require embedding-provider discovery.

#### Scenario: Non-default search weights are configured
- **WHEN** the same data and query use non-default decay and hybrid alpha through CLI and server entry points
- **THEN** their service ranking parameters agree

#### Scenario: Pure vector weighting is explicit
- **WHEN** hybrid alpha is explicitly zero
- **THEN** configuration and service construction preserve that value

#### Scenario: Continuity capabilities are queried
- **WHEN** an operator only queries continuity capability support
- **THEN** no embedding provider is contacted or initialized

### Requirement: Vector signals belong to a compatible embedding space
Vector recall and fusion SHALL use only nonzero stored vectors whose model identifier and actual dimension match the active embedder and valid query vector. Missing or incompatible metadata SHALL be an absent signal, preserving keyword-only scoring when keyword weight is nonzero and honest mode reporting. Stored vectors SHALL remain unchanged until explicit maintenance.

#### Scenario: A different model has the same dimension
- **WHEN** a stored vector has the query dimension but another model identifier
- **THEN** it contributes neither vector-only recall nor a fusion signal

#### Scenario: Stored dimensions differ
- **WHEN** a keyword candidate carries an incompatible vector dimension
- **THEN** its keyword relevance is not reduced by a fabricated zero vector signal

#### Scenario: A compatible semantic-only memory exists
- **WHEN** its compatible vector matches the query without keyword overlap
- **THEN** independent vector recall can still return it

#### Scenario: Pure semantic weighting has no compatible candidate signal
- **WHEN** alpha is zero and a usable query vector exists but a candidate has no compatible vector
- **THEN** that candidate is excluded because its remaining keyword source has zero configured weight

#### Scenario: The query vector cannot be used
- **WHEN** query embedding fails or returns an invalid vector, including with alpha zero
- **THEN** the query falls back to keyword scoring and reports FTS mode

#### Scenario: Pure keyword weighting is configured
- **WHEN** alpha is one
- **THEN** query embedding and vector recall are skipped and retrieval reports FTS mode

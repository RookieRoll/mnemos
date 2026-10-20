# Spec Delta

## MODIFIED Requirements

### Requirement: pi continuity startup is opt-in and session isolated

When file configuration sets continuity.enabled true and integration.disabled false, pi SHALL attempt the versioned continuity startup path. Each real session_start event SHALL have a new invocation key and in-memory generation; retries of that invocation SHALL retain its key. Results from superseded generations SHALL NOT replace current session identity or context. Without the opt-in, existing startup SHALL remain unchanged.

#### Scenario: A new or resumed pi session replaces the old one
- **WHEN** the old startup response arrives after the new session_start event
- **THEN** the response cannot become the new session's Mnemos ID or prewarm block

#### Scenario: The installed binary is older
- **WHEN** a side-effect-free capability check proves continuity unsupported
- **THEN** pi performs its existing prewarm startup without a continuity claim

#### Scenario: A startup result is unknown
- **WHEN** open may have executed but times out or returns an invalid receipt
- **THEN** pi continues without blocking the turn and does not open a fallback session with another key

#### Scenario: Prewarm fails after continuity open
- **WHEN** pi receives a valid committed session identity but context is unavailable
- **THEN** it retains that identity for its own generation and does not create another session

### Requirement: pi checkpoint writes are explicit and accurately reported

The shipped skill SHALL describe explicit checkpoint save, clear and source-read operations using actual available tool naming. Checkpoint writes SHALL be recognized by the existing suffix-aware guardrail across direct and package installations. The extension SHALL NOT fabricate a checkpoint from raw prompts or tool output. Explicit write failures SHALL reach the calling agent, while automatic integration failures retain existing nonblocking degradation.

#### Scenario: A package-prefixed checkpoint tool is called
- **WHEN** its submitted body trips the write-boundary scanner
- **THEN** the write is blocked regardless of the adapter's package prefix

#### Scenario: A checkpoint save fails
- **WHEN** an explicit tool request encounters a storage error
- **THEN** its result reports failure rather than a successful save inferred from empty output

#### Scenario: pi shuts down without a new checkpoint
- **WHEN** the session-end hook runs
- **THEN** it closes the exact current session under the existing lifecycle contract without claiming to have generated or saved a final checkpoint

#### Scenario: Automatic integration is disabled
- **WHEN** file configuration disables automatic integration even though continuity opt-in is present
- **THEN** no automatic continuity startup, offer or side effect occurs, while explicit MCP tool availability retains its existing policy

## ADDED Requirements

### Requirement: Pi shares the installed file configuration route
Pi SHALL obtain its executable and explicit config selector from its package MCP entry and validate a nonsecret configuration projection per session generation. Hooks and MCP SHALL use the same route. Invalid or ambiguous routes and stale responses SHALL not trigger guessed startup or store selection.

#### Scenario: Route contains cwd-dependent relative paths
- **WHEN** adapter and hook execution cannot guarantee identical interpretation
- **THEN** pi reports the route problem and does not silently open another store

#### Scenario: An earlier config response arrives after reload
- **WHEN** a new session generation already exists
- **THEN** the response cannot replace its route, switches or session identity

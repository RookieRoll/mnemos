# Spec Delta

## ADDED Requirements

### Requirement: pi continuity startup is opt-in and session isolated

When MNEMOS_CONTINUITY is `1` and the existing disable switch is unset, pi SHALL attempt the versioned continuity startup path. Each real session_start event SHALL have a new invocation key and in-memory generation; retries of that invocation SHALL retain its key. Results from superseded generations SHALL NOT replace current session identity or context. Without the opt-in, existing startup SHALL remain unchanged.

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

### Requirement: Unknown continuity identity cannot trigger legacy session inference

Until continuity startup confirms the current generation's exact session ID, pi SHALL remain unbound and suppress automatic session-dependent prompt, touch, compaction and shutdown calls. It SHALL NOT send an empty ID into legacy inference. Write-boundary scanning SHALL continue through a session-independent guardrail-only path. Suppressed events SHALL NOT be replayed after binding, and explicit MCP tools SHALL retain their own result semantics.

#### Scenario: An uncertain startup is followed by prompts and shutdown
- **WHEN** another session in the workspace is open and the current startup outcome remains unknown
- **THEN** prompt, touch and shutdown events cannot update or close the other session through an empty-ID fallback

#### Scenario: A dangerous write occurs while startup is unbound
- **WHEN** the input trips the write-boundary scanner
- **THEN** the guardrail still blocks it without resolving, injecting into or mutating any session

#### Scenario: The original startup is confirmed later
- **WHEN** the same generation obtains its exact session ID using the original start key
- **THEN** subsequent automatic events use that ID and earlier suppressed events are not replayed

### Requirement: pi delivers recovery only through supported context channels

pi SHALL deliver startup recovery and candidate offers through its non-accumulating before_agent_start context channel. Explicit later recovery SHALL be available as a tool result. The extension SHALL NOT try to inject recovery from tool_call, silently select an ambiguous source, or push other sessions' progress in the background. Existing ordinary prompt-memory behavior SHALL remain unchanged.

#### Scenario: Multiple matching tasks are available
- **WHEN** startup returns an ambiguous candidate offer
- **THEN** pi presents that bounded offer without treating the newest source as selected

#### Scenario: A file edit occurs
- **WHEN** tool_call runs the guardrail
- **THEN** no recovery system-prompt rewrite is attempted through that event

#### Scenario: Another session publishes a new checkpoint
- **WHEN** the current pi session has not requested another recovery read
- **THEN** the new progress is not automatically pushed into its ongoing turn

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
- **WHEN** MNEMOS_DISABLED is set even though continuity opt-in is present
- **THEN** no automatic continuity startup, offer or side effect occurs, while explicit MCP tool availability retains its existing policy

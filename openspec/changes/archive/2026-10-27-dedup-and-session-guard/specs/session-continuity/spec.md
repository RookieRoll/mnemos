## ADDED Requirements

### Requirement: Session end ownership guard

`mnemos_session_end` SHALL close only a session the caller is entitled to close:
the session the invocation is bound to, or a session carried by a continuity
receipt whose workspace binding matches the invocation's workspace. Ownership
SHALL be decided by workspace binding rather than by agent identity. The host
hook paths SHALL resolve their session themselves and SHALL be unaffected.

#### Scenario: own session closes

- **WHEN** the MCP tool is invoked with the session id the invocation is bound to
- **THEN** the session is closed and the summary stored as before

#### Scenario: foreign UUIDv7 rejected

- **WHEN** the tool is invoked with a session id that is neither the bound session nor covered by a continuity receipt
- **THEN** the call fails with `session_not_owned`, the session is left open, and the error text names the supported route (`mnemos sessions close <id>`)

#### Scenario: receipt authorises across agents in one workspace

- **WHEN** the caller holds a valid continuity receipt for the target session and the receipt's workspace matches the invocation's workspace, while the agent identity differs
- **THEN** the close is accepted

#### Scenario: receipt from another workspace rejected

- **WHEN** the only receipt for the target session binds a different workspace
- **THEN** the call fails with `session_not_owned`

#### Scenario: hook path unaffected

- **WHEN** a session is closed through the host hook path
- **THEN** the host session is resolved as before and no ownership rejection occurs

### Requirement: Local close as trusted maintenance channel

The local CLI `mnemos sessions close` SHALL remain able to close any session id,
because it is a trusted maintenance channel reached with local operator authority,
and it SHALL be the documented route for orphaned sessions and batch closure.

#### Scenario: orphan closure via CLI

- **WHEN** an operator closes an orphaned session id locally
- **THEN** the close succeeds regardless of the MCP ownership guard

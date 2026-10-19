# Spec Delta

## ADDED Requirements

### Requirement: Continuity is an explicit versioned protocol

New continuity operations SHALL use explicit protocol version 1 with typed outcomes through CLI, MCP, HTTP and the Go client. Equivalent inputs SHALL enforce equivalent identity, visibility, revision, limits and receipt semantics. Legacy hook invocations SHALL retain their existing output and exit behavior. Legacy empty results SHALL NOT be interpreted as committed checkpoint receipts.

#### Scenario: Existing neutral hooks return empty context
- **WHEN** an existing side-effect hook emits its legacy empty result
- **THEN** no caller treats that result as checkpoint persistence confirmation

#### Scenario: A new checkpoint request fails validation
- **WHEN** equivalent invalid inputs are sent through the supported transports
- **THEN** each reports a validation failure without a success receipt; CLI uses a nonzero exit and MCP/HTTP expose the corresponding explicit error

#### Scenario: Default Claude hook behavior is checked
- **WHEN** unchanged inputs are run without opting into continuity
- **THEN** default stdout, stderr and exit status remain compatible with the pre-change binary

### Requirement: Startup fallback requires proof of no new startup effect

A client SHALL use a legacy startup fallback only after a side-effect-free capability check establishes unsupported behavior or a confirmed unknown-command result establishes that continuity open did not execute. After open may have executed, timeout, invalid JSON, legacy envelopes, unsupported receipt versions and missing receipt fields SHALL mean unknown outcome. Recovery SHALL reuse the original start key rather than start a fallback session.

#### Scenario: An older binary lacks continuity support
- **WHEN** a capability check establishes that the operation is unsupported without opening a session
- **THEN** the client can perform its existing legacy startup

#### Scenario: Open committed but its response is malformed
- **WHEN** the client cannot validate the returned startup receipt
- **THEN** it does not create a legacy fallback session and can retry the original start key

#### Scenario: Open times out
- **WHEN** the caller does not know whether a session was committed
- **THEN** it neither reports success nor creates another session with a new key

### Requirement: Prewarm after continuity open is identity preserving

After continuity open commits, prewarm SHALL compose context for that exact returned session without creating or adopting another session. Context failure SHALL NOT undo or hide the committed identity. Retrying startup with the same key SHALL confirm that identity. Checkpoint writes SHALL be subject to the existing write-boundary scanner regardless of transport or host tool naming.

#### Scenario: Context composition fails after a session opens
- **WHEN** continuity open has committed its session but prewarm cannot complete
- **THEN** the response preserves the committed session identity with an explicit context-unavailable result and no replacement session is opened

#### Scenario: A later retry obtains context
- **WHEN** the caller retries that same startup request after context becomes available
- **THEN** the original session identity is reused

#### Scenario: A checkpoint tool uses an adapter prefix
- **WHEN** a harness supplies a prefixed checkpoint write-tool name under its supported naming contract
- **THEN** the write is still covered by the write-boundary scanner

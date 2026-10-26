# agent-workflow — delta for cli-maintenance-contract

## ADDED Requirements

### Requirement: Prewarm Opens A Session Only For Hook Invocations

`mnemos prewarm` SHALL open a Mnemos session only when the invocation supplies a
hook payload. A payload is supplied when either an explicit `--payload <json>`
argument is present, or standard input is not a character device and contains a
JSON object carrying `hook_event_name` or `session_id`.

When no payload is present, `mnemos prewarm` SHALL be read-only: it composes and
prints the prewarm block and MUST NOT create, modify, or close any session.

The `--open-session` flag SHALL force the session-open behaviour regardless of
payload detection.

Standard input MUST NOT be read when it is a terminal, so an interactive
invocation cannot block.

#### Scenario: Hook invocation via argv payload

- **WHEN** `mnemos prewarm --payload '{"hook_event_name":"session_start","session_id":"..."}'` runs
- **THEN** a session is opened as before this change and the prewarm block is printed

#### Scenario: Hook invocation via stdin payload

- **WHEN** `mnemos prewarm` runs with a JSON hook payload piped on stdin
- **THEN** the payload is parsed, a session is opened, and the prewarm block is printed

#### Scenario: Hand-run prewarm is read-only

- **WHEN** a human runs `mnemos prewarm` on a terminal with no `--payload`
- **THEN** the prewarm block is printed, stdin is not read, and the session table is unchanged

#### Scenario: Forced session open

- **WHEN** a human runs `mnemos prewarm --open-session` with no payload
- **THEN** a session is opened and the prewarm block is printed

#### Scenario: Installer command strings unchanged

- **WHEN** the installer byte-comparison tests run against a pre-change binary
- **THEN** they pass, because no hook command string or payload shape was modified

## MODIFIED Requirements

### Requirement: Shared Task Session Flow

The shared task-session flow SHALL keep hook entry points byte-compatible with
pre-change installations. Adding payload-conditional behaviour MUST be confined
to the CLI handler and MUST NOT alter the command strings generated for any
harness adapter.

#### Scenario: Hook command strings are frozen

- **WHEN** the generated hook configuration for any supported harness is compared against the pre-change output
- **THEN** it is identical

# harness-neutral-hooks Specification

## Purpose
Gives mnemos' harness-side hook commands a transport-neutral input and output contract, so any agent harness can consume mnemos' hook decisions — memory recall, capture directives, and the write-boundary guardrail — without reimplementing them, while the existing Claude Code rendering stays byte-for-byte unchanged.

## Requirements

### Requirement: Hook payloads can be supplied as an argument

Every hook subcommand SHALL accept its event payload as a JSON argument in addition to reading it from standard input. When the argument is supplied it MUST take precedence over standard input. When neither is present the subcommand MUST behave as it does today.

#### Scenario: Payload argument supplies the event

- **WHEN** a hook subcommand is invoked with a JSON payload argument describing a user prompt
- **THEN** the subcommand processes that payload as if it had been read from standard input

#### Scenario: Standard input still works without the argument

- **WHEN** a hook subcommand is invoked with no payload argument and a JSON payload on standard input
- **THEN** the subcommand processes the standard-input payload

#### Scenario: The argument wins when both are present

- **WHEN** a hook subcommand is invoked with both a payload argument and a different payload on standard input
- **THEN** the payload argument is the one processed

#### Scenario: A malformed argument does not fail the hook

- **WHEN** a hook subcommand is invoked with a payload argument that is not valid JSON
- **THEN** the subcommand exits successfully having produced no context and no block decision

### Requirement: Hooks can emit a transport-neutral result

When the neutral result format is selected, a hook subcommand SHALL emit exactly one JSON object on standard output describing the outcome of the hook: the context to inject, whether the harness should block the triggering action, the reason to show when it should, and the mnemos session identifier when one was established or reused.

#### Scenario: A prompt hook reports injectable context

- **WHEN** the user-prompt hook runs with the neutral format for a payload whose prompt has on-topic prior memory
- **THEN** the emitted object contains the assembled context text and reports no block

#### Scenario: A pre-tool hook reports a block decision

- **WHEN** the pre-tool hook runs with the neutral format for a write-tool payload that trips the write-boundary scanner
- **THEN** the emitted object reports a block and carries the reason to show

#### Scenario: A side-effect-only hook emits an empty result

- **WHEN** the post-tool or session-end hook runs with the neutral format
- **THEN** the emitted object is well-formed, reports no block, and carries no context

#### Scenario: Nothing to report is not an error

- **WHEN** a hook runs with the neutral format and has no memory to inject
- **THEN** the emitted object is still well-formed and the subcommand exits successfully

### Requirement: The default CLI rendering is unchanged

Without the neutral result format selected, every hook subcommand SHALL produce the same standard output, the same standard error, and the same exit status as before this change for the same payload. This applies in particular to the pre-tool hook's blocked exit status, which the Claude Code integration depends on.

#### Scenario: Prompt hook output is byte-identical

- **WHEN** the user-prompt hook runs without the neutral format for a payload
- **THEN** its standard output is identical to the output produced before this change for that payload

#### Scenario: Blocked pre-tool hook keeps its exit status

- **WHEN** the pre-tool hook runs without the neutral format for a payload that trips the write-boundary scanner
- **THEN** it exits with the same non-zero status as before this change

#### Scenario: Side-effect-only hooks stay silent

- **WHEN** the post-tool, session-end, or pre-compact hook runs without the neutral format
- **THEN** it produces no standard output

### Requirement: A guardrail block short-circuits the rest of the pre-tool work

When the pre-tool hook decides to block, it SHALL report the block and MUST NOT also push memory for the same invocation. This ordering is part of the contract, not an incidental consequence of control flow.

#### Scenario: A blocked edit yields no memory block

- **WHEN** the pre-tool hook blocks an edit because its content tripped the scanner
- **THEN** the emitted result reports the block and carries no context

#### Scenario: A non-blocked edit still gets its memory

- **WHEN** the pre-tool hook does not block an edit on a file with on-topic memory
- **THEN** the emitted result carries the context and reports no block

### Requirement: Only the default format signals a block by exit status

A pre-tool block SHALL be communicated to a harness that selected the neutral result format through that result, and MUST NOT do so through a non-zero exit status. The default format MUST continue to signal a block with the non-zero status the existing Claude Code integration depends on. The reason given SHALL be the same text in both formats.

#### Scenario: The neutral format exits successfully on a block

- **WHEN** the pre-tool hook runs with the neutral format for a payload that trips the write-boundary scanner
- **THEN** the emitted result reports the block, carries the reason, and the subcommand exits successfully

#### Scenario: The default format still exits non-zero on a block

- **WHEN** the pre-tool hook runs with the default format for the same payload
- **THEN** the subcommand exits with a non-zero status and the reason is on standard error

#### Scenario: The reason is the same text either way

- **WHEN** the same blocking payload is processed once in each format
- **THEN** the reason text in the neutral result is identical to the text written to standard error in the default format

### Requirement: Format selection does not change hook decisions

The decision a hook reaches MUST be independent of the requested output format. For one payload, the same context text, the same block decision, and the same reason MUST be produced regardless of format, and each invocation MUST record at most one set of surfaced memories in the injection log.

#### Scenario: Both formats agree on the decision

- **WHEN** the same payload is processed once with the neutral format and once without
- **THEN** the context text and block decision are the same in both runs

#### Scenario: Surfaced memories are logged once per invocation

- **WHEN** a hook runs with the neutral format for a payload that has on-topic prior memory
- **THEN** the injection log records that surfaced set exactly once, not once per rendering path

### Requirement: The write-boundary guardrail is consistent across harnesses

The pre-tool hook SHALL evaluate write-tool payloads with the same safety scanner and the same risk threshold as the existing Claude Code path, and the block reason it reports MUST name the same triggered rules.

#### Scenario: A high-risk payload is reported with its rules

- **WHEN** the pre-tool hook processes a write-tool payload containing a prompt-injection pattern at or above the blocking risk
- **THEN** it reports a block and the reason names the triggered rules and the assessed risk

#### Scenario: A write payload that trips the scanner reports the same decision either way

- **WHEN** the pre-tool hook processes a write-tool payload that trips the write-boundary scanner, once with the neutral format and once without
- **THEN** it reports a block with the same reason in both runs

#### Scenario: A payload below the threshold is not blocked

- **WHEN** the pre-tool hook processes a write-tool payload whose highest risk is below the blocking risk
- **THEN** it reports no block

#### Scenario: Non-write tools are not evaluated

- **WHEN** the pre-tool hook processes a payload for a tool that is not one of mnemos' write tools
- **THEN** it reports no block and no context

# pi-integration Specification

## Purpose
Delivers mnemos inside the pi coding agent at parity with its other registered hosts: the MCP tool surface, plus the harness-side push — session prewarm, per-prompt memory recall, the capture directive, the write-boundary guardrail, file touches, and session close — that mnemos delivers to Claude Code today.

What each push achieves is not uniform, and this capability does not claim otherwise. The two scenarios where mnemos' published A/B measurement shows a full on/off gap are memory-application scenarios: they turn on whether stored memory reaches the model before it acts. The capture scenarios sit at a 53% on-arm ceiling in that same measurement, and they are already measured with the capture directive present in context — pushing the directive is therefore a precondition for that number, not a route past it.

## Requirements

### Requirement: mnemos tools are visible in pi without a discovery step

When mnemos is wired into pi, its tools SHALL be registered so the model can call them without first searching for them, and SHALL be namespaced so they cannot collide with another server's tools.

#### Scenario: A mnemos tool is callable without discovery

- **WHEN** a pi session has mnemos wired up and the model decides to record a correction
- **THEN** the record-a-correction tool is available to call without a preceding search step

#### Scenario: Unrelated MCP servers keep their own naming

- **WHEN** pi has more than one MCP server configured
- **THEN** mnemos' naming choice does not rename any other server's tools

### Requirement: The guardrail matches write tools independently of naming scheme

The pi-side guardrail SHALL recognize mnemos' write tools by name suffix, and SHALL recognize pi's file-editing tools by their own registered names, so that every combination of adapter prefix mode and installation route — including installation as a pi package, where the adapter prefixes the server name with the package name — still subjects mnemos writes to the write-boundary scanner and still injects file-relevant memory before pi's edits.

#### Scenario: A write through a directly configured server is scanned

- **WHEN** mnemos is configured directly in pi's MCP configuration and the model calls a mnemos write tool with a payload that trips the scanner
- **THEN** the call is blocked with a reason naming the triggered rules

#### Scenario: A write through a package-declared server is scanned

- **WHEN** mnemos is installed as a pi package, so the adapter exposes its tools under a package-derived name
- **THEN** a mnemos write tool called with a payload that trips the scanner is still blocked

#### Scenario: An unrelated tool is not treated as a mnemos write

- **WHEN** the model calls a tool whose name does not identify it as a mnemos write tool
- **THEN** the guardrail does not evaluate it and reports no block

#### Scenario: A write under any adapter prefix is scanned

- **WHEN** a harness without a dispatch matcher submits a mnemos write tool under a name the adapter derived from the server key, the prefix mode, or a package manifest
- **THEN** the write is evaluated by the scanner and blocked when its content trips the blocking risk

#### Scenario: The strict match is the default for the matcher-driven path

- **WHEN** the guardrail is reached without a harness declaring that it has no dispatch matcher
- **THEN** only the exact names the Claude Code matcher is installed for are treated as mnemos writes

#### Scenario: A pi file edit is recognized as an edit

- **WHEN** the model calls one of pi's file-editing tools with a casing that differs from the Claude Code matcher's
- **THEN** it is treated as a file edit and file-relevant memory is pushed before it runs

### Requirement: The shipped skill describes the tools pi actually exposes

The shipped skill's statement of how its tools are addressed SHALL match the names that pi exposes for mnemos, so an agent following the skill can address a real tool.

#### Scenario: The skill's tool address resolves

- **WHEN** the model follows the shipped skill's instruction to call a specific mnemos write tool by name
- **THEN** that name identifies a callable tool in that session

#### Scenario: The skill does not hard-code a name that only one naming scheme produces

- **WHEN** mnemos' tools are exposed under a package-derived name
- **THEN** the shipped skill still describes them in a way that lets the model address them

### Requirement: pi installs mnemos with one command

A single pi package install SHALL wire all three parts — the MCP server entry, the mnemos skill, and the harness hooks — without hand-editing pi configuration files.

#### Scenario: Install wires every part

- **WHEN** a user installs the mnemos pi package
- **THEN** the MCP server entry, the mnemos skill, and the hook extension are all active in the next pi session

#### Scenario: Removal leaves nothing behind

- **WHEN** a user removes the mnemos pi package
- **THEN** no mnemos-owned MCP server entry, skill, or extension remains registered

### Requirement: Session start pushes a prewarm block

mnemos SHALL push its prewarm block into a pi session without the model having to call a tool, and the block SHALL carry the mnemos session identifier so later writes in that session are scoped to it.

#### Scenario: Prewarm arrives before the model acts

- **WHEN** a pi session starts in a project that has stored conventions or corrections
- **THEN** the prewarm block is present in the model's context without the model having called a mnemos tool

#### Scenario: The session identifier is reusable

- **WHEN** the prewarm block has been pushed
- **THEN** the mnemos session identifier it carries is the one used for that session's subsequent mnemos writes

#### Scenario: An empty store is not an error

- **WHEN** a pi session starts against a store with nothing relevant to surface
- **THEN** the session proceeds normally with no prewarm content and no error shown to the user

#### Scenario: A new pi session does not reuse a stale mnemos session

- **WHEN** a pi session starts in a project whose most recent mnemos session was opened long before
- **THEN** a new mnemos session is opened rather than adopting the stale one

### Requirement: Each prompt pushes on-topic memory and the capture directive

On each user prompt mnemos SHALL push memories relevant to that prompt, subject to the existing relevance floor, deduplication window, and per-prompt hit cap, and SHALL push the capture directive when the prompt's phrasing is correction-shaped, convention-shaped, or an explicit save request. This push SHALL apply to the current turn only and MUST NOT accumulate duplicate copies in the session's own history.

#### Scenario: Relevant memory reaches the model for that turn

- **WHEN** a user prompt is strongly on-topic for stored memory
- **THEN** the matching memories are present in the model's context for that turn

#### Scenario: The same memory does not ride along forever

- **WHEN** the same memory was already pushed into this project's context within the deduplication window
- **THEN** it is not pushed again

#### Scenario: Weak matches are not force-fed

- **WHEN** a user prompt's best matching memory is below the relevance floor
- **THEN** no memory block is pushed

#### Scenario: Correction-shaped phrasing triggers the directive

- **WHEN** a user prompt contains correction-shaped phrasing
- **THEN** the capture directive instructing the model to record a correction is present in the model's context for that turn

#### Scenario: Convention-shaped phrasing triggers the directive

- **WHEN** a user prompt declares a project convention
- **THEN** the capture directive instructing the model to record a convention is present in the model's context for that turn

#### Scenario: The push does not accumulate in session history

- **WHEN** several prompts in one pi session each receive a pushed block
- **THEN** the session's own message history does not contain one accumulating copy per prompt

### Requirement: Edits receive the write-boundary guardrail

Before a file-editing tool runs, mnemos SHALL evaluate the write-boundary guardrail. When the content being written trips the scanner, mnemos SHALL block the call and report why.

**Note on the memory half.** The retrieval still runs on this path and its injection log entry is still written, but pi's `tool_call` event accepts only `block`, `reason`, and `terminate` — it cannot alter the system prompt, and the turn's messages are already fixed. The memory therefore cannot reach the model from this event. The pushes that do reach the model are prewarm and the per-prompt context, both delivered on `before_agent_start`. On Claude Code the equivalent push does arrive, as `hookSpecificOutput.additionalContext`; this is a pi limitation, recorded in `design.md` D3.

#### Scenario: A blocked edit does not also receive memory

- **WHEN** a file-editing tool call is blocked by the guardrail
- **THEN** the call is reported as blocked and no memory block is pushed for it

#### Scenario: A high-risk write is blocked with a reason

- **WHEN** a file-editing or mnemos-write tool is about to run with content that trips the write-boundary scanner
- **THEN** the tool call is blocked and the model is told why, naming the triggered rules

#### Scenario: An ordinary edit is unaffected

- **WHEN** a file-editing tool is about to run with ordinary content and no on-topic memory
- **THEN** the call proceeds normally with no block and no injected context

#### Scenario: The pre-tool push is not claimed where pi cannot deliver it

- **WHEN** the pre-tool hook returns context for a clean edit
- **THEN** the extension does not attempt a system-prompt rewrite on the `tool_call` event, because that event has no such channel and a return value there is silently discarded

### Requirement: File edits are recorded without model cooperation

mnemos SHALL record a passive file touch, scoped to the files a session actually changed, after a file-editing tool call completes successfully. This MUST NOT require the model to call a mnemos tool.

#### Scenario: A successful edit records a touch

- **WHEN** a file-editing tool call changes a file and completes successfully
- **THEN** that file is recorded in the project's touch heat map

#### Scenario: A failed edit records nothing

- **WHEN** a file-editing tool call completes with an error
- **THEN** no touch is recorded for it

### Requirement: Session end closes the mnemos session

mnemos SHALL close the mnemos session when the pi session ends. This MUST NOT require the model to call a mnemos tool.

#### Scenario: Session end closes the mnemos session

- **WHEN** a pi session ends while its mnemos session is still open
- **THEN** the mnemos session is closed with a summary and a status

#### Scenario: An already-closed session is left alone

- **WHEN** the model already closed its mnemos session before the pi session ended
- **THEN** the session-end path does not reopen or overwrite it

### Requirement: mnemos can be paused and its state inspected from pi

Setting the existing disable switch SHALL suppress every pi-side push and side effect while leaving the MCP tools available. Diagnostic coverage SHALL report whether mnemos is wired into pi and whether its hooks are present.

#### Scenario: The disable switch silences the pushes

- **WHEN** the disable switch is set and a pi session runs
- **THEN** no prewarm, memory block, capture directive, guardrail, touch, or session-close side effect occurs

#### Scenario: Diagnosis reports the pi host

- **WHEN** the health check runs on a machine with pi present
- **THEN** it reports the pi MCP registration and the presence of the pi hooks

### Requirement: Degraded mnemos never obstructs a pi session

Every pi-side push and side effect SHALL fail silently. An unreachable binary, a missing configuration, a storage error, or a malformed response MUST NOT block a pi session, surface an error to the user, or leave a tool call failed for a reason unrelated to the session's work.

#### Scenario: A missing binary does not break the session

- **WHEN** the mnemos binary is unavailable while pi runs
- **THEN** the session proceeds normally with no user-visible error and no blocked tool calls

#### Scenario: A slow hook does not stall the turn indefinitely

- **WHEN** a pi-side hook invocation exceeds its allotted time
- **THEN** the turn proceeds without the pushed content rather than waiting

#### Scenario: Hook state stays isolated between sessions

- **WHEN** a pi session is replaced by a new or resumed session
- **THEN** the previous session's hook state is discarded and the new session establishes its own

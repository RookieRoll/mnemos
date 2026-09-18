## 1. pi parity precondition

- [x] 1.1 Record the decided pi MCP configuration shape and the tool-naming derivation table in the change's `design.md` (D6), so the naming the matcher must tolerate is documented as a derivation rather than as one machine's observation. Verify the table covers every prefix mode and both installation routes, and that the observed names on this machine are recorded against it once a live pi session confirms them.
- [x] 1.2 Implement the pi-side write-tool matcher as a case-insensitive suffix match over the mnemos write-tool names, and implement the pi-side edit-tool match over pi's own registered editing-tool names (`edit`, `write`) alongside the Claude Code casings, and verify with unit tests that directly configured names, package-declared names, and the Claude Code form all match, that pi's lowercase edit names are recognized as edits, and that a same-suffix tool from an unrelated server does not match.
- [x] 1.3 Make `verify/runners/on.sh` and `verify/runners/off.sh` path-portable so the behavior and capture harnesses can run outside the author's machine, and verify both scripts pass a syntax check. Leave `verify/capture.yaml`'s `expect_tools` unchanged: those fixtures drive `claude -p`, so replacing Claude's tool names with pi's would turn a passing assertion into a failing one.
- [x] 1.4 Restate the shipped skill's tool addressing so it holds under more than one naming scheme instead of asserting a single literal prefix, and verify the frontmatter still parses as a valid skill in pi.
- [x] 1.5 Confirm against a live pi session that mnemos tools are callable without a discovery step, that the names match a row of the D6 table, and that unrelated MCP servers keep their own tool names.

## 2. Extract hook decisions from hook rendering (no behavior change)

- [x] 2.1 Change `emitPreToolMemory` in `cmd/mnemos/hook.go` to return a result value (context text plus the surfaced memory ids) instead of writing to an `io.Writer`, and verify the pre-tool tests in `cmd/mnemos/hook_test.go` pass unchanged.
- [x] 2.2 Split the prompt path of `runHookUserPrompt` into a goal-backfill step, a retrieval step returning the kept hits, and a directive step returning the detected capture signal, and verify the user-prompt hook tests pass unchanged.
- [x] 2.3 Add a Claude Code renderer that turns those result values back into today's exact stdout bytes (memory block, capture directive, and the `hookSpecificOutput.additionalContext` JSON), and verify byte-identical output with a table test over the existing hook fixtures.
- [x] 2.4 Confirm the guarded default path still exits with the blocked status for a high-risk pre-tool payload, by running the existing blocked-payload test and checking the exit status assertion.
- [x] 2.5 Run `go test ./cmd/mnemos/... ./internal/...` and confirm the whole suite passes with no golden-output changes.

## 3. Neutral hook payload and result format

- [x] 3.1 Define the neutral result envelope (`session_id`, `context`, `block`, `block_reason`) and its JSON encoding in `cmd/mnemos/hook.go`, and verify with a unit test that an empty result marshals to a well-formed object with no context and no block.
- [x] 3.2 Add a `--payload` flag to the hook subcommand dispatcher that overrides stdin, and verify with tests that an argv payload is processed, that stdin still works when the flag is absent, that argv wins when both are present, and that malformed JSON yields an empty result at exit 0.
- [x] 3.3 Add a `--format json` flag to the hook subcommands that selects the neutral renderer, and verify with tests that the neutral output parses and that omitting the flag still produces the Claude Code bytes.
- [x] 3.4 Make the pre-tool path emit its block decision in the neutral envelope when selected, and verify with tests that the block flag, the reason naming the triggered rules, and the no-block cases for below-threshold and non-write-tool payloads all match the Claude Code path's decisions.
- [x] 3.5 Add a test asserting that a blocked pre-tool invocation emits a result carrying the block and no context, and that a non-blocked invocation on a file with on-topic memory carries the context and no block.
- [x] 3.6 Verify that one invocation records surfaced memories exactly once regardless of format, by running the same payload through both formats against a test store and asserting a single injection-log entry.
- [x] 3.7 Add the same `--payload` and `--format json` support to `mnemos prewarm`, and verify `mnemos prewarm --project <p> --format json` still returns the prewarm text plus the session id.

## 4. pi host in the Go tooling

- [x] 4.1 Add the pi MCP config location to `DetectTargets` in `internal/installer/installer.go`, following the existing JSON target shape, and verify with a test that a temporary pi config directory produces a detected target, that a missing directory produces none, and that an override environment variable relocates the target the way it already does for Claude Code.
- [x] 4.2 Teach `mnemos init` to write the mnemos entry into the pi MCP config without disturbing unrelated server entries, mirroring the Codex TOML test structure, and verify idempotence with a second install that reports no change. Confirm the Claude Code hook block's gate is untouched by running the existing init tests.
- [x] 4.3 Add pi registration and pi hook checks to `mnemos doctor`, and verify by running `mnemos doctor` on a machine with pi present and confirming the new checks report their state.
- [x] 4.4 Verify that `mnemos init` and `mnemos doctor` treat a pi config that carries adapter-only fields (such as a tool-visibility or lifecycle setting on the mnemos entry) as already installed rather than drifting it, by running init twice against such a config and asserting the file is unchanged.

## 5. pi package scaffold

- [x] 5.1 Create `pi/` in the repository with a `package.json` carrying the `pi-package` keyword, a `pi` manifest declaring the extension, skill, and MCP config paths, and a peer dependency range for the pi packages the extension imports, and verify pi loads the package from a local path without error.
- [x] 5.2 Add the package-local MCP config declaring the mnemos server with direct tool registration, and verify the server entry becomes active after installing the package and that its tools are subject to the guardrail under the package-derived name.
- [x] 5.3 Wire the shipped `SKILL.md` into the package's skill path, and verify the skill is listed in a pi session after install.
- [x] 5.4 Verify a full install followed by a full uninstall leaves no mnemos-owned MCP entry, skill, or extension registered in pi.

## 6. Hook transport seam and spawn implementation

- [x] 6.1 Define the hook-invocation interface in the extension covering prewarm, prompt block, pre-tool, touch, session end, the disable switch, and a per-call timeout, and verify with a unit test that a stub implementation satisfies it and that the event handlers depend only on the interface.
- [x] 6.2 Implement the spawn transport by invoking the mnemos subcommands with the payload as an argument and parsing the neutral envelope, and verify with a test that a canned envelope is parsed and that a non-zero exit, malformed JSON, and a timeout each produce an empty result rather than an error.
- [x] 6.3 Implement the disable-switch check so it short-circuits every call before spawning, and verify with a test that no subcommand is invoked while the switch is set.
- [x] 6.4 Locate the mnemos binary and configuration using the same resolution the Go side uses, and verify that a deliberately wrong binary path degrades to empty results with no user-visible error.

## 7. pi event mappings

- [x] 7.1 Map `session_start` to prewarm and retain the returned session id in extension state, re-establishing that state on every `session_start` reason so a resumed or forked session does not inherit the previous one, and verify by starting, replacing, and resuming sessions and observing the id change.
- [x] 7.2 Map `before_agent_start` to the prompt block and return the combined memory block plus capture directive via `systemPrompt`, and verify that a correction-shaped prompt in a project with stored memory makes both the memory block and the directive visible to the model for that turn.
- [x] 7.3 Verify the prompt push does not accumulate: send several on-topic prompts in one pi session and confirm the session's message history does not grow one persisted copy per prompt.
- [x] 7.4 Map `tool_call` for both pre-tool concerns into one pre-tool invocation that decides the guardrail first, and verify with a test that a blocked call reports a block with no context while a non-blocked call on a file with on-topic memory carries that memory.
- [x] 7.5 Return `{ block, reason }` from `tool_call` when the neutral pre-tool result reports a block, and verify that a write payload containing a high-risk pattern is blocked with a reason naming the triggered rules and mentioning mnemos, and that a below-threshold payload and a non-write tool are not blocked.
- [x] 7.6 Verify an ordinary edit produces neither a block nor injected context, by editing a file with no on-topic memory and confirming the call proceeds unchanged.
- [x] 7.7 Map the file-editing `tool_result` case to the touch invocation and record a touch only for calls that completed without error, and verify with two cases — one successful file edit and one failing edit — that only the first appears in the project's touch heat map. Record explicitly in the task notes that "succeeded but changed nothing" is not separately detectable in pi's results, so this guarantee is error-vs-success and not change-detection.
- [x] 7.8 Map `session_shutdown` to the session-end invocation, and verify that an open mnemos session is closed with a summary after a pi session ends and that an already-closed session is not modified.

## 8. Robustness and isolation

- [x] 8.1 Give every hook call a timeout and verify that an artificially slow invocation lets the turn proceed without the pushed content rather than stalling.
- [x] 8.2 Verify that with the mnemos binary made unavailable, a full pi session — including prompts, edits, and exit — completes with no user-visible error and no unexpectedly blocked tool calls.
- [x] 8.3 Register an idempotent `session_shutdown` handler that releases any state the extension opened from `session_start`, and verify that repeated session replacement does not leak handlers or grow extension state.

## 9. End-to-end verification

- [x] 9.1 With `mnemos verify` untouched, run the five README behavior scenarios manually in pi against a seeded store and record the observed outcome for each. Report memory-application scenarios and capture scenarios separately, and state the capture result against the 53% on-arm ceiling the README already publishes with the directive present, rather than against the two full-gap scenarios. Label the whole result as unmeasured observation, not harness output, and confirm no shipped file claims a pi effect figure.
- [x] 9.2 Confirm existing Claude Code behavior is untouched by running the Claude Code path — prewarm, user-prompt, post-tool, session-end, and the pre-tool guardrail including its blocked exit status — against the same payloads and observing identical behavior to before the change. Verified by three byte-comparison tests against a binary built from the pre-change commit (`TestPreToolRenderingMatchesPreChangeBinary`, `TestPreChangeBinaryMatchesSeededPreToolRendering`, `TestPreChangeBinaryMatchesSeededPromptRendering`), plus the pre-tool blocked exit status pinned at 2 and the silent side-effect hooks emitting nothing.
- [x] 9.3 Confirm the guardrail still fires under the package-declared installation route by installing mnemos as a pi package, calling a mnemos write tool with a payload that trips the scanner, and observing a block whose reason names the triggered rules.
- [x] 9.4 Update the README and quickstart to document the pi install path, and state plainly both that pi's effect is not measured by the verify harness and that the capture directive was already present in the measurements mnemos publishes.
- [x] 9.5 Add a note to `ROADMAP.md` covering the remaining pi work that this change deliberately excludes: the compaction-summary channel, the HTTP hook transport, and the verify runner; and note that mnemos' hooks reach no host other than Claude Code and pi.

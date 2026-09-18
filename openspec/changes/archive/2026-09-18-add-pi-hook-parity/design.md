## Context

See `proposal.md` — Why. This section covers only the current state and constraints that shape the approach.

**How mnemos hooks work today.** Each hook subcommand in `cmd/mnemos/hook.go` (`user-prompt`, `post-tool`, `session-end`, `pre-tool`, `pre-compact`, `post-compact`) starts by reading a Claude Code hook payload from stdin via `readHookStdin` (`cmd/mnemos/prewarm.go`), then performs its effect and writes harness-specific output: `emitPromptMemoryBlock` and `emitCaptureDirective` print a text block to stdout that Claude Code concatenates into context; `emitPreToolMemory` prints Claude's `hookSpecificOutput.additionalContext` JSON; `decidePreTool` returns a reason and an exit-2 signal. The decision logic is already separable from the transport in two places — `decidePreTool` is a pure function of the payload, and `detectCaptureShape` is a pure function of the prompt — but the writing is not.

**A resident service already exists but is not assumed.** `internal/api` serves 15 REST endpoints behind a bearer-token middleware, including session open (which already returns a prewarm block), search, context, correct, convention, touch, and session close. `mnemos serve --http` runs that. It has no endpoints for the hook-only decisions — the per-prompt recall plus directive, the pre-tool memory push, and the pre-tool block — so an HTTP transport would require new handler surface. `internal/storage` opens SQLite in WAL mode with `busy_timeout(5000)` but pins `SetMaxOpenConns(1)`, so a second long-lived process would serialize against the MCP server's connection rather than run concurrently.

**What pi provides.** pi is extended through TypeScript modules loaded from `~/.pi/agent/extensions/` or a package's `pi.extensions`. Relevant events: `session_start`, `session_shutdown`, `before_agent_start` (may return `{ systemPrompt, message }`), `tool_call` (may mutate `event.input` and return `{ block, reason }`), `tool_result` (carries the input and the error state), `session_before_compact`, `session_compact`. Two constraints matter:

- `pi.exec(command, args, options)` has no stdin parameter (`dist/core/exec.d.ts` exposes only `signal`, `timeout`, `cwd`), so a payload cannot be piped to a subcommand through it.
- `before_agent_start` returning `systemPrompt` is the only channel that reaches the model every turn without accumulating entries in the session's own history.

## Goals / Non-Goals

**Goals:**

- One hook decision path in Go serving two renderings, so the Claude Code integration and the pi integration cannot drift.
- A pi integration that reaches **Claude Code parity**, not merely Codex parity. The lift-producing channels are push channels; a pi integration that only exposes tools does not qualify. Parity here means the same pushes occur, not that they produce the same measured numbers — see D3.
- An internal seam in the pi extension that lets the hook transport change without touching any event mapping.
- Zero behavioral change for existing Claude Code users.

**Non-Goals:**

- A resident mnemos daemon, HTTP hook endpoints, or any change to `internal/api`.
- Adopting pi's `session_before_compact` to supply the compaction summary. Deferred; see Open Questions.
- A `mnemos verify` runner for pi.
- Fixing the `toolPrefix`/`directTools` gap for hosts other than pi.
- Redefining the existing Claude Code hook behavior in a spec delta. The spec capability `harness-neutral-hooks` describes the new neutral contract and the compatibility guarantee; the Claude Code rendering itself is unchanged and therefore not restated.

## Decisions

### D1. The hook returns a result; the CLI renders it

`emitPromptMemoryBlock`, `emitCaptureDirective`, and `emitPreToolMemory` change from `(w io.Writer, ...)` to returning a result value. A renderer converts that value to the current Claude Code bytes, and a second renderer produces the neutral JSON. The decision functions (`detectCaptureShape`, `decidePreTool`, the scanner call, the injection log write, `RecordSurfaced`) stay exactly where they are.

*Why:* the alternative — having the pi extension parse Claude's output shapes — puts harness knowledge on both sides of the boundary and means the pi path silently breaks whenever Claude's hook payload format shifts. It also makes the eventual HTTP transport re-derive meaning from rendered text.

*Alternatives considered:* (a) emit only neutral JSON and have a thin Claude-side renderer in the installer; rejected because it changes the Claude Code bytes and forfeits the compatibility guarantee. (b) Duplicate the decision logic in TypeScript; rejected because `detectCaptureShape`'s patterns, the scanner threshold, and the suppression window are test-covered Go behavior and a second implementation would drift.

*Note on where the envelope is defined:* `decidePreTool` is already a pure `hookInput → (string, bool)` function, so the pre-tool path is nearly free. The prompt path is the real work, because `runHookUserPrompt` mixes three concerns in one function: goal backfill, memory retrieval plus suppression, and directive detection.

*Rejected sub-option:* emitting both formats from one invocation. It would make the neutral JSON the source of truth for the Claude renderer and double the surface the compatibility guarantee covers. The formats are selected, not emitted together.

### D2. Spawn-per-event now, behind a transport seam

The extension defines a hook-invocation interface — prewarm, prompt block, pre-tool, post-tool touch, session end, plus the existing disable switch and a timeout — with a spawn-based implementation for now. Adding `--payload <json>` to the hook subcommands is what makes spawn usable without `pi.exec` stdin support.

*Why:* it mirrors how mnemos already integrates with Claude Code (short-lived processes), requires no lifecycle management, no port allocation, and no second long-lived SQLite opener. It is also the smaller Go change, which matters because the Go change is the part that can break existing users.

*Alternatives considered:* (a) An HTTP transport against `mnemos serve --http` — zero per-event process cost, and `internal/api` already has most endpoints — but it needs a resident process with start/stop ownership (the extension would have to spawn it, and a hard-killed pi leaks it), port contention handling, and five new endpoints for the hook-only decisions. It also adds a second long-lived SQLite connection to a database currently pinned to one. Deferred as the seam's second implementation, not rejected: the endpoint work is only worthwhile once spawn latency is a demonstrated problem. (b) Reusing `pi-mcp-adapter`'s runtime MCP registration event — rejected because it is proxy-tool-only and, more fundamentally, because anything reached through MCP tools is pull, which is the thing this change exists to avoid.

### D3. Per-turn injection goes through `before_agent_start` returning `systemPrompt`

The prompt-time push (memory block plus capture directive) is delivered by returning a modified `systemPrompt` from `before_agent_start`. The prewarm block is delivered the same way on the session's first turn. The pre-tool hook is still invoked from the `tool_call` event, but only for the guardrail — see the correction below.

*Why:* it is pi's structural equivalent of Claude Code's `UserPromptSubmit` stdout concatenation — it reaches the model for that turn and leaves nothing in session history. Of the two scenarios where mnemos' A/B measurement shows a full on/off gap, `session_start_on_edit` tests precisely whether the model recalls its own memory tools on the first real editing turn, so the push has to be present at that turn rather than earlier in the session.

*What this does and does not buy:* the two full-gap scenarios are memory-**application** scenarios, and they are what this channel is chosen for. The capture scenarios are a different case. `verify/runners/on.sh` already invokes `mnemos hook user-prompt` and passes its output in via `--append-system-prompt`, so mnemos' published capture numbers were measured *with* the capture directive already in context: 8/15 overall, with `architectural_decision` at 0/3 even then. Delivering the directive on pi therefore restores the precondition for that measurement; it does not reproduce a 100% effect. Any claim that pi's capture behavior will match the full-gap scenarios would be unsupported by mnemos' own fixtures.

*Alternatives considered:* (a) `pi.sendMessage` at `session_start` — cheaper and cache-friendlier, but the message sinks into history and is distant by the first editing turn, which is likely to miss the scenario the push exists for. (b) Returning `{ message }` from `before_agent_start` — persisted into session history, so it accumulates and duplicates the memory block every prompt.

*Accepted cost:* the system prompt changes per turn, which defeats prompt caching and adds tokens per turn. This is the same trade Claude Code makes; the alternative is a push that does not work.

**Correction found during implementation: the pre-tool push has no channel in pi.** The design above assumed `tool_call` could return a `systemPrompt` the way `before_agent_start` can. It cannot. pi's documented contract for `tool_call` return values is `{ block, reason, terminate }` and nothing else (`docs/extensions.md`, "Tool Events"), so a handler that returns a system prompt there is silently ignored. The memory the pre-tool hook retrieves is still fetched, and its injection-log entry is still written, but it cannot reach the model from that event: the turn's system prompt is already built and the session's message list for the turn is already fixed.

This narrows what pi reaches relative to Claude Code, and the narrowing is real: on Claude Code, `PreToolUse` output arrives as `hookSpecificOutput.additionalContext` and does reach the model. The pushes pi does deliver are prewarm and the per-prompt context, both through `before_agent_start`. The guardrail — the half of the pre-tool hook that decides something rather than saying something — works unchanged, and it is the only path that deliberately fails a call.

*Options rejected, for the record:* `context` fires before the next provider request and can rewrite that turn's messages non-destructively, which does make it a genuine injection channel — but it is a different event with a different contract (it receives the message list, not the tool call), the retrieval would have to be re-derived from scratch rather than reusing the hook's own decision, and wiring it would be new behaviour arriving after task 7.7's mapping was already verified. That is a new change, not a fix to this one.

### D4. Prewarm is pushed, and the session id is captured for reuse

Prewarm runs at session start, and the `session_id` from the neutral result is retained by the extension so later hook invocations carry it explicitly.

*Why:* the existing `mnemos prewarm` derives the project from the payload's `cwd`. A resumed or forked session must not adopt a stale session, so passing the captured id explicitly makes the scoping deterministic instead of relying on the existing 90-second adoption window to happen to pick the right row.

*Constraint:* the extension must be prepared for `session_start` to fire more than once per process (startup, reload, new, resume, fork) and must re-establish its own state each time, discarding the previous session's.

### D5. The guardrail is decided first, and a block ends the pre-tool work

One `tool_call` invocation runs the pre-tool hook once. Inside it, the guardrail decision is reached first; if it blocks, the invocation reports the block and returns without searching for file-relevant memory. Only when it does not block does the memory push run.

*Why:* this mirrors `runHookPreTool` exactly — it calls `decidePreTool`, writes to stderr and exits 2 when blocked, and reaches `emitPreToolMemory` only otherwise. That ordering is load-bearing rather than incidental: blocking is a safety decision, and the safest reading is that a call refused for its content does not then get its file's memory shoved into the same turn's context. Preserving it also keeps the two harnesses comparable, since `emitPreToolMemory`'s own deduplication state would otherwise be written on a call that never executed.

*Alternatives considered:* running both and merging the results — rejected because it changes Claude Code's observable ordering too if implemented in the shared path, and because it inverts the safety precedence for no benefit.

*Consequence for `isMnemosWriteTool`:* mnemos' write tools are not file-edit tools, so they never reach the memory push. That is true on both harnesses and is not a pi-specific behavior.

### D6. Tool naming: the guardrail matches by suffix, not by an assumed prefix

The pi MCP configuration sets direct tool registration so the tools are callable without a discovery step, and leaves the adapter's naming to the adapter. The pi-side guardrail recognizes mnemos' write tools by matching the tool name's suffix against the mnemos write-tool names (`mnemos_save`, `mnemos_correct`, `mnemos_convention`), whatever prefix the adapter applies.

*Why the suffix match is not a shortcut but the only correct approach in pi:* the adapter derives tool names as `<server prefix>_<tool name>`, and the prefix depends on both the prefix mode and — when mnemos arrives via a package manifest — an additional package-name prefix. Applied to the plain server key `mnemos`, no prefix mode produces the double-underscore form `mcp__mnemos__mnemos_save`; the sanitizer replaces an invalid character with a single underscore, so the double underscore can only arise from a configuration that deliberately encodes it, such as a server key of `mnemos_`. A package-declared server compounds this again. Anything that hard-codes one prefix shape is therefore correct for exactly one configuration and silently wrong — silently dropping write-boundary coverage — for every other, including the package install path this change adds.

*The match has to be widened on the Go side too, and this was found during implementation rather than during design.* The first implementation matched on the pi side and then forwarded the pi tool name to the Go guardrail, which compared against the Claude literal only. The result was a guardrail that returned no block for every pi write — the exact failure this decision exists to prevent, reintroduced one layer down. The fix keeps one decision path: `decidePreTool` gained a prefixed-matching mode that the neutral-format caller enables, while the default path stays strict. Widening the match is confined to the caller that has no dispatch matcher, so the Claude Code path is unchanged and the safe default is unchanged.

*The file-editing tool names differ too.* pi's built-in editing tools are registered as lowercase `edit` and `write`, while Claude Code's matcher uses `Edit` and `Write`. The pre-tool path therefore has the same naming problem for its edit branch as the guardrail has for its write branch. The pi-side match must be case-insensitive over the mnemos write-tool names and must list the file-editing names in both casings, or it will silently stop injecting file-relevant memory on edits.

*Why `isMnemosWriteTool` itself keeps its strict default:* in Claude Code, dispatch is already narrowed by the installed matcher, and the Go function's exact-name comparison serves that path. The neutral-format caller opts into the prefixed match explicitly, so the two concerns stay separated rather than the guardrail being widened for everyone. The strict form remains the default precisely because a guardrail's match is safety-relevant.

*Skill implication:* because the effective name varies by installation route, the shipped skill must describe mnemos' tools in a way that holds under more than one naming scheme rather than asserting a single literal prefix that only one configuration produces.

*The naming inputs and their outputs.* Naming is a pure function of three inputs — the configured server key, the adapter's `toolPrefix` mode, and whether the server arrives through a package manifest (which prepends the package name and `__` to the server key). That makes the whole matrix derivable rather than observable, and a derivation covers configurations a single observation would not:

```
server key   prefix   via package?   resulting name for mnemos_save        suffix match
----------   ------   ------------   ----------------------------------    ------------
mnemos       server   no             mnemos_mnemos_save                    match
mnemos       mcp      no             mcp__mnemos_mnemos_save               match
mnemos       none     no             mnemos_save                           match
mnemos       mcp      yes            mcp__mnemos__mnemos_mnemos_save       match
mnemos_      mcp      no             mcp__mnemos__mnemos_save              match
other        any      any            other_mnemos_save                     no match
```

The chosen direct-route configuration is `directTools: true` with `toolPrefix: "mcp"`, producing `mcp__mnemos_mnemos_save`. The double-underscore form the Go matcher expects is reachable only by deliberately encoding it in the server key, which is why the pi path does not depend on it. The table is the source for the matcher's test cases, so the coverage claim is checked against the same reasoning that produced the design rather than against one machine's configuration.

*Alternative considered:* pin the server key so the assumed literal form is produced everywhere. Rejected — it requires a configuration key chosen to spell an artifact of another tool's naming convention, and it breaks as soon as the package path adds its own prefix.

### D7. Touch recording is scoped to successful file-editing calls

The touch hook runs on `tool_result` for a file-editing tool and records a touch when that call completed without error.

*Why:* `pi.exec` cannot feed a `tool_call`'s input to a subcommand, so `tool_result` — which carries the input, the output, and the error state — is the practical signal. Recording unconditionally would pollute the heat map with failed edits, which a `PostToolUse`-style hook cannot distinguish.

*Fidelity note:* this is a deliberate, bounded difference from Claude Code. Claude Code records on `PostToolUse` for the matched edit tools without inspecting the outcome, and pi's `tool_result` exposes an error flag Claude Code's matcher does not use. "Changed nothing but succeeded" is not separately detectable in either harness: pi's write tool returns no change indicator in its result details, so this design scopes the guarantee to error-vs-success rather than claiming it detects no-op writes.

### D8. All hook results are advisory — the guardrail is the exception

Every pi-side call is best-effort with a timeout. The only intentional failure is a guardrail block, and its reason must identify mnemos and the triggered rules so the model does not misread it as a harness malfunction.

*Why:* mnemos' existing stance is that hooks must never block the user or spam the transcript; the same stance makes a dead mnemos binary a non-event for a pi session. The guardrail block is a deliberate, informative failure in the same spirit as Claude Code's exit-2 path.

## Risks / Trade-offs

- **Spawn latency on Windows dominates the hook budget** → The extension's hook calls carry explicit timeouts and the turn proceeds without pushed content on expiry. If this proves unacceptable in practice, D2's seam allows an HTTP transport without touching event mapping. Not measured on Linux/macOS, where the same call is expected to be far cheaper.
- **Per-turn `systemPrompt` rewrites defeat prompt caching** → Accepted deliberately (D3). Mitigation is keeping the injected block small: the existing per-prompt hit cap and relevance floor already bound it, and the deduplication window prevents the same memory riding along on every prompt.
- **The neutral envelope leaks an internal contract** → The envelope is a CLI-level interface, so it must be treated as compatibility surface. Mitigation: keep it to the four fields the harness actually consumes, and cover the default rendering with the existing hook tests so a drift is caught immediately.
- **`emit*` refactor perturbs a working Claude Code integration** → The compatibility guarantee is a spec requirement with byte-identical scenarios. Mitigation: land the extraction and the renderer with no behavior change, tests green, before adding the payload flag.
- **The pi extension duplicates dispatch state that `pi-mcp-adapter` also owns** → Two independent mnemos consumers in one process (the adapter's server connection and the extension's hook calls). Mitigation: they share no state and no process; the extension never touches the MCP server connection, and the adapter never calls the hook subcommands. The SQLite file is the only shared resource, and it is already WAL-mode with a busy timeout.
- **The guardrail silently loses coverage if it matches on a fixed tool-name prefix** → Identified during review, not theoretical: no prefix mode applied to the plain server key produces the double-underscore form the existing Go matcher expects, and the package-declared installation route prefixes the server name with the package name as well. Mitigation: the pi guardrail matches on the tool-name suffix (D6), and the spec covers the package-declared configuration explicitly. `isMnemosWriteTool` and its Claude Code dispatch are unchanged.
- **A weaker default model may capture less than the published figures** → The capture scenarios were measured at a 53% on-arm ceiling *with* the directive present, so this change is not promising a fix for that ceiling. Mitigation: expectations are stated per capability rather than as a blanket "parity" claim, and only a verify runner could quantify the difference.
- **The pi-side guardrail may run on blocked tool calls, or may not** → pi's documentation states that `tool_result` follows tool execution without saying whether a call blocked in `tool_call` still produces one. This does not change any spec requirement (touch recording is scoped to successful file-editing calls, and guarded write tools are not file-editing tools), but it is the reason D7's wording stops at error-vs-success.
- **A failed embedder probe is paid on every hook invocation** → Each invocation that builds the memory service probes the embedding provider; with the provider disabled or its endpoint unreachable, that probe fails on every call. Mitigation: bound each hook call with a timeout and treat an expired call as empty. Worth revisiting if an HTTP transport is ever implemented, since a resident process would pay the probe once.
- **No measured evidence for pi** → This is the sharpest risk: with the verify runner out of scope, completion is asserted from code and manual observation, not from the paired A/B harness that backs the README's numbers. Any effect claim for pi is unsupported until a runner exists. Recorded as a non-goal rather than hidden.
- **pi's extension API is versioned independently** → The extension depends on event names and return shapes from pi 0.85.1. Mitigation: keep the pi dependency expressed as a peer range in the package manifest and fail soft on an unrecognized event shape rather than throwing.

## Migration Plan

1. Land the Go extraction (`emit*` return results) with the compatibility tests green and no new flags. Existing Claude Code users see no change.
2. Add `--payload` and the neutral format. Still no change for users who do not pass the flags.
3. Land the pi package (manifest, MCP config, skill, extension). Nothing changes for any existing host until a user installs it.
4. Add the pi target to `mnemos init` and the pi checks to `mnemos doctor`.

Rollback: steps 1–2 are revertible by removing the flags while keeping the extraction; step 3 by uninstalling the pi package; step 4 by dropping the target. No storage migration, no schema change, and no change to any existing config file format is involved.

## Open Questions

- **Compaction recovery via pi's summary channel.** pi lets an extension supply the compaction summary (which Claude Code's `PreCompact` cannot do), and mnemos already produces a token-budgeted recovery block. Whether to adopt this, and whether it should replace or augment the model-authored summary, changes no decision in this design — it adds one event mapping later. Deferring.
- **HTTP transport timing.** Whether spawn latency justifies implementing the D2 seam's second transport is a measurement question that cannot be answered on the current machine. Deferring until the pi path runs somewhere representative.
- **Whether a tool call blocked in `tool_call` still produces a `tool_result` in pi.** pi's documentation states that `tool_result` fires after tool execution finishes and does not say whether a blocked call counts. No spec requirement depends on the answer — the guardrail is applied on `tool_call` and touch recording is scoped to successful file-editing calls — so this is deferrable, and it would only refine D7's wording. Worth confirming while implementing, since it determines whether a blocked call is visible to the touch path at all.

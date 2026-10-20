# Installed pi shared-session verification

Date: 2026-10-20 (Asia/Shanghai). Product baseline: `main@d317997`,
distribution version `0.15.0`. This follow-up records real model execution through
the installed pi SDK, extension and MCP adapter. The original
[implementation verification](verification.md) remains an as-of-2026-10-19 record.

## Result and scope

One controlled handoff completed successfully: pi agent A saved partial progress,
then a fresh pi agent B explicitly read that checkpoint, verified the actual
workspace, completed the remaining implementation and passed all four tests.
A remained open during B's work; its checkpoint did not change. B saved its own
completion checkpoint, then both sessions closed through their own lifecycle.

There were **two attempts**, not two successful handoffs. The first stopped on
the test environment before publishing a checkpoint; the second completed.
This is one successful synthetic task, with directed checkpoint capture and no
off arm. It establishes an observed working path, not a success rate, spontaneous
capture rate, time saving, token saving or general efficacy estimate.

The run used real pi agent sessions and model tool calls through the SDK. It did
not exercise an interactive pi TUI, another host product, a non-Git workspace,
distinct owner identities, or more than one concurrent receiver. Those broader
claims are outside this observation.

## Installed components and isolation

- pi `1.3.0`; Node `v26.10.0`; `pi-mcp-adapter` `5.3.2`.
- Installed executable: `D:\software\mnemos\mnemos.exe`, reporting
  `0.15.0+d317997`; SHA-256
  `23FDE47F20D01AA2C642F2D2E29445531C34259F5548F6A1B02384C6CDD5384D`.
- Installed pi package: `~/.pi/agent/local-packages/mnemos`, version `0.15.0`.
  Its four extension TypeScript files matched the repository by SHA-256.
  Execution used that installed copy, rather than the repository extension.
- Configured model identifier: `litellm/deepseek-v4.1-flash`, API
  `openai-responses`, thinking level `off`. This identifies the configured route;
  it does not independently attest the provider's underlying model deployment.
- Two separate Node child processes, each with an independent in-memory pi
  SessionManager and zero initial messages. The actual installed MCP adapter
  connected the installed binary to an isolated SQLite database.
- A disposable Git workspace, home, pi agent state, temporary files and Mnemos
  config/database were isolated under ignored `bin/` fixture directories.
  Continuity was enabled only in the children with `MNEMOS_CONTINUITY=1`.
- The configured real provider credentials and model definitions were read using
  read-only auth storage. Their contents were not copied into fixture files or
  this report. Catalog refresh was disabled; actual model requests were enabled.
- Test-only tool controls restricted writes to the synthetic workspace and reads
  to that workspace plus the installed Mnemos skill. Native pi PowerShell ran
  the allowed Node tests and read-only Git inspection commands.

No product code, installed package, user configuration or global continuity
setting was changed by this verification. The real memory database received none
of the fixture session IDs; the test's own isolated database held the checkpoints.

## Task and observed sequence

The fixture started with two unimplemented functions and four fixed tests:
`subtotalCents(lines)` sums `unitCents * quantity`; `invoiceCents(lines,
shippingCents = 0)` adds shipping once. Each phase has two tests.

A was explicitly instructed to implement only the subtotal, run the phase-one
tests, leave phase two and test files untouched, then save a truthful checkpoint
for task `invoice-relay` with `state=blocked` and `visibility=workspace`.
It was also instructed to stop after a committed receipt without closing its
session. This is an instructed publication test.

B received only this user prompt (90 UTF-8 bytes):

> 从工作区检查点继续任务，核验当前文件和测试，再完成剩余工作。

The harness did not put A's checkpoint body, session ID, file hints or next steps
into B's user prompt. B's first provider request contained the extension's
candidate metadata and B's own current write target, without A's summary body.
Its first tool batch called the advertised prefixed `mnemos_resume_context`
with A as the explicit source. The result selected A's revision-one checkpoint
and reported `session_closed=false`. B then read the actual implementation and
tests, reran phase-one tests, edited only the invoice function, ran phase-two
tests and the complete test suite, and saved completion to B's own session.

| Evidence | Agent A | Agent B |
| --- | --- | --- |
| Current Mnemos session | `01M5BCJ7NP2RJP49WYA3V510JE` | `01M5BCM11N7J216XAFNAHHD1X7` |
| Initial pi messages | 0 | 0 |
| User-level `session.prompt()` calls | 1 | 1 |
| Audited provider-request events | 6 | 12 |
| Committed checkpoint | revision 1, `blocked`, `workspace` | revision 1, `done`, `workspace` |
| Successful test commands | `node --test phase1.test.mjs` | phase one, phase two, then `node --test` |

Two user prompts therefore do not mean two provider requests. The request counts
above cover the successful attempt only and are not performance measurements.

A's checkpoint was committed at `00:50:20.5976352 UTC`. Before/after-B database
snapshots showed A still open and its complete checkpoint row equal, including
body, revision, visibility, timestamp and retry metadata. B closed at
`00:51:36.383 UTC`; A closed afterward at `00:51:37.902 UTC`. Final inspection
found exactly two ended sessions and two revision-one checkpoints, zero ordinary
observations, and SQLite `quick_check=ok`. No claim or consumption state was added.

The independent post-run test command passed **4/4**, with no failures or skips.
Both test files still matched their hashes recorded before model execution.
A's subtotal implementation remained intact when B completed the invoice.

## Failed attempt and recovered errors

The first fixture, `bin/pi-real-relay-dDG3L1`, reached six real provider-request
events and A edited the phase-one code. The harness offered a Bash tool in an
environment without a Bash shell; attempted execution reported
`No bash shell found`. No test completed, no checkpoint committed, and B never
started. A was closed during cleanup. The final assistant event had error status,
but that harness revision did not record the error message, so a more specific
terminal cause is not asserted.

Only the harness was corrected: it used pi's native PowerShell tool, allowed
reading the installed Mnemos skill, and captured subsequent assistant errors.
A model-free PowerShell smoke passed before the second attempt. A/B prompts were
byte-identical across attempts. Successful-attempt prompt SHA-256 values:

- A: `4A01D92A1AC1C7CBC96FE113ABCD2B79EA08C7574BA4EBD0C3CF6395789EA64B`
- B: `D4A6439791DE8A058600F5EBB8B88668FDFA3552979A7171EDEEA1F7FB43D3BA`

In the successful attempt, some compound inspection commands were rejected by
the test-only shell whitelist. The agents split commands and subsequently ran
the allowed tests successfully. These controls affect the observed interaction
and prevent treating it as unconstrained interactive usage.

B's first checkpoint save also returned a real product validation error:
`workspace metadata is server sampled`. It had copied `payload.workspace` from
the read response into the write request. The rejected call committed nothing.
B removed that field, used a new request ID, and received `applied`, revision 1,
for its own session. This was a recovered error, not an error-free run.

The current domain `Payload` is shared by checkpoint input and output and includes
the server-sampled `Workspace` field (`internal/continuity/types.go`). A follow-up
could separate writable input from read output, or exclude server-owned fields
from the MCP input schema while retaining output and boundary validation. This
observation justifies reducing avoidable model retries; no such product change
was made as part of this verification.

## Evidence and independent review

Local raw evidence is intentionally ignored rather than committed. The successful
fixture is `bin/pi-real-relay-nVdLgX`; it contains `result.json`, A/B `events.jsonl`,
first-provider system-text audits, prompts, `before-B.json`, `after-B.json`,
`golden-test-hashes.json` and `independent-tests.txt`. The disposable runner is
`bin/pi-real-continuity-relay.mjs`. These local artifacts are not release assets
and are not guaranteed to remain available in a fresh checkout.

An independent agent read the successful-attempt traces, source snapshots,
receipts, test output and golden hashes. Review approved the controlled SDK
handoff result, including correct source/current identities and unchanged A
checkpoint, while requiring the first failed attempt and B's rejected save to be
reported. No model calls were made by that reviewer. Documentation review and
`git diff --check` are separate from the earlier implementation test gates;
this documentation follow-up does not claim a new Go suite or race/lint run.

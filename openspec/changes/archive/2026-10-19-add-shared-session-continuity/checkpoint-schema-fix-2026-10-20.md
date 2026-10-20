# Checkpoint MCP write-schema correction

Date: 2026-10-20. This corrects the avoidable save retry observed in the
[installed pi handoff](pi-verification-2026-10-20.md). It is a fix to the existing
server-owned metadata boundary, without a database migration or new capability.

## Change

`mnemos_checkpoint` previously inferred its MCP input schema from the domain
`CheckpointInput`, whose shared `Payload` also describes read results. That
advertised `payload.workspace` even though clients cannot author branch/HEAD.

The MCP adapter now has a separate writable DTO and maps its fields into the
unchanged service input. The six writable payload fields are `task`, `state`,
`summary`, `next_steps`, `blockers` and `references`. Server-sampled workspace
metadata remains in read output. Strict raw-request decoding, typed error
envelopes, CAS, exact request retry and nil-payload clear remain enforced.
Domain, SQLite, CLI, HTTP and public Go-client implementations are unchanged.

No pi extension source change is needed: clients discover the corrected schema
from the binary. An already connected adapter needs to reconnect/reload to refresh
its tool definitions. The distribution version remains `0.15.0`; the executable
used below is an explicitly identified local development build.

## Regression and reproducible pi gate

The new actual Go SDK `ListTools` test first failed against the old implementation
with `checkpoint input schema exposes server-sampled payload.workspace`. It then
passed with the fix. Further actual SDK calls verify full writable-field mapping,
read-output metadata, forged/empty metadata and unknown-field rejection, required
revision/type checks, CAS, exact retry without resampling and clear. Raw requests
with an earlier duplicate `payload` containing `workspace` cannot bypass rejection
or echo submitted text. This does not claim all duplicate known JSON keys are
newly prohibited.

The first complete Go run found an existing cross-transport success fixture that
serialized a zero-valued read-only workspace struct. The fixture now constructs
the legitimate writable wire object explicitly, including for CAS/owner negative
cases. The common invocation helper still forwards supplied fields unchanged;
invalid metadata is not filtered out to make tests pass.

The tracked [pi gate](../../../../verify/pi/README.md) uses a real locally installed
pi SDK and adapter, empty in-memory credentials, no model prompt, blocked fetches
and isolated home/config/database. It copies only the specified Mnemos package's
runtime files and changes the temporary copy's MCP command to the candidate binary.
Hooks use the same binary; original installation and account files are untouched.

- Old installed binary: the gate failed on the actually discovered schema, before
  any checkpoint save (`bin/mnemos-pi-schema-H0UBCR`).
- Candidate: `bin/mnemos-checkpoint-schema.exe`,
  `0.15.0+559e82e.checkpoint-fix`, SHA-256
  `BA5C0540C0107C6C3B9AF9A70BE235BDB0BAC49BDBDBC790965997C84438B4AE`.
- Final gate passed **12 actual adapter tool executions, zero model requests**
  (`bin/mnemos-pi-schema-AJ8xG7`). It covers independent opens, save/read workspace
  metadata, forged input rejection with unchanged source/revision/time, exact
  replay, normal update, clear and clear replay. The source package's four
  extension TypeScript hashes matched the temporary copy. The resolved MCP
  command and candidate hash/version are retained in `provenance.json`.
- During gate development the error parser was adjusted for the adapter's explicit
  `Error:` / `structuredContent:` wrappers, and the clear expectation was corrected
  to the existing `cleared` result rather than `unavailable`. These were harness
  corrections; the successful final run, not those intermediate runs, is the gate.

## Real-model pi follow-up

One further real-model A/B SDK handoff used the same prompts and task as the prior
successful observation, with an isolated copy of the installed `0.15.0` pi package
pointing both hooks and MCP at the candidate above. SDK pi `1.3.0`, adapter `5.3.2`,
Node `v26.10.0`, configured route `litellm/deepseek-v4.1-flash`, thinking `off`.
The provider's underlying deployment is not independently attested.

| Evidence | A | B |
| --- | --- | --- |
| Current session | `01M5BE23TJ4TANPH5ZYN3QVDVT` | `01M5BE374G3RPEX02BSV27YM0P` |
| Initial messages | 0 | 0 |
| User prompts | 1 | 1 |
| Provider-request events | 6 | 9 |
| Checkpoint calls / applied receipts | 1 / 1 | 1 / 1 |
| Final checkpoint | revision 1, blocked | revision 1, done |

Both first saves succeeded without a workspace validation retry. B's first model
request offered A's metadata plus B's own write target, without A's checkpoint
body; B explicitly called resume for A, verified the files/tests, then completed
the invoice function. A stayed open, and its entire checkpoint row was equal
before and after B. Both sessions subsequently closed, B before A. Independent
tests passed **4/4**, both golden test hashes were unchanged, and SQLite
`quick_check=ok`. Evidence is retained locally in ignored
`bin/pi-real-relay-Ib3Qi7`.

This is one controlled successful handoff with instructed publication, not an
interactive TUI observation, cross-owner experiment, off-arm comparison or
performance estimate. Test-only shell restrictions rejected some compound
inspection commands; no error-free overall model interaction is claimed.

Independent review approved the production DTO, regression tests, adjusted
cross-transport fixture, reproducible gate and the real-model trace/database
evidence. Local raw logs/databases, provider/account data, temporary package copies
and development binaries are not committed or published as release assets.

## Local check results

- CGO-free candidate build passed; the new two-test SDK regression passed after
  first reproducing the schema failure on the old implementation.
- `go test -p 1 ./... -count=1`: every package except the command package passed
  on the first complete run. After the success-fixture correction described above,
  `go test -p 1 ./cmd/mnemos -count=1` passed (145.344s). Compatibility cases used
  the real installed pre-fix binary through `MNEMOS_PRE_CHANGE_BIN`.
- `go vet -p 1 ./...` passed.
- All pi extension tests passed against the candidate: **57/57**, zero skipped,
  using `MNEMOS_BIN` and `node --test --test-concurrency=1`.
- Changed Go files are gofmt-clean, the pi gate passes `node --check`, and
  `git diff --check` passes.
- The code graph was refreshed. Its optional SQL parser is unavailable, so SQL
  files remain outside that auxiliary graph; no dependency was installed for it.

Windows local results do not claim the race detector or golangci-lint passed;
those remain CI checks. The test fixture and manually invoked pi gate never
invoke the memory layer's own LLM inference, add a storage table or alter a schema.

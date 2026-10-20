# Runtime hardening verification

Date: 2026-10-20. Baseline: `ba9cebb` / local `0.15.0+ba9cebb`.
This is local development acceptance, not a published release or model efficacy
measurement. Synthetic fixtures do not use the user's memory database.

## Reproduced failures

- Eight independent database handles planned migration 9 before writers started:
  the old implementation failed seven with duplicate DDL. The writer-transaction
  recheck regression fails against that implementation.
- Metadata/no-op updates previously changed nine SQLite rows including FTS work
  where only one base-row update was needed. The new audit requires one and also
  checks inserted/changed/deleted text using real FTS `MATCH` queries.
- A limit-one continuity request materialized 40 full sources before truncation.
  The bounded-store audit fails against that implementation.
- Repeated concurrent production `storage.Open` exposed fresh WAL initialization
  BUSY errors at Ping, before migration DDL. Sequentially opening test connections
  would hide this; the final gate retains simultaneous production opens.
- Explicit TOML alpha zero resolved to 0.5; incompatible vectors affected scores
  or mode. Configuration and actual SQLite-backed retrieval regressions cover
  these cases.

## Review

Implementation was divided into storage/continuity, search/maintenance, and
installer lanes. A different implementer reviews each lane; the manager reviews
integration and acceptance. Final verdicts are recorded after those checks.

| Lane | Independent reviewer | Verdict |
| --- | --- | --- |
| Search/configuration/maintenance | installer implementer | Approve |
| Installer/init | search/maintenance implementer | Approve, including exact-self global alias conflict |
| Storage/continuity | installer implementer | Approve, including ledger bootstrap and typed BUSY initialization retry |

Findings resolved during review include query vectors returned alongside errors,
the explicit alpha-zero endpoint contract, truthful cancellation counts, and
recognizing the exact current executable after a local rename so repeated init
does not add a global route. Existing old-hook order was preserved for doctor
compatibility. Backfill also preserves the first failing ID, embed/update stage
and wrapped cause instead of discarding all diagnostics; this is bounded transient
state and passed independent incremental review.

## Capacity measurement

The same isolated benchmark uses 128 and 2,048 stored sources, either small
checkpoints or approximately 12 KiB bodies, and requests a window of five. A
store wrapper counts materialized source bodies. Fixture creation is outside
the timed/allocation region. Single-iteration latency is noisy and is not used
to claim a speedup.

| History | Body | Baseline B/op | Baseline allocs/op | Baseline bodies/op | New B/op | New allocs/op | New bodies/op |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 128 | small | 264880 | 3035 | 128 | 93000 | 1645 | 5 |
| 128 | large | 5694080 | 6052 | 128 | 957224 | 1545 | 5 |
| 2048 | small | 4658576 | 43153 | 2048 | 513640 | 16779 | 5 |
| 2048 | large | 81682976 | 102982 | 2048 | 1722496 | 23466 | 5 |

Command: `go test -p 1 ./internal/continuity -run '^$' -bench
BenchmarkContinuityCandidateWindow -benchtime=1x -benchmem -count=1`.
The store wrapper counts returned full source objects, not intercepted SQL calls;
the bounded loader source and undisplayed-corrupt-body regression independently
verify that it does not decode the discarded bodies. All matching histories
remain counted (128 or 2048 respectively).
Local raw stdout is retained in `bin/runtime-capacity-before.log`,
`bin/runtime-capacity-after.log`, and `bin/runtime-storage-context-green.log`.

Completed command results are listed below. Metadata
scan CPU and cumulative metadata allocations remain linear in history; only
retained candidate bodies are bounded by the requested window. There is no new
retention policy, checkpoint history, ownership claim, or automatic deletion.

## Acceptance scope

Candidate: `mnemos 0.15.1+ba9cebb.runtime`, built from the reviewed working tree
before its final commit, with `CGO_ENABLED=0`, `-trimpath`, `-buildvcs=false` and
`-s -w`. SHA-256:
`990882cb4909f802739af5a349a4fd0e3bcb12391a377eb12ca850edbec56c54`.

| Check | Result and retained local evidence |
| --- | --- |
| `go test -p 1 ./... -count=1` | PASS, 20 packages with tests; `bin/runtime-hardening-go.log` |
| `go vet -p 1 ./...` | PASS, exit 0 |
| Changed-file `gofmt -l` and `git diff --check` | PASS |
| Real old/candidate executable compatibility | PASS, all four selected tests ran without skip; `bin/runtime-hardening-compat.log` |
| `MNEMOS_BIN=<candidate> node --test --test-concurrency=1` in `pi/` | PASS, 57 tests, zero skipped; `bin/runtime-hardening-pi.log` |
| Independent process startup | PASS, three fresh and three schema-8 upgrade rounds, eight processes each; all 48 exit 0, all six ledgers 1–10 and quick checks `ok`; `bin/runtime-hardening-o5jodnsn/result.json` |
| Explicit configured alpha zero | PASS, candidate resolves zero; same process-probe record |
| Actual pi SDK 1.3.0 / adapter 5.3.2 | PASS, 12 direct tool executions, zero model requests/auth use; `bin/mnemos-pi-schema-yEAZD4/{provenance,transcript}.json` |
| Local copied package 0.15.1 update | PASS, `pi update --extensions` exit 0 with offline bypass disabled; settings and package hashes unchanged; `bin/runtime-hardening-local-update.json` |

The SDK gate covers independent sessions, checkpoint save, explicit resume,
replay/update/clear, input schema and forged workspace rejection. Its isolated
MCP configuration and hook invocations point to the same candidate. The test
package is a copy, not a production registration linked to the checkout.

- Targeted installer/Init/Doctor regressions passed; the complete installer package
  also passed. Actual repeated init fixtures preserve the local-package route.
- Targeted configuration, memory, storage keyset and command maintenance tests
  passed. The documented rebuild option reached an isolated HTTP fake provider.
- `python verify/runners/test_snapshot.py`: 3 tests passed.
- Five JSON manifests parse, mirrored plugin files are byte-identical, and all
  eight distribution versions agree at `0.15.1`.
- `graphify update . --no-cluster` refreshed the local code graph (4,166 nodes,
  18,707 edges). SQL AST extraction is unavailable without the optional parser;
  this is not a complete semantic documentation graph rebuild.

The change passes strict OpenSpec validation. The pre-sync main-spec baseline
passes ordinary validation (10/10) with two pre-existing long-requirement warnings
in `memory-lifecycle-retrieval` and `pi-integration`; their content is not rewritten
as part of this change.
All five deltas were then synchronized: 11 added requirements across three new
and two existing capabilities. Existing main-spec bytes remain prefixes of their
updated files, and new purposes were copied verbatim. Post-sync ordinary
validation passes 13/13 specs with exactly those same two baseline warnings.

## Local installation follow-up

After committing, build the same source with `0.15.1+<commit>` and atomically
replace `D:/software/mnemos/mnemos.exe`, retaining the prior executable outside
the installation directory. Refresh only the independent user-directory pi
package's runtime files, skill and manifest. Preserve the existing absolute MCP
command, pi registration and Codex configuration.

A consistent pre-upgrade backup and all business-table primary-key sets were
saved under `bin/runtime-hardening-install/`. Post-commit installation checks
must verify original keys remain, ledger 10, quick-check integrity, source/copy
hashes, installed SDK tool execution and the installed local-package update.
Their machine-specific record belongs in that ignored local evidence directory;
published releases continue to use the existing tagged CI workflow.

Windows has no configured GCC or golangci-lint. Local checks cannot replace CI's
race detector or golangci-lint run. No remote CI success is implied by local
acceptance. The pi SDK gate uses real local tools without a model prompt or
provider request; it does not establish TUI behavior or model capture quality.

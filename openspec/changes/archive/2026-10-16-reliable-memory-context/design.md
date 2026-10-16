# Design

## Context

See [proposal.md](proposal.md). Inspection on 2026-10-16 found:

- `internal/prewarm/prewarm.go:Build` selects strings per section, then truncates the last section while appending all its references. `Section.Body` also retains the untruncated body. Existing injection tests check recording but do not cover partial selection.
- `internal/memory/service.go:Context` already selects whole observations, but has a separate formatter/budget loop. `RecordSurfaced` and explicit storage `Get` both affect access counts.
- `internal/verify/retrieval.go` deliberately uses best rank across queries. `calibrate.go` labels the target positive and other hits negative; its access-versus-surfacing comparison is a usage proxy, not outcome attribution.
- Existing specs own scores, pinned ordering/overflow, trust boundaries, and graph expansion. This change owns delivery and evaluation, leaving those requirements intact.

## Goals / Non-Goals

**Goals:** One consistent account of selected text, references, and measured delivery; backwards-compatible fixtures; no new durable state.

**Non-Goals:** MMR, automatic summarisation, new tokenizers/dependencies, prompt capture, causal-success attribution, changes to hook transport formats, or retrospective repair of old injection records.

## Decisions

### 1. Merge delivery with its evaluation; keep reads and evolution independent

The earlier grouping put evaluation beside read APIs. That hides the immediate dependency: selection improvements need trustworthy labels and exact surfacing records. Package those together. Batch reading and version/evidence persistence have distinct contracts and migrations, so bundling all three would make a large release unnecessarily dependent on schema work. The three changes share no mandatory new public type.

External design references are [Claude-Mem](https://github.com/thedotmack/claude-mem), [OpenViking](https://github.com/volcengine/OpenViking), and [Graphiti's search recipes](https://github.com/getzep/graphiti/blob/main/graphiti_core/search/search_config_recipes.py), inspected in the preceding research. They support layered loading and optional diversity selection, not a claim that their benchmarks transfer to this project. MMR remains an experiment until labelled cases show a problem.

### 2. Select render items, never truncate an assembled section

Represent a draft as ordered items carrying text, optional observation/skill reference, and required annotations. Session summaries and hot-file entries are items without memory references. Keep the current pipeline order and section names. Deduplicate by `(kind, ID)` at the first selected occurrence; do not deduplicate by source, topic, or semantic similarity.

```text
domain candidates --> render items --> safety scan --> budget selection
                                                           |
                                      +--------------------+------------------+
                                      v                                       v
                                 final text                           selected refs
                                                                              |
                                                                              v
                                                                    existing injections
```

Try each item's complete normal representation, then a deterministic compact representation containing its ID/type, complete title if it fits, and required warnings. Do not truncate body assertions into a different meaning. If even a minimal identification plus warnings does not fit, omit it. Keep safety scanning over complete candidate material and scan rendered sections as well so splitting cannot bypass cross-item detection. A high-risk section must retain its wrapper around selected items; reserve that cost before selecting them.

Count headers, separators, overflow notices, and warnings with the existing byte-based estimate, applied consistently to final UTF-8 text. This remains an estimate, not an exact model token guarantee. Work at rune boundaries for any display shortening. Returned section bodies must match final emitted content. Existing pinned budget/order rules take precedence, with space reserved for an overflow notice. For budgets too small even for the notice, emit a complete fitting notice or an empty block, never malformed fragments.

No global context renderer is needed. Use a small shared formatting/budget utility for prewarm and `memory.Context`, injected/consumed at service boundaries without importing prewarm into memory or pulling protocol logic downwards. Existing hook serializers stay unchanged.

### 3. Keep a clear event meaning

Only references present in selected items enter `Block.Surfaced` and the existing injection logger. Preserve best-effort logging and present channel behaviour. An event means the service emitted context, not that a host displayed or the model attended to it. Do not backfill old events from today's corpus: the historical render is unknown.

Do not add an access-to-success inference. Existing access counters are retained for compatibility; calibration reports their usage basis explicitly and never treats it as a verified outcome.

### 4. Add explicit fixture cases instead of redefining legacy probes

Keep existing `probes` and their any-query verdict. Add a separately named `cases` collection with per-case `queries`, `relevant_ids`, `forbidden_ids`, `expect_in_top` (default 5), `query_mode` (`any` or `all`, default `all`), scope, and an optional `expect_no_injection`. Per-query retrieval passes when all labelled positives occur within the cutoff and no forbidden ID occurs there; a negative-only case needs no positives. A no-injection expectation additionally requires an empty final selection. Reject cases without any labels or delivery expectation, overlapping labels, and invalid aggregation. Unlabelled hits are visible in reports but excluded from calibration labels.

Report per-query recall over labelled relevant IDs, forbidden-hit counts, selected references, and aggregate verdict. Keep legacy proxy calibration as an identified compatibility mode; never silently mix its labels with explicit labels into one supposedly clean result. Empty/single-class coverage must be visible. Evaluate no-injection cases against the actual consumer gate and selection path, with a fixed clock and isolated seeded store; a non-empty candidate set can still correctly produce no delivery. Expose gate/selection through narrow consumer-owned interfaces rather than transcribing production logic into tests. Do not change existing gate constants merely because the report format changes.

### 5. Persistence assessment

| Data | Destination | Why retain it / lifetime |
| --- | --- | --- |
| Authored observations and skills | Existing SQLite stores | Existing durable knowledge; lifecycle unchanged |
| Actual selected memory reference, channel, scope, time | Existing `injections` rows | Existing cross-session suppression/digest/promotion evidence; retain under existing policy |
| Candidate IDs, ranking factors, render items, dedup set | Request memory only | Reconstructable from the query; discard after request |
| Token estimate, selected snippets, final block | Response only | Delivery payload, not a second memory copy; discard server-side after response |
| Relevance/forbidden labels | Versioned YAML fixture | Test truth belongs with tests, never FTS/search memory |
| Calibration and evaluation report | CLI output or explicit output file | Reviewable run artifact; no automatic DB write |
| MMR scores, generated summaries, full prompt trace | Not introduced | No demonstrated need; cost and poisoning/privacy surface exceed current value |

Do not add an event-retention timer in this change: silently pruning existing events could change recurrence and suppression signals. Missing historical evidence must remain unknown. A separate retention policy can later define an explicit trade-off; it is not required to avoid adding new tables here.

## Risks / Trade-offs

- Compact items convey less detail -> expose a stable ID and keep complete warnings so the existing get tool can retrieve the source.
- Byte-based estimates differ by model/language -> label estimates honestly and test mixed-language framing, not an invented exact-token promise.
- Refactoring formatting can change generated context -> preserve unchanged complete-item rendering where possible, test intended differences, and keep hook envelopes byte-compatible.
- Fixed ordering can still starve later sections -> report selected coverage in fixtures; reserve MMR or allocation changes for evidence-backed follow-up.

## Migration Plan

No database migration or corpus rewrite. Add regression cases first, introduce item-level rendering and logging, then add explicit fixture cases and reports. Run affected Go tests and vet; full tests and race checks follow repository/CI conventions. Rolling back code leaves existing data readable but restores the old inaccurate logging behaviour; do not claim that historical events have been repaired.

## Implementation verification (2026-10-16)

Independent review approved the final diff after fixing a high-risk instruction formed by control-character sanitisation and a mismatched prewarm safety report; both have regression tests. Whole session and hot-file entries, including newline-containing paths, were re-reviewed and tested after the final delta. The same partial-section regression fails against baseline commit `14d9aff` and passes on the implementation.

Manager verification passed: `go test -p 2 ./... -count=1` with `MNEMOS_PRE_CHANGE_BIN` pointing to a real binary built from `14d9aff`; `go vet -p 2 ./...`; and `CGO_ENABLED=0 go build -p 2 ./...`. Final affected prewarm tests, graphify update, and `openspec validate reliable-memory-context --strict` also passed. The local Windows toolchain has no race detector support; no local race run or golangci-lint result is claimed.

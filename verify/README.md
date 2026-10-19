# mnemos verify — efficacy harness

Two tests, two numbers, both repeatable.

## Retrieval (cheap, fast)

```
mnemos verify retrieval
```

Reads `verify/retrieval.yaml`. For each probe, runs the listed queries
through the live store and checks whether the named memory appears in
the top-K hits. Legacy probes pass when at least one query finds the target in the top-K; this is an any-query hit rate, not classical precision@K. Failures point at ranking, tags, or
content — the memory exists but the trigger context can't reach it.

Add a fixture entry every time you save a high-importance memory:

```yaml
- id: 01ABCDEF...
  title: short human label
  queries:
    - "the situation phrasing that should surface this memory"
    - "another way the agent might describe the same trigger"
  expect_in_top: 5
```

## Behavior (expensive, slow, real tokens)

```
mnemos verify behavior
```

Reads `verify/behavior.yaml`. For each scenario, runs the trigger prompt
N times under each arm (mnemos on, mnemos off) and counts how often the
agent's transcript matches the assertion. Lift = pass_rate(on) -
pass_rate(off). A correction memory that produces zero lift is reaching
the agent but not changing its conduct.

The arms are command templates. The default uses
`claude -p ... --mcp-config <on|off>.json --strict-mcp-config` so the
two arms see different MCP configurations; customize to match your CLI.
`{{trigger}}` is substituted per run.

The on-arm never writes to your real store. `runners/on.sh` snapshots it
into a scratch directory and exports `MNEMOS_CONFIG` pointing there, so
the hooks, the MCP server claude spawns, and anything the agent saves all
hit the snapshot, which is deleted when the run ends. Set `MNEMOS_BIN` to
run the arm against a development build.
The on-arm requires Python 3 with its stdlib `sqlite3` module (no SQLite CLI).
Override the interpreter with `MNEMOS_VERIFY_PYTHON` when it is not `python3`.
Snapshot setup fails closed; it never falls back to writing the live store.
The generated MCP config binds the exact `MNEMOS_BIN`. Registered global
mnemos hook binaries are checked too: if an older installed binary ignores
`MNEMOS_CONFIG`, the runner stops before launching Claude. Upgrade that binary
first; an unparseable mnemos hook wrapper also requires review before running.
The no-model isolation regression is `python3 verify/runners/test_snapshot.py`.

## Both

```
mnemos verify all
```

Run together pre-release. Retrieval can run on every commit; behavior is
slow enough that nightly or per-release is the right cadence.


## Explicit retrieval cases and delivery checks

`probes` retain their legacy any-query verdict. Add `cases` for explicit labels;
these default to `query_mode: all` and `expect_in_top: 5`:

```yaml
cases:
  - name: rule-and-exception
    project: fixture
    queries: [transaction]
    relevant_ids: [rule, exception]
    forbidden_ids: [raw, stale]
    query_mode: all
    expect_in_top: 10
  - name: no-useful-context
    project: other-project
    queries: [unrelated topic]
    expect_no_injection: true
```

All relevant IDs must occur within the cutoff and no forbidden ID may occur there.
`query_mode: any` accepts any passing query; `all` requires every variant to pass.
Negative-only cases are valid. Empty label sets require a delivery expectation.
An ID cannot be both relevant and forbidden. Hits in neither set are reported as
unlabelled and never become calibration negatives. Recall is reported per query;
it is only meaningful when the case declares relevant IDs. Duplicate retrieved
IDs count once. Reports retain all failing query variants and forbidden IDs.

`expect_no_injection: true` additionally tests that the final selected references
are empty; `false` requires at least one. The CLI uses the production prompt-hook
search, project affinity, relevance floor, cap, and recent-surfacing suppression,
with writes disabled. Weak non-empty candidate sets may therefore pass an empty
selection case. The callback uses a single time per run. Existing injection history
still affects suppression; use an isolated seeded store and a fixed injected clock
for reproducible regressions. Raw/stale/agent-filtered retrieval cases are supported,
but delivery expectations using these scopes are rejected because the prompt
consumer does not expose those policies. Retrieval-only queries explicitly report
`delivery_measured=false`; no delivery claim is inferred from their search hits.

`verify/context-retrieval.yaml` is an example seeded-corpus fixture, exercised by
`TestVersionedLabelledFixtureOnIsolatedStore`; its IDs are not your live database IDs.
It covers a rule and exception, raw/stale visibility, and Chinese Unicode content.
The production consumer regression separately covers weak candidates, unexpected
selected references, fixed-clock repeatability, and unchanged observation/access/
injection state.

`mnemos verify calibrate [fixture]` reports explicit positive/negative/unlabelled
sample counts per query, along with whether both labelled classes were observed.
Empty and single-class coverage does not establish separation. Legacy probes are
calibrated separately and identified as single-target proxy labels. Access versus
surfacing is a usage proxy, never a verified-success or causal-benefit measure.
Explicit labelled calibration does not use that proxy weighting.

Reports go to standard output. Save them only to a destination you explicitly
choose, for example in PowerShell:

```powershell
mnemos verify retrieval ./my-fixture.yaml > ./retrieval-report.txt
mnemos verify calibrate ./my-fixture.yaml > ./calibration-report.txt
```

These commands do not save probes, labels, reports or generated contexts as
memories, and delivery evaluation does not record injection/access events. Labels
stay in versioned YAML; intermediate candidates and reports remain transient.

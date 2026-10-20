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

The arms are command templates that invoke the Python standard-library runner;
`{{trigger}}` is substituted per run. Before the first run, copy
`verify/runners/runtime.example.json` to the ignored local file
`verify/runners/runtime.local.json` and set absolute paths for the candidate
Mnemos binary, the TOML config whose store should be snapshotted, and the Claude
Code executable. The interpreter is the explicit `python3` command in the YAML;
edit that command if this machine uses another Python executable. No product
environment variable selects the binary, config, host or interpreter.

```powershell
Copy-Item verify/runners/runtime.example.json verify/runners/runtime.local.json
# Edit runtime.local.json with the three absolute paths before running the arms.
```

The runner checks Claude Code's version and required isolation flags before it
starts a model. It requires version 2.1.286 or newer and help entries for
`--print`, `--bare`, `--settings`, `--mcp-config`, `--strict-mcp-config`,
`--append-system-prompt`, `--output-format`, and `--verbose`. Each launch uses bare
mode with a scratch settings file that disables all hooks. The off arm gets an
empty MCP config and starts no Mnemos process. The on arm reads the selected
source config, snapshots its SQLite store with Python's backup API, then runs
prewarm and the user-prompt hook manually with the candidate binary and the same
scratch config selector. Claude receives that context through
`--append-system-prompt`; its only MCP server is the candidate with that scratch
config. A setup or isolation failure stops before the model starts.

The selected database is read only for SQLite's consistent backup into the
disposable fixture; Mnemos commands and MCP writes target only that scratch copy.
The user's Claude settings and credential files are not copied. Host provider
credentials remain available to Claude through its normal process environment;
Mnemos routing variables and `CLAUDE_CONFIG_DIR` are removed, and the runner
gives the host a scratch home. The no-model isolation regressions are
`python3 verify/runners/test_snapshot.py`. These runners change the measurement
protocol: existing published behavior/capture numbers are historical and have
not been recalculated, so results from this isolation protocol are not directly
comparable to them.

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

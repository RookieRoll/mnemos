# Architecture

Mnemos is a layered Go service with strict boundaries. This document walks the layers from the bottom up.

## Principles

- **Interfaces declared where they're consumed.** `memory.Reader/Writer/Maintenance/Exportable/Vectorable` live in the memory package because services depend on them; the `storage` package implicitly satisfies them. This is the stdlib `io.Reader` idiom — segregated, composable, easy to mock.
- **Transports are thin adapters.** `internal/mcp` wraps the official Model Context Protocol Go SDK; `internal/api` is a stdlib `net/http` adapter. Neither contains business logic.
- **No global state. No `init()` side effects. No reflection wiring.** All dependencies are constructor-injected.
- **Errors wrapped at every boundary** with `fmt.Errorf("context: %w", err)`. Nothing escapes naked.
- **Context propagation.** Every public method takes `context.Context` first.
- **Tests live next to code.** Table-driven where patterns repeat; end-to-end at each layer's public API.
- **Don't reinvent the wheel.** We use the official MCP SDK (protocol compliance + schema inference), `gopkg.in/yaml.v3` (robust YAML for vault frontmatter), `golang.org/x/sync/errgroup` (clean daemon lifecycle), `BurntSushi/toml` (config), `modernc.org/sqlite` (pure-Go driver), `oklog/ulid/v2` (sortable IDs). Everything else is stdlib.

## Layer map

```
┌──────────────────────────────────────────────────────────┐
│  cmd/mnemos                (CLI: serve, init, doctor, …) │
├──────────────────────────────────────────────────────────┤
│  internal/mcp              (JSON-RPC stdio server)       │
│  internal/api              (HTTP — planned)              │
├──────────────────────────────────────────────────────────┤
│  internal/prewarm          (push-based context assembly) │
│  internal/safety           (promptware scanner)          │
│  internal/dream            (consolidation + promotion)   │
├──────────────────────────────────────────────────────────┤
│  internal/memory           (Observation service)         │
│  internal/session          (Session service)             │
│  internal/skills           (Skill service)               │
├──────────────────────────────────────────────────────────┤
│  internal/storage          (SQLite + FTS5, migrations)   │
└──────────────────────────────────────────────────────────┘
```

## Storage

`modernc.org/sqlite` (pure Go, no CGO). One file at `~/.mnemos/mnemos.db`. WAL mode, foreign keys on, 5s busy timeout. Schema versioned via `schema_migrations` + numbered SQL files embedded via `//go:embed`.

Key design calls:

- **Bi-temporal**. Observations carry `valid_from`/`valid_until` (fact time) and `created_at`/`invalidated_at` (system time). Supersession invalidates rather than deletes. Historical queries work via `SearchInput.AsOf`.
- **FTS5 external-content virtual table** mirrors observations for BM25 search. Triggers keep it in sync on insert/update/delete.
- **Content hash on insert** powers dedup-on-save: identical content in the same `(agent, project)` bumps access count instead of duplicating.
- **Embeddings as BLOB**. `sqlite-vec` is a C extension that modernc.org/sqlite can't load; cosine similarity runs in pure Go over every embedded row in scope (recall benchmark: ~9 ms for 10k rows × dim 64 — no index needed). Zero-CGO, zero dependency cost.

## Memory service

**Score contract** (specs: `memory-retrieval`, `memory-lifecycle-retrieval`). Every search result's `Score` is

```
Score = clamp(relevance × policy × graph_factor × contradiction_factor, (0, 1.2])

relevance = weighted mean of per-source [0,1] relevance over available signals
            n_bm25 = b / (b + 8)                     # saturating; b = raw BM25 magnitude
            n_cos  = clip((cos − 0.2) / 0.6, 0, 1)   # calibrated similarity window
policy    = importance_weight × recency_factor × access_factor × cross_project_penalty

importance_weight     = 0.5 + 0.5 × (importance / 10)   # 0.55..1.0
recency_factor        = (1 + age_days)^(-decay_rate)    # exactly 1.0 when pinned
access_factor         = 1 + 0.1 × ln(1 + access_count)  # ACT-R base-level activation
cross_project_penalty = 0.05                            # other-project hits (PreferProject)

graph_factor          = 1.0 for primary hits; 0.70 for 1-hop neighbours (refines/derived_from)
contradiction_factor  = 1.0 clean; 0.50 if curated/skill contradicts; 0.75 if raw-only refutes
```

**Type-differentiated decay and pinned lifecycle.** Rather than a single flat decay slope, `decay_rate` reflects cognitive multi-sector persistence:
- `convention`, `architecture`, `decision`: `0.002` (year-scale half-life; core architectural facts do not rot)
- `pattern`, `preference`, `semantic`: `0.030` (slow decay)
- `bugfix`, `context`, `episodic`, `dream`: `0.050` (standard default)
- `correction`: `0.100` (accelerated fade unless reinforced or promoted)

**Pinned core memory (`pinned = true`):**
- Recency factor is held at `1.0` (zero time decay regardless of type or age).
- Exempt from idle-importance decay in the dream pass (`DecayImportance AND pinned = 0`).
- Pinning clears `expires_at` so core memories cannot silently be pruned by TTL.
- Guaranteed Tier-0 slot at the top of every prewarm block (up to 150 tokens).
- Does *not* bypass trust-tier quarantine (`raw` pinned notes stay quarantined).

**Expansion stays inside the query's contract.** A graph neighbour is never a
back door past the search filters: expanded neighbours pass the same raw-tier
quarantine and type/project/agent/importance/tag constraints as primary hits,
and they never override their parent's score.

The scale is identical in keyword-only and hybrid retrieval — that is the
contract's whole point: a consumer threshold (the prompt hook's floor) means
the same thing in every mode, and a weak result set scores low even for its
top hit. Recall runs keyword (FTS5 BM25) **unioned with** vector cosine over
every embedded row in scope before ranking, so a memory sharing no keywords
with the query is retrievable at all. A missing embedding is renormalised
away, never penalised; zero-relevance hits are dropped. `retrieval_mode`
reports which sources contributed and never changes what `Score` means.

**Calibration basis.** The gate floor (`promptMemoryMinScore = 0.10`, used by
the per-prompt and pre-tool hooks) and the cross-project penalty came from
the deterministic sweep (`mnemos verify calibrate`) over the seeded
calibration corpus, corroborated by translating the old empirically tuned
BM25-scale floor (1.5) onto the contract scale (~0.15). The sweep is pure
and deterministic; re-run `mnemos verify calibrate [fixture]` against a live
corpus to retune. Each result carries its breakdown (`Relevance`,
`PolicyFactor`, raw `BM25`) so "why did this pass the gate" is answerable
per hit.

`Save` returns a `SaveResult` indicating fresh insert vs dedup. Search pulls `limit × 3` raw hits per source, re-ranks, truncates to `limit`. Default filters to live-now; `IncludeStale` and `AsOf` opt into historical queries.

## Progressive scoped reading

`memory.Service.GetMany` and `Neighborhood` add ordinary scoped navigation alongside existing search and administrative single-ID get/replay. Both require an explicit non-empty project and resolve an omitted agent to `default`, matching save. Scope remains hard even with raw or historical options. By default, observations must be non-raw and fact-valid at the effective time: `valid_from <= AsOf`, with invalidation, valid-until, and expiry after that time. Omitted `AsOf` uses the service clock; explicit `IncludeStale` bypasses temporal predicates. Historical validity does not impose `created_at <= AsOf` or reconstruct a historical snapshot of mutable fields. Existing Search/Get/replay semantics stay unchanged.

A batch accepts 1–32 requested IDs, deduplicates in first-requested order, and returns `ok`, `unavailable` (missing and filtered are indistinguishable), or `omitted_for_size`. An `ok` detail is complete except that embedding vectors and model names are excluded. Size-omitted and unavailable results contain only the requested ID/status, with no title, content, scope, or omission reason for filtered records.

The response budget defaults to 32 KiB and has a 256 KiB maximum. `bytes_returned` is the sum of the JSON-encoded complete detail/summary objects, including metadata and JSON escaping. This bounds large titles, tags, rationale, and structured fields as well as content; it excludes the fixed/count-bounded status envelope. IDs must be valid UTF-8 and at most 256 bytes, projects at most 4096 bytes, and agent IDs at most 256 bytes. An oversized stored neighbourhood ID fails the request rather than leaking an unbounded ID outside the budget. Items are never partially truncated to force a detail to fit.

A neighbourhood validates its anchor and reads one coherent SQLite transaction snapshot. It applies scope/trust/time filters before selecting 0–10 visible neighbours on either side, ordered by `(created_at, id)`. Omitted before/after limits default to 3; explicit zero selects none on that side and can still report more eligible neighbours. The window metadata counts visible window members before size omission, not hidden rows. A hidden anchor returns one unavailable status and no window counts. A visible anchor without a session returns just its summary and `no_session: true`. Summaries include original visibility metadata and explicitly flagged, UTF-8-safe title/snippet shortening (256/512 bytes); they do not include full bodies.

Non-mutating scoped reads fully drain their cursors before a single atomic access update. Only unique `ok` batch details increment existing counters; a bookkeeping error fails the complete detail request and rolls back partial updates. Summary browsing and size-omitted/filtered items do not increment access. A counter measures a service read, not confirmed delivery, successful use, or an outcome. Neither operation creates injection events. Batch selections, windows, snippets, budgets, and status objects remain transient; no migration, cursor table, read-event table, remembered copy, or automatic source-URL/path fetch is introduced.

## Pre-warm

The big one. Agents don't self-invoke memory tools reliably, so `mnemos_session_start` doesn't hand back just an ID — it returns a pre-warmed block assembled from:

1. **Conventions** for the project (title + one-line rationale)
2. **Recent sessions** (3 most recent, summarised)
3. **Matching skills** (top 3 by FTS relevance on `goal`)
4. **Corrections** (top 3 on `goal` or `project`)
5. **Hot files** (top 5 by touch count for the project)

Each section is scanned by `safety.Scanner`. High-risk content gets a visible `[MNEMOS: FLAGGED]` banner; low-risk content is sanitised (zero-width + control chars stripped). The whole block is token-budgeted to ~500 tokens (curate aggressively; bloated context hurts more than no context).

Delivery selects complete independently rendered items in pipeline order. A full item is tried first, then a complete title with its type/ID, then a minimal type/ID. Assertions are never byte-truncated. Mandatory contradiction and safety annotations stay attached even to compact items; an item is omitted if those annotations cannot fit. Full candidate sections and sanitised selected sections are scanned, including patterns spanning items. Different IDs, including a rule and its exception, remain distinct. A repeated `(kind, ID)` is emitted only at its first selected occurrence.

Prewarm and explicit `memory.Context` use the same `ceil(UTF-8 bytes / 4)` estimate over final output, including headings, separators, warnings, and pinned overflow notices. This is an estimate, not a model token limit. Pinned items keep their Tier-0 priority and 150-token allocation with a complete shown/available notice when needed. Budgets too small for an entire notice can yield an empty block.

Returned section bodies, selected references, and the existing injection log describe the same emitted items. Recording remains best-effort: failure does not prevent context delivery. An injection event means the service emitted an item; it does not prove the host displayed it, the agent attended to it, or the task succeeded. Historical over-recording cannot be reconstructed and is not backfilled.

Candidate sets, render items, compact snippets, deduplication sets, estimates, and final bodies live only for the request/response. They are never stored as observations, cached in new tables, or copied into injection events. Existing minimal injection rows retain only reference, channel, scope, and time under the existing retention policy. Evaluation labels stay in versioned fixtures; reports go to CLI output or an explicitly requested file.

Compaction-recovery mode reuses the pipeline but prioritises the current session's state: goal + in-session observations come before conventions.

## Skills

Procedural memory. `(agent_id, name)` is unique; saving the same name again bumps `version` instead of duplicating. `effectiveness` is `success_count / use_count`, nudges ranking. No LLM calls from inside the memory layer.

Skills arrive two ways. The primary path is agent-authored: the agent calls `mnemos_skill_save` and we store + rank. The secondary path is pattern-mined: the dream pass (see below) clusters correction observations and promotes them into skills when a cluster crosses a threshold. Promotion is pure string assembly over the structured correction records, so the "no LLM in the memory layer" invariant holds in both paths.

## Dream consolidation

`internal/dream` runs the sleep-time-compute pass, either one-shot (`mnemos dream`) or as a daemon (`mnemos dream --watch`). Each pass produces a `Journal` mirrored into memory as a `TypeDream` observation so the agent can query its own history via normal search.

Pipeline, in order:

1. **Prune** expired observations (`valid_until` passed or `expires_at` reached).
2. **Decay** importance on rows idle past `StaleDays`.
3. **Promote** skills from correction clusters. When three or more live corrections share an `(agent_id, project, label)` key — label = first tag, falling back to a normalised title prefix — the pass synthesises a skill with `## When this applies / ## Avoid / ## Do` sections. Idempotency is keyed on a 12-char sha256 of the group, carried as a `promoted-origin:<hash>` tag on the resulting skill. Repeat passes either no-op (nothing new) or version-bump (new corrections joined the group).
4. **Journal** — write the counts as a dream observation if any step changed state.

`dream.Config` takes the `memory.Reader` + `*skills.Service` for step 3; when either is nil the step silently skips so existing callers that predate promotion still work.

## MCP

We use the [official Model Context Protocol Go SDK](https://github.com/modelcontextprotocol/go-sdk) (v1.5.x, maintained in collaboration with Google). Each tool is registered via `mcp.AddTool[In, Out]` with a typed argument struct — JSON schemas are inferred from `json` and `jsonschema` struct tags, and the SDK validates inputs before invoking the handler. Our `internal/mcp` package is a thin wrapping layer (~750 LOC) that owns (a) the Config + NewServer wiring, (b) 20 core tool handlers plus 4 optional rumination handlers, and (c) the 3 resource handlers. Protocol framing, JSON-RPC, version negotiation, cancellation, and transport plumbing are all the SDK's job.

Resources (`mnemos://session/current`, `mnemos://skills/index`, `mnemos://stats`) are read-only JSON snapshots.

## Installer / doctor

`internal/installer` auto-detects `~/.claude.json`, `~/.cursor/mcp.json`, `~/.codeium/windsurf/mcp_config.json`. Writes idempotently — unrelated keys preserved, atomic temp-file rename. `doctor` is the inverse: probes each target, reports green/red for binary path, config load, storage open, and each agent registration.

## What we deliberately don't do

- **No LLM calls from inside Mnemos.** Reflection and summaries are agent-authored. Skill promotion is the one place we go beyond pure storage, and even there it is deterministic pattern-mining over structured correction records — no model inference, no prompts.
- **No push of context mid-session by default.** Only on session_start and explicit recovery-mode calls. Otherwise it's too easy to flood the agent's context.
- **No vector DB dependency.** Optional embeddings stored as BLOBs in the same SQLite file. Zero infrastructure.
- **No multi-tenant SaaS wiring.** Local-first. Team deploy uses the HTTP transport; auth and ACL live downstream.

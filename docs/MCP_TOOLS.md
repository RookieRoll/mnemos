# MCP tools reference

All tool names are namespaced with `mnemos_`. Every tool has a JSON Schema in its definition — your agent client shows these automatically. A standard server exposes 23 core tools; enabling rumination adds 4 more (27 total).

Writes that omit `project` (`mnemos_save`, `mnemos_correct`, `mnemos_convention`, `mnemos_touch`, `mnemos_session_start`) land in the project the server was started in, which is the basename of its working directory. Claude Code and Codex start the server in the project root. Pass `"project": "*"` to save a memory that applies to every project.

## Shared session continuity (explicit v1)

Continuity restores reported progress into a new independent session, not a
conversation transcript or running process. It uses a verified local workspace;
separate clones/worktrees remain distinct and unbound old sessions stay excluded.

Opt in through `mnemos_session_start`'s optional `continuity` object:

```json
{"agent_id":"worker-a","project":"api","goal":"finish migration","continuity":{"protocol_version":1,"cwd":"D:/work/api","harness_kind":"codex","start_key":"unique-start-invocation"}}
```

For non-Git projects also provide `workspace_root`. Omitted agent resolves to
`default`, independently of harness. Use a new start key for each actual new,
resumed or forked session; retain the same key/input for retries. Conflicting reuse
or a closed target conflicts. Nonempty outer agent/project/goal overrides nested
values. The flat v1 result contains `status=applied|replayed`, `session_id`,
`started_at`, resolved scope, `context_status=available|unavailable`, `prewarm`
(string), and actually delivered `continuity_refs`. Context failure preserves the
committed ID; never open a replacement legacy session to compensate.

For example, a non-Git project rooted at `D:/work/my-app` can start from its
`src` subdirectory with this `mnemos_session_start` input:

```json
{"continuity":{"protocol_version":1,"harness_kind":"codex","start_key":"unique-start-invocation","workspace_root":"D:/work/my-app","cwd":"D:/work/my-app/src"}}
```

Supply the same absolute `workspace_root` on subsequent checkpoint and context
requests, including calls from other subdirectories or agents. The root and
`cwd` must exist, and the canonical `cwd` must be inside the canonical root.
For CLI `--payload` or HTTP open, send only the inner `continuity` object above.
This is a per-request field, not a `config.toml` setting or environment variable.
Non-Git workspaces have unknown branch/HEAD, so choose recovery sources explicitly
with `source_session_id`; they are not automatically selected by resume intent.

### `mnemos_checkpoint`

Requires `protocol_version:1`, exact `session_id`, workspace `cwd` (plus non-Git
root), optional `agent_id`, `action=save|clear`, explicit `expected_revision`
(initially 0) and stable `request_id`. Save replaces the complete `payload`:

```json
{"protocol_version":1,"agent_id":"worker-a","cwd":"D:/work/api","session_id":"<current-session>","action":"save","expected_revision":0,"request_id":"checkpoint-1","visibility":"workspace","payload":{"task":"migration","state":"blocked","summary":"Schema change implemented; targeted test passes.","next_steps":["Check compatibility."],"blockers":["Full regression has not run."],"references":[{"kind":"file","id":"internal/storage/sessions.go"}]}}
```

Visibility defaults to `owner`; `workspace` explicitly publishes only this body
and bounded metadata. It never grants access to private goal/summary/reflection
or referenced observations/files. Shared readers cannot mutate another owner.

Limits in UTF-8 bytes: required task 1–256, required summary at most 4096; state
`active|blocked|done`; next_steps/blockers at most 8 each, 512 per entry;
references at most 16 (`kind=observation|file`, ID at most 256, files relative).
Branch/HEAD is server sampled. Complete canonical payload is at most 16 KiB,
raw request at most 32 KiB. Unknown fields, invalid UTF-8, oversize and high-risk
content fail without partial writes. Hooks never invent checkpoint prose.

Trust only `{protocol_version:1,status:applied|replayed,session_id,revision,
updated_at}` receipts. Exact retries of the current request preserve revision,
time and workspace sample; older requests after a later write conflict. Clear
omits payload, advances revision and keeps a tombstone, preventing old body or
summary resurrection. One latest body is retained, with no TTL/history. Closure
rejects new bodies but allows clear and exact current-request confirmation.
Final `mnemos_session_end` remains separate. Clear is not secure WAL/backup erasure.

### `mnemos_resume_context`

Requires v1, receiving current `session_id`, workspace and optional owner.
`source_session_id` explicitly selects one visible source; unavailable never
falls back. `task` filters exact labels. Otherwise `resume_intent=true` selects
only if the entire visible set contains one checkpoint, at most 24 hours old,
active/blocked, with known branch and HEAD matching this read's current sample.
Any second visible source, including stale/done/summary, prevents selection.
Newest does not win. Explicit older/changed sources retain warnings.

Candidates default to 5, maximum 20. The full compact JSON object (envelope,
references, warnings included) defaults to 32768 bytes, maximum 65536; outer
MCP JSON-RPC framing is excluded. Complete oversized objects are omitted with
`omitted_for_size`/`has_more`, never counted as delivered. Verify current files,
diffs and relevant tests before acting. Reads do not claim, consume, execute,
reopen or persist progress, or create observation-access/injection/success events.

V1 errors expose `code=validation|unavailable|conflict|unsupported|storage_failure`;
conflict may include the owned current revision. A timeout or malformed receipt
after dispatch is `outcome_unknown`, not proof of failure: retry identical input.
Legacy empty hook results are not receipts. Only side-effect-free unsupported
discovery or confirmed unexecuted unknown-command permits legacy startup fallback.
Restart all same-DB hosts before enabling; concurrent old/new binaries cannot
enforce binding isolation. See [API.md](API.md) for CLI/HTTP/client mappings.

## Save & retrieve

### `mnemos_save`
Store an agent-curated observation.

| Param | Required | Notes |
|---|---|---|
| `title` | ✓ | short scannable label |
| `content` | ✓ | the memory itself (structure as what/why/where/learned) |
| `type` | ✓ | `decision`, `bugfix`, `pattern`, `preference`, `context`, `architecture`, `episodic`, `semantic`, `procedural`, `correction`, `convention`, `dream` |
| `tags` |   | string array |
| `importance` |   | 1-10, defaults to 5 |
| `ttl_days` |   | auto-expire |
| `agent_id`, `project`, `session_id` |   | scoping; `project` defaults to the server's project, `"*"` for global |
| `valid_from`, `valid_until` |   | ISO-8601 fact-time bounds |
| `source_kind` |   | `user`, `tool`, `agent_inference`, `dream`, `import`; defaults to `user` |
| `trust_tier` |   | `raw`, `curated`, `skill`; defaults to `curated` |
| `derived_from` |   | parent observation IDs |
| `topic_key` |   | optional case-sensitive grouping key, trimmed UTF-8 up to 256 bytes; same topic appends |
| `replaces_id` |   | explicit expected live same-agent/project target; replacement is atomic |
| `unpin_replacement` |   | acknowledge removing the inherited pin when replacing a pinned version |
| `evidence` |   | up to 16 immutable source-reference identities; see evidence check below |
| `rationale` |   | why this memory matters |
| `pinned` |   | `true` to pin as core memory: immune to decay, Tier-0 prewarm slot, clears expiry |

Returns `{id, title, type, source_kind, trust_tier, derived_from, pinned, created_at, deduped, topic_key, evidence}`. `deduped: true` means an identical live observation already existed; its access counter was bumped instead.

### `mnemos_search`
BM25 + importance + recency + access-frequency ranked search.

| Param | Required | Notes |
|---|---|---|
| `query` | ✓ | FTS query string |
| `type` |   | filter by observation type |
| `tags` |   | array, AND-joined |
| `min_importance` |   | floor |
| `limit` |   | default 20, max 100 |
| `agent_id`, `project` |   | scoping |
| `include_stale` |   | include invalidated/expired |
| `include_raw` |   | include quarantined raw-tier observations |
| `as_of` |   | ISO-8601, historical query |
| `topic_key` |   | optional exact case-sensitive topic filter, no ranking boost |

Returns `{results: [{id, title, type, tags, importance, score, relevance, policy_factor, graph_factor, contradiction_factor, expanded_from, contradicted_by, pinned, snippet, created_at, source_kind, trust_tier, derived_from}], retrieval_mode}`.

`retrieval_mode` is `"hybrid"` when vector cosine was fused into the ranking via RRF, or `"fts"` when BM25/FTS5 alone decided it. It reports the actual per-call outcome, not the configured capability: a hybrid-capable store still returns `"fts"` when the embedder fails at query time or no matching observation carries a vector, so the signal never overstates what ran.

### `mnemos_get`
Fetch full observation by ID. Bumps access counter.

### `mnemos_get_many`
Read selected details after searching. Accepts `ids` (1-32 requested IDs) and a
required non-empty `project`. Optional `agent_id` defaults to the same identity as
save (`default`). IDs are deduplicated in first-requested order. Scope is hard:
missing, other-project/agent, and hidden IDs all return `unavailable` with no body.

Optional `include_raw` and `include_stale` enable explicit inspection within that
scope. `as_of` is an RFC3339 historical validity instant, not a full database
snapshot; `include_stale` explicitly bypasses temporal validity filtering.

`max_bytes` defaults to 32768 and accepts 1-262144. The shared budget counts compact
JSON encoding of each selected observation, including title, tags, rationale,
structured and validity/provenance metadata. Vectors/model names are excluded;
the bounded status envelope and transport pretty-print whitespace are not counted.
A full observation fits or is `omitted_for_size`; no partial body assertion is
returned. The response contains `items`, `bytes_returned`, `max_bytes`, and
`content_limited`. Each item has `id`, `status` (`ok`, `unavailable`, or
`omitted_for_size`) and `observation` only when `ok`. Existing observation fields retain PascalCase names; new `topic_key`, `evidence`,
request and envelope fields use snake_case.

```json
{"project":"mnemos","ids":["01A","01B","01A"],"max_bytes":32768}
```

Only unique returned detail observations increment existing access count/time,
after successful atomic bookkeeping. A service read count does not prove client
receipt or agent success. Unavailable and size-omitted entries do not count.
Retry size-omitted IDs individually with a larger allowed budget when needed.

### `mnemos_neighborhood`
Browse summaries around `id` with required `project` and the same optional agent,
trust, history and byte-budget fields as batch. `before` and `after` each default
to 3, accept 0-10, and preserve explicit zero. Visible neighbours share the
anchor's session/project/agent and are ordered by `(created_at, id)`; hidden rows
do not consume the window. This operation does not reuse historical replay reads.

```json
{"project":"mnemos","id":"01A","before":0,"after":3}
```

The response includes `anchor_id`, ordered `items` with `summary` only for `ok`
entries, `bytes_returned`, `max_bytes`, `content_limited`, and `no_session`. A
`window` reports before/after counts, requested limits, and `has_more_before` /
`has_more_after`; an unavailable anchor exposes no sibling counts. A visible
anchor with no session returns only that summary and `no_session: true`.
Summaries preserve original scope, validity and trust metadata; titles are capped
at 256 UTF-8 bytes and snippets at 512, with `title_truncated` and
`snippet_truncated` flags. The budget counts encoded selected summary objects.
Neighbourhood listing never increments detail-access counts.

Both tools are additive: existing `mnemos_get` and replay semantics remain as
before. No database migration, cursor storage, read-event table, injection event,
used/success flag or remembered copy of results is introduced. Locators inside
observations are returned without fetching their contents. HTTP routes and Go
examples are documented in [API.md](API.md).

### `mnemos_evidence_check`
Record an explicit caller-reported latest result for an existing reference:

```json
{"observation_id":"01A","evidence_key":"<stored key>","status":"changed"}
```

`status` is `unverified`, `matched`, `changed`, or `unavailable`. `matched` requires
a recorded SHA-256 fingerprint baseline. The injected service clock supplies
`checked_at`; callers cannot certify their own timestamp. It returns the reference
with its latest marker. No URL/file is fetched, no source body or check history is
stored, and the check never changes trust, importance, ranking, validity or pinning.
A matching fragment is not proof that its claim is true. A mismatch means review
is needed, not automatic invalidation.

Save references accept `locator`, `relation` (`supports`/`refutes`), optional
`revision`, `selector`, and `fingerprint`. At most 16 references may be submitted before identity deduplication; field limits are
2048 locator bytes, 256 revision bytes, 512 selector bytes; fingerprints are 64
hexadecimal SHA-256 characters. No quote/body field exists. New saves always start
`unverified`, even when a caller supplies markers through another transport.
Output also contains a derived `key`, latest `status`, and optional `checked_at`.
Duplicate identities count as one stored reference; this is never an independent
proof count or confidence score. Old-version references stay with that version;
a replacement starts with only explicitly supplied references.

Topics group assertions, not truths: a rule and its exception may share one topic
and remain live. Use `replaces_id` only for explicit replacement, in the same resolved
agent/project scope. Omitted topic inherits from the target; a different explicit
topic is rejected. Raw cannot replace trusted content. Pins inherit unless
`unpin_replacement` is true, and pinned expiry remains neutralized. Future-effective
replacement is rejected. A matching retry returns its original successor; a stale
or different request returns a tool error with structured `{code:"conflict",error}`.
Inspect the current version before retrying a conflict; do not guess a new target.

Full-store snapshots preserve topic, references, latest markers, and historical
supersession links; [backup guidance](BACKUP.md) describes downgrade constraints.
Vault and skill packs are intentionally lossy for this metadata.

### `mnemos_delete`
Hard-delete by ID. Use only for mistaken saves. For changed facts, use `mnemos_link` with `supersedes`.

### `mnemos_link`
| `source_id`, `target_id`, `link_type` | all required |

`link_type`: `related | caused_by | supersedes | contradicts | refines`. `supersedes` automatically invalidates the target so default searches no longer surface the stale fact.

### `mnemos_pin`
Pin or unpin an observation as core memory. Pinned memories are immune to time decay (`recency_factor = 1.0`), exempt from dream-pass idle importance decay, and guaranteed a Tier-0 slot at the top of every prewarm block (up to 150 tokens). Pinning clears any existing expiry (`expires_at`); unpinning restores type-differentiated decay.

| Param | Required | Notes |
|---|---|---|
| `id` | ✓ | observation ID |
| `pinned` | ✓ | `true` to pin, `false` to unpin |

Returns `{id, title, pinned, expires_at}`.

## Sessions

### `mnemos_session_start`
**Returns a pre-warmed context block, not just an ID.** The block composes conventions for the project, recent session summaries, top matching skills, correction-journal matches on the goal, and hot files.

| Param | Required | Notes |
|---|---|---|
| `project` |   | recommended — enables convention injection |
| `goal` |   | recommended — improves skill and correction matching |
| `agent_id` |   | for multi-agent setups |

Returns `{session_id, started_at, prewarm: {text, token_estimate, section_count, safety_risk}}`. Token budget: ~500 (curated; bloat hurts).

### `mnemos_session_end`
| Param | Required | Notes |
|---|---|---|
| `session_id` | ✓ | |
| `summary` | ✓ | what shipped, what broke, what was learned |
| `reflection` |   | transferable lessons — drives skill promotion |
| `status` |   | `ok` \| `failed` \| `blocked` \| `abandoned` |
| `outcome_tags` |   | short tags characterising the outcome |

Observations from `failed` sessions get a ranking boost — agents learn faster from what went wrong.

### `mnemos_context`
Two modes.

**Default (query-based)**:
```json
{"query": "...", "max_tokens": 2000, "agent_id": "...", "project": "..."}
```
Token-budgeted search-and-pack.

**Recovery (after compaction)**:
```json
{"mode": "recovery", "session_id": "...", "project": "...", "goal": "..."}
```
Restores current session goal, in-session observations, conventions. The "oh shit, context just got compacted" button.

### `mnemos_premortem`
Submit a plan before executing it; returns how similar attempts failed.

```json
{"plan": "add oauth retry handling to the api client", "project": "...", "max_tokens": 800}
```

Sections, failure-first: matching corrections (wider cap than the prewarm block, full tried/wrong/fix substance), past sessions with overlapping goals that ended `failed`/`blocked`/`abandoned`, applicable skills (flagged when under rumination review), and conventions the plan must respect. Empty result returns an explicit "no recorded failures match — proceed" verdict so the agent knows the store was consulted, not skipped. Surfaced memories are recorded in the injection log under the `premortem` channel.

## Provenance

### `mnemos_promote`
Promote an observation between trust tiers after validation.

| Param | Required | Notes |
|---|---|---|
| `id` | ✓ | observation ID |
| `to_tier` | ✓ | `raw`, `curated`, or `skill` |
| `why_better` | ✓ | one sentence naming the concrete signal that justifies promotion; min 16 chars |

Typical path: `raw` to `curated` after tool-output content is verified, or `curated` to `skill` when a pattern is durable enough to reuse.

## Agent supercharge

### `mnemos_correct`
Record a mistake and its fix. Higher retrieval weight than regular observations.

| `title`, `tried`, `wrong_because`, `fix` | all required |
| `trigger_context`, `tags`, `agent_id`, `project`, `session_id`, `importance` | optional |

Surfaced automatically in pre-warm when the session goal matches.

### `mnemos_convention`
Declare a project convention. Auto-injected at every `mnemos_session_start` for the matching project.

| `title`, `rule` | required |
| `project`, `rationale`, `example`, `tags`, `agent_id` | optional; `project` defaults to the server's project |

Rationale is surfaced in pre-warm — WHY matters more than what.

### `mnemos_touch`
Record that a file was touched in the current session. Builds a heat map: frequently-touched files get priority in pre-warming.

| `path` | required |
| `project`, `session_id`, `agent_id`, `note` | optional; `project` defaults to the server's project |

## Skills

### `mnemos_skill_match`
Find skills matching a query. Effectiveness (success/use ratio) nudges ranking so skills that actually worked rise up.

### `mnemos_skill_save`
Save or version a reusable procedure. Keyed by `(agent_id, name)` — same name bumps `version`.

### `mnemos_skill_score`
Return a structured quality report for one skill: raw `effectiveness` (success/use ratio), `use_count`, `success_count`, Bayesian-shrunk `adjusted_effectiveness` (so a 1/1 record does not outrank 9/10), a `recency` factor that decays linearly to zero over 90 days, and a composite 0-1 `score`. Use this when ranking, selecting, or reporting skill quality from measurements rather than guesses.

| Parameter | Notes |
| --- | --- |
| `id` | required — the skill ID returned by `mnemos_skill_save` or `mnemos_skill_match` |

The composite `score` is `adjusted_effectiveness * recency`, intentionally simple so callers can recompute their own weighting from the components. Forms the basis of "skills ranked by measured lift" registries.

## Rumination

Four tools, exposed only when `[rumination].enabled = true` in config (the default). The pattern:

1. `mnemos_ruminate_list` → pick a candidate ID from the pending queue
2. `mnemos_ruminate_pack(id)` → read the review block, answer the hostile prompts
3. `mnemos_ruminate_resolve(id, resolved_by, why_better)` OR `mnemos_ruminate_dismiss(id, reason)`

Server-side validation enforces Popper's falsifiability guard at the resolve boundary: `why_better` must name a new prediction the revision makes. Cosmetic rewording is rejected.

### `mnemos_ruminate_list`
Return pending rumination candidates ordered severity-desc, detected-at-desc. Each candidate has `id`, `monitor`, `severity`, `reason`, `target_kind`, `target_id`, `detected_at`, `evidence_n`. Response also includes `counts` (pending / resolved / dismissed totals) so the agent can report store health at a glance.

| Parameter | Notes |
| --- | --- |
| `limit` | optional; 0 = all |

### `mnemos_ruminate_pack`
Fetch the full review block for one candidate: hypothesis verbatim, disconfirming evidence, falsifiable restatement, hostile-review prompts (steel-man → fatal flaw → falsification vs noise → context shift → new prediction), and an action section naming the provenance tag the revision must carry.

| Parameter | Notes |
| --- | --- |
| `id` | required — candidate ID from `mnemos_ruminate_list` |

### `mnemos_ruminate_resolve`
Close a candidate by naming the revision that replaces the flagged belief.

| Parameter | Notes |
| --- | --- |
| `id` | required |
| `resolved_by` | required — ID of the new skill version or superseding observation |
| `why_better` | required, min 16 chars — one sentence naming a concrete new prediction the revision makes that the old version did not. The Popper guard. |

Idempotent when called a second time with the same `resolved_by`. Rejects attempts to resolve an already-resolved candidate with a different `resolved_by` (that's a conflict; either the store is wrong or the agent is).

### `mnemos_ruminate_dismiss`
Close a candidate as noise. The rule stands.

| Parameter | Notes |
| --- | --- |
| `id` | required |
| `reason` | required, min 8 chars — why the evidence was insufficient to force a revision. Preserved so a later dream pass does not re-raise the same flag without context. |

## Stats

### `mnemos_stats`
Counts, top tags, recent sessions. When rumination is enabled, the response also includes a `rumination` object with `pending`, `resolved`, and `dismissed` counts.

## Resources

- `mnemos://session/current` — most recent open session
- `mnemos://skills/index` — all skills (slim)
- `mnemos://stats` — system statistics
- `mnemos://capabilities` — live `mcp-memory-spec` conformance: the spec version, derived conformance tiers, and per-capability flags (bi-temporal, provenance, trust tiers, typed links, deterministic dedup, hybrid retrieval, portable export/import). Tiers are derived from live behaviour, not hardcoded: a hybrid-capable store drops the embeddings tier when its embedder is unavailable. Also served unauthenticated over HTTP at `GET /v1/capabilities` for pre-auth discovery.

## Safety

Pre-warm and recovery blocks scan their content for prompt-injection patterns (instruction-override phrases, role spoofing, fake tool syntax, zero-width unicode, bidi overrides). High-risk sections are wrapped in `[MNEMOS: FLAGGED risk=high rules=...]` before injection. Low-risk content is sanitised (zero-width + control chars stripped) silently. Memory stores are a new attack surface; we treat them like any other untrusted input source.

# Hindsight absorb-check (Oct 2026)

Target: **Hindsight** — "Hindsight is 20/20: Building Agent Memory that Retains, Recalls, and
Reflects" (Vectorize.io + Virginia Tech + The Washington Post,
[arXiv:2512.12818](https://arxiv.org/abs/2512.12818), Dec 2025); open source at
[vectorize-io/hindsight](https://github.com/vectorize-io/hindsight) (Postgres + pgvector,
server / embedded / cloud; requires an LLM for extraction, entity linking, rerank, and reflect).

## What it actually does

- Does **not** store conversations. An LLM pipeline extracts typed facts from transcripts
  (`retain`), retrieves across four paths with reranking (`recall`), and answers questions by
  reasoning over the bank (`reflect`).
- Memory types: **world fact** (objective claims told to it), **experience fact** (what the agent
  did), **observation** (belief consolidated from many facts, carrying evidence + history),
  **mental model** (curated summary for a recurring question), **knowledge page** (living doc).
- Claims: temporal reasoning ("what did Alice do last spring"), multi-hop fact chaining
  (Alice works-at Google, Google located-in Mountain View), consolidation, per-bank context.
- Numbers: LongMemEval-S 94.6%, LoCoMo 92.0% (conversational recall). Coding-agent evidence:
  AMB sdebench — same 60–61/61 tasks solved with or without memory, but corrections/task
  0.85 → 0.36 (Claude Code) and lower cost.

## Absorb-check against mnemos

| Hindsight mechanism | mnemos today | Verdict |
| --- | --- | --- |
| Typed memory separation (world vs experience facts) | 12 ObsTypes + source_kind + trust tiers | **absorbed** (finer typing) |
| Evidence vs inference separation | source_kind (user/tool/agent_inference/dream/import), raw-tier quarantine, derived_from provenance, rationale | **absorbed** |
| Observations: beliefs with evidence + history | dream-pass promotion (corrections → skills, deterministic), bi-temporal supersession history, contradicts edges + score penalty + naming warnings | **absorbed** (deterministic form) |
| Temporal reasoning | bi-temporal (valid_from/valid_until + created_at/invalidated_at), AsOf queries, type-differentiated decay, pinned immunity | **absorbed, stronger** (fact-time vs system-time split) |
| recall: multi-path retrieval + rerank | hybrid BM25+vector RRF, 1-hop graph expansion (0.70x), contradiction penalty, layered score contract | **absorbed** (deterministic equivalent, no LLM rerank) |
| retain: LLM extraction from transcripts | capture hooks; the harness agent writes via mnemos_save/correct/convention | **not absorbed by design** (no-LLM invariant; capture-rate route) |
| reflect: LLM reasoning to answer questions | none; prewarm/premortem supply context, the agent thinks | **not absorbed by design** (invariant: mnemos stores, the agent thinks) |
| Multi-hop fact chaining at query time | typed links + 1-hop expansion; multi-hop is a stated non-goal (determinism/latency) | **partial** (data model yes, traversal no) |
| Entity layer + knowledge pages (living per-entity summaries) | project/agent/session/tag scoping; no entity rollup plane | **candidate** |
| Memory banks (per-user isolation) | agent_id + project scoping | **absorbed** |
| LongMemEval as primary KPI | roadmap stance: own the refactor-survival benchmark instead | **decided against** (ROADMAP) |

## Outcome

Hindsight's core architecture is already covered — in mnemos' deterministic, local-first idiom —
or explicitly rejected on invariant grounds (no LLM inside the memory layer; no
conversational-benchmark KPI). Two residual takeaways:

1. **Deterministic entity/symbol summary plane (candidate).** Hindsight's mental-model /
   knowledge-page layer validates maintaining a rolling summary per frequently-asked-about entity.
   mnemos' deterministic version: per-symbol / per-file rollups assembled from touch history and
   linked observations at prewarm time. This is the summary plane for **Bet 1 (symbol-anchored
   memory)** — the research now has a concrete shape for it.
2. **External corroboration.** The sdebench numbers (fewer corrections/task, lower cost with
   memory) measure exactly the axis `mnemos verify behavior` targets; useful evidence that the
   efficacy-harness bet is pointed at a real effect.

Why not adopt Hindsight wholesale: it is an LLM-pipeline system (API key required at ingest and
query time), server/cloud-shaped (Postgres + pgvector), and conversation-centric. mnemos is a
single static CGO-free binary, code-centric, and LLM-free by hard invariant — the trade is
deliberate, not accidental.

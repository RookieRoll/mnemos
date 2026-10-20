# Embeddings

Mnemos optionally unions keyword recall with semantic recall, then fuses absolute
relevance signals on the documented [score contract](ARCHITECTURE.md#memory-service).
FTS5 remains available when a query cannot obtain a usable embedding.

## Storage and compatibility

Vectors are little-endian float32 BLOBs in the existing observations table.
`embedding_model` records the provider-qualified model identifier. Cosine runs in
pure Go over the scoped embedded set; keyword and semantic recall are independent.
There is no additional vector database or CGO dependency.

A stored vector contributes only when its model identifier and actual dimension
match the active embedder and usable query vector. A missing model identifier,
different model or incompatible dimension is an absent signal, rather than a
similarity of zero that penalizes a result with nonzero keyword weight. Such vectors remain stored
until explicit maintenance. Model identifiers are operator-configured names, not
attestation that two remote deployments serve identical model weights.

## Providers

Configure all provider-specific fields together:

```toml
[embedding]
provider = "ollama"
model = "nomic-embed-text"
dimension = 768
```

For an OpenAI-compatible `/v1/embeddings` endpoint:

```toml
[embedding]
provider = "openai"
base_url = "https://api.openai.com/v1"
model = "text-embedding-3-small"
dimension = 1536
api_key = "<your-key>"
```

`provider = "none"` selects Noop. With the default `auto`, an explicitly configured
base URL is first probed as an OpenAI-compatible endpoint, then Ollama is probed
(localhost:11434 when no URL is supplied). Unavailable providers fall back to
Noop. Capability-only and continuity-only commands do not perform these probes.

## Backfill and switching models

Fill missing vectors:

```sh
mnemos embed backfill
```

After changing model or dimension, explicitly include incompatible vectors:

```sh
mnemos embed backfill --rebuild-incompatible
```

Default maintenance leaves existing complete vectors unchanged. The explicit
option additionally selects missing model identifiers, model mismatches and
dimension mismatches; compatible rows are left alone. Observation identity,
content, provenance and lifecycle are preserved.

Each invocation captures an ID high-water mark and visits eligible rows with a
keyset cursor in pages of 100. Successes and failures both advance the cursor, so
one permanently failing batch cannot prevent later rows from being attempted.
Each selected ID is attempted at most once per invocation. Cursors and failure
summaries are transient; concurrent mutations may require another invocation.

The final summary reports attempted, successful and failed counts. Any failed row
produces a nonzero exit with the first failing ID, stage and cause, while
successful updates remain saved. Only one failure cause is retained in memory.
Cancellation stops
the run. Retrying the command retries remaining work; no manual SQL clearing is
required to switch models.

## Ranking and operational cost

Keyword magnitude and cosine are normalized independently. Relevance is the
weighted mean of available signals, followed by policy, graph and contradiction
factors. `[search].hybrid_alpha` defaults to `0.5`; explicit `0` and `1` select
semantic and keyword weighting respectively. CLI, hooks, MCP and HTTP share the
same configured service parameters. See the architecture document for formulas.

If the query embedding is unavailable, fails, or has an invalid signal or
dimension, the entire query falls back to keyword weighting, including when
alpha is `0`. With a usable query vector, the configured endpoint weights apply;
alpha `1` skips query embedding and semantic recall. An individual incompatible
stored vector remains an absent signal and is never compared across models.
At alpha `0`, candidates without a compatible vector are excluded while a usable
query vector exists. An empty result uses the existing FTS mode value; it does
not imply that keyword results were returned.

Save-time embedding is synchronous and outside the write transaction; a provider
failure does not prevent saving the observation. Explicit maintenance can fill
the missing vector later. Scoped semantic recall scans vectors, so cost depends
on corpus size and dimensions; measure your workload before choosing a larger
indexing architecture. Provider latency is separate from local retrieval cost.

Embedding providers receive the submitted observation text and query text.
Choose a local endpoint or `provider = "none"` when those texts should remain
on the machine. Mnemos does not fetch referenced source bodies to embed them.

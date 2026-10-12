# Spec Delta

## Purpose

Defines the retrieval score contract for mnemos memory search: which recall sources contribute candidates, how their signals become absolute relevance, how the composite score is produced and bounded, and how consumers gate on it — so scores mean the same thing at every point in the pipeline and in every retrieval mode.

## ADDED Requirements

### Requirement: Independent recall sources
Retrieval SHALL union candidate memories from every configured recall source before ranking. The vector-similarity source SHALL contribute candidates independently of the keyword source, so a memory that shares no keywords with the query is still retrievable. When a recall source is unavailable, retrieval SHALL continue with the remaining sources and report which ones contributed.

#### Scenario: Paraphrase-only memory is retrieved
- **WHEN** a memory is phrased with no keyword overlap to the query, embeddings are configured, and the query's vector is close to the memory's vector
- **THEN** the memory appears in the candidate set and can appear in results

#### Scenario: Exact-identifier memory is retrieved
- **WHEN** a query names an exact identifier that appears verbatim in one memory
- **THEN** that memory is a candidate and ranks above paraphrase-only matches for that query

#### Scenario: Embedder unavailable degrades gracefully
- **WHEN** embeddings are configured but the query cannot be embedded
- **THEN** retrieval returns keyword-sourced results, the reported mode reflects that the vector source did not contribute, and no error surfaces to the caller

### Requirement: Absolute relevance normalization
Each recall source's raw signal SHALL map to an absolute relevance in [0,1], where the value expresses how relevant the memory is to the query, not where it ranks within the candidate batch. The composite relevance SHALL be the weighted mean over available signals, with weights renormalized across the signals actually present, so a memory lacking an embedding is not penalized for the missing signal.

#### Scenario: Weak result sets score low
- **WHEN** a query matches no memory well in any source
- **THEN** every candidate's relevance is low, including the top-ranked candidate

#### Scenario: Missing embedding is not penalized
- **WHEN** one candidate has an embedding and another does not, and both are equally relevant by keyword
- **THEN** the candidate without an embedding is not down-ranked merely for lacking the vector signal

### Requirement: Composite score contract
Retrieval results SHALL carry a composite score of the form relevance multiplied by policy factors (importance, recency, access frequency, project affinity), bounded to (0, 1.2], produced at a single point in the pipeline. The score's meaning and scale SHALL be identical across retrieval modes. The multiplicative form SHALL hold: a candidate with near-zero relevance cannot be lifted into results by policy factors alone.

#### Scenario: Irrelevant but popular memory stays out
- **WHEN** a memory has maximum importance, high access frequency, and recent age but near-zero relevance to the query
- **THEN** its composite score remains below any memory with material relevance

#### Scenario: Scores are comparable across modes
- **WHEN** the same corpus and query are searched once with and once without vector contribution
- **THEN** both result sets' scores lie on the same documented scale and a single gate threshold applies to both

### Requirement: Mode is an honesty signal
The reported retrieval mode SHALL state which recall sources actually contributed to the ranking for that call. It MUST NOT change how the composite score is computed or interpreted.

#### Scenario: Hybrid-capable store reports keyword mode honestly
- **WHEN** embeddings are configured but no candidate carries a vector or the query failed to embed
- **THEN** the reported mode is the keyword mode and the score scale is unchanged

### Requirement: Consumer gating on the contract scale
Every consumer that suppresses low-value memories (per-prompt injection, pre-tool injection) SHALL gate on the composite score contract scale. The gate threshold and the project-affinity penalty SHALL be derived from a recorded calibration over retrieval measurements and surfaced-versus-used data, and the calibration basis SHALL be documented with the values.

#### Scenario: Hybrid mode keeps prompt injection working
- **WHEN** embeddings are configured, vector recall is active, and a memory is strongly on-topic for a user prompt
- **THEN** the prompt hook injects it, passing the same gate that keyword-mode results pass

#### Scenario: Weak matches are still suppressed
- **WHEN** a prompt's candidates are all weakly related
- **THEN** no memory is injected for that prompt

### Requirement: Result explainability
Each retrieval result SHALL report its composite score together with its relevance component and its policy-factor component, so a consumer or an operator can determine why a memory was surfaced.

#### Scenario: Breakdown is consistent with the composite
- **WHEN** a result is returned
- **THEN** the reported components and factors reproduce the reported composite score within floating-point tolerance

#### Scenario: Ranking is deterministic
- **WHEN** the same corpus, configuration, and query are searched twice
- **THEN** both searches return the same order and the same scores

# memory-lifecycle-retrieval Specification

## Purpose
Defines observation lifecycle semantics and graph-augmented retrieval behaviour for Mnemos memory: type-differentiated time decay, pinned core memory with Tier-0 prewarm guarantees and dream-pass protection, 1-hop graph neighbour expansion, and contradiction penalty and annotation in ranked results.

## Requirements

### Requirement: Type-differentiated time decay

The retrieval ranking policy SHALL apply time-decay rates specific to observation types instead of a single global rate. Types carrying durable system knowledge (`convention`, `architecture`, `decision`) SHALL decay at rate 0.002; `pattern`, `preference`, `semantic` at 0.03; `bugfix`, `context`, `episodic` at 0.05; and `correction` at 0.10. The decay rate used for a result SHALL be derivable from its observation type alone.

#### Scenario: Architectural convention survives across months

- **WHEN** an observation of type `convention` or `architecture` is 60 days old
- **THEN** its recency factor remains above 0.99 and it ranks substantially above an equally relevant transient bugfix of the same age

#### Scenario: Transient correction decays rapidly unless reinforced

- **WHEN** an observation of type `correction` is 14 days old and has not been promoted or accessed
- **THEN** its recency factor is below 0.77, letting fresher or reinforced observations outrank it

#### Scenario: Default types keep today's slope

- **WHEN** an observation of type `bugfix`, `context`, or `episodic` is evaluated
- **THEN** its recency factor matches the current global formula with decay rate 0.05

### Requirement: Pinned core memory status

The system SHALL allow observations to carry a `pinned` boolean status. A pinned observation SHALL be immune to recency decay (recency factor exactly 1.0) regardless of its observation type or age, and SHALL be exempt from idle importance decay in the dream pass. Pinning an observation SHALL clear its expiry so it cannot be pruned by TTL; pinning SHALL NOT change trust-tier quarantine behaviour.

#### Scenario: Pinned observation bypasses decay

- **WHEN** a pinned observation is 180 days old
- **THEN** its recency factor evaluates to exactly 1.0 regardless of observation type

#### Scenario: Dream pass leaves pinned memories untouched

- **WHEN** the dream pass runs its idle-importance-decay and prune steps and pinned observations exist
- **THEN** no pinned observation has its importance reduced and none is removed

#### Scenario: Pinning neutralises expiry

- **WHEN** an observation with a future expiry is pinned
- **THEN** its expiry is cleared, the pin response reports the change, and subsequent TTL pruning cannot remove it

#### Scenario: Pinned raw observation stays quarantined

- **WHEN** an observation has `pinned = true` and trust tier `raw` and a default search runs
- **THEN** the observation is excluded from results exactly as an unpinned raw observation would be

### Requirement: Tier-0 prewarm guarantee for pinned memories

Every prewarm block SHALL render active pinned memories for the scope in a dedicated first section before all other sections, within a budget of approximately 150 tokens. Ordering inside the section SHALL be importance descending, then creation time ascending. When pinned memories overflow the budget, the block SHALL report how many were included versus available and SHALL NOT silently drop the overflow without notice.

#### Scenario: Pinned memories lead every prewarm mode

- **WHEN** a prewarm block is built in session-start, compaction-recovery, or premortem mode and active pinned memories exist
- **THEN** the pinned section appears first and passes the safety scanner like every other section

#### Scenario: Overflow is transparent

- **WHEN** active pinned memories exceed the pinned budget
- **THEN** the section includes the highest-importance memories and states the number shown versus total

### Requirement: Pinning surface parity across transports

The system SHALL accept pinned status when saving an observation through every save surface (memory tool, HTTP observation/convention/correct endpoints, Go client) and SHALL provide an in-place pin/unpin operation by observation ID on the tool and HTTP surfaces. Error responses SHALL be returned when the target observation does not exist. Read surfaces SHALL expose pinned status without a second round trip.

#### Scenario: Agent pins an existing observation

- **WHEN** the pin operation is invoked with a valid observation ID and pinned = true
- **THEN** the stored observation becomes pinned and the response echoes the new status

#### Scenario: Unpinning restores normal lifecycle

- **WHEN** the pin operation is invoked with pinned = false on a pinned observation
- **THEN** the observation resumes type-differentiated decay on subsequent ranking

#### Scenario: Pinning a missing observation fails clearly

- **WHEN** the pin operation is invoked with an unknown ID
- **THEN** the call returns a not-found error and changes nothing

#### Scenario: Save surfaces agree

- **WHEN** equivalent observations are saved with pinned = true through the memory tool and through the HTTP convention endpoint
- **THEN** both are stored pinned and both read surfaces report pinned = true

### Requirement: Pinned status survives export and import

Portable export and import SHALL round-trip pinned status verbatim alongside existing fidelity fields. Vault export SHALL record pinned status in frontmatter when set.

#### Scenario: Backup and restore keeps core memory

- **WHEN** a store containing pinned and unpinned observations is exported and imported into a fresh store
- **THEN** every observation arrives with its original pinned status unchanged

### Requirement: 1-hop graph-augmented retrieval expansion

Retrieval SHALL inspect initial top hits (top 3 results with score above 0.45) for live `refines` and `derived_from` edges and SHALL union up to two unretrieved live neighbours per parent into the result set. Expanded results SHALL carry the parent's relevance and policy breakdown scaled by GraphFactor 0.70, SHALL be tagged with the parent's identifier as their expansion source, and SHALL receive the same contradiction treatment as primary hits. When expanded results are delivered through a push surface, they SHALL be recorded in the injection log. Neighbours SHALL honour the query's visibility contract: a raw-tier neighbour SHALL NOT surface unless the query opts into raw results, and the query's type, project, agent, importance, and tag constraints SHALL apply to expanded neighbours unchanged.

#### Scenario: Relevant parent pulls in its refinement

- **WHEN** observation A scores above 0.45 within the top 3, has a live `refines` neighbour B not otherwise retrieved, and no more than two such neighbours exist
- **THEN** B appears in results with the parent breakdown scaled by 0.70 and an expansion-source tag naming A

#### Scenario: Low-scoring hit does not trigger expansion

- **WHEN** an observation scores 0.45 or below or falls outside the top 3
- **THEN** no neighbours are expanded from it

#### Scenario: Parent cap is enforced

- **WHEN** a parent has more than two eligible neighbours
- **THEN** at most two are added, chosen deterministically

#### Scenario: Expansion honours the query's visibility contract

- **WHEN** the query excludes raw-tier results or constrains type, project, agent, importance, or tags, and a linked neighbour violates one of those constraints
- **THEN** the neighbour is not added to the result set

#### Scenario: Expanded neighbour honours contradiction handling

- **WHEN** an expanded neighbour has a live contradiction edge
- **THEN** its contradiction factor applies exactly as it would to a primary hit

### Requirement: Contradiction penalty and annotation

When a candidate has a live `contradicts` edge to or from a live `curated` or `skill` observation, the system SHALL multiply the candidate's score by 0.5 and annotate the result with a warning naming the refuting observation. When the only contradictors are `raw` observations, the factor SHALL be 0.75 and the warning SHALL identify the refutation as unverified. Candidates without contradiction edges SHALL keep their full score.

#### Scenario: Refuted candidate is penalised and flagged

- **WHEN** candidate X is contradicted by live curated observation Y
- **THEN** X's score is multiplied by 0.5, X reports Y as its contradictor, and rendered output carries a warning naming Y

#### Scenario: Unverified refutation softens the penalty

- **WHEN** candidate X is contradicted only by raw-tier observations
- **THEN** X's score is multiplied by 0.75 and the warning marks the refutation as unverified

#### Scenario: Clean candidate retains full score

- **WHEN** candidate X has no live contradiction edges
- **THEN** X's score carries no contradiction factor

### Requirement: Layered score contract

The composite score SHALL be the clamped product `Relevance x PolicyFactor x GraphFactor x ContradictionFactor` bounded to (0, ScoreCeiling], where primary hits use GraphFactor 1.0 and no contradiction uses ContradictionFactor 1.0. Every result SHALL expose the factor breakdown so a consumer can reconstruct the score from its parts.

#### Scenario: Breakdown is auditable

- **WHEN** any search result is returned
- **THEN** multiplying its reported factors reconstructs its reported score within floating-point tolerance

### Requirement: Capability advertisement stays behaviour-bound

The capability document SHALL report `pinned_memory` and `graph_retrieval` only while the corresponding behaviour is live in the running store, consistent with the existing behaviour-bound advertisement contract.

#### Scenario: Flags track live behaviour

- **WHEN** the capability document is read from a build where pinning and graph expansion are active
- **THEN** both flags are true and the derived tier list reflects them

### Requirement: Near-duplicate save merge

The memory layer SHALL store a normalized form of each observation's content.
Plain prose SHALL use Unicode simple lowercase, collapsed whitespace and removal
of trailing sentence marks (`.,!?，。！？、；：`). If interior punctuation or other
symbols remain after that suffix is removed, the complete original body SHALL
instead be compared literally, trimming outer whitespace only. Structure, case
and whitespace within such bodies SHALL be preserved without language parsing.
The layer SHALL use equal normalized forms to merge observations within the same
`(agent_id, project)` and inside a configurable time window whose default is 24
hours. A near-duplicate fold SHALL preserve the agent, project, topic and trust
boundaries and SHALL NOT discard a distinct fact-time interval. Every merge SHALL retain the merged-away content verbatim in a provenance
record. The exact content hash SHALL keep its current definition and value, and
SHALL NOT be recomputed for existing rows.

#### Scenario: punctuation, case or whitespace variant merges

- **WHEN** an observation is saved in an `(agent_id, project)` that already holds a live observation with the same normalized content, and that observation was last touched inside the near-duplicate window
- **THEN** no second live observation is created, the existing observation's access signal is incremented, a provenance record retains the merged-away content, and the save returns the existing observation's id

#### Scenario: same session and title inside the window merges

- **WHEN** an observation is saved with a session id and a non-empty title that match a live observation in the same agent, project and topic inside the near-duplicate window, and their numeric tokens match, with neither body containing syntax or both complete syntax-bearing bodies matching literally after trimming outer whitespace
- **THEN** that observation is merged into under the same rule, the merged-away content is retained verbatim in the provenance record, and the provenance row is marked with the session/title origin

#### Scenario: different content does not merge

- **WHEN** two observations in the same `(agent_id, project)` differ by at least one letter or digit
- **THEN** both are stored as separate observations

#### Scenario: Different observation type or pin intent

- **WHEN** normalized content or session/title matches but the incoming observation type or pinned state differs
- **THEN** near-duplicate folding does not discard that distinct save

#### Scenario: File configuration disables near-duplicate folding

- **WHEN** `[search].near_dup_window` is `0s`
- **THEN** production save paths apply exact deduplication only; omitted configuration uses `24h`, and invalid or negative durations fail configuration loading

#### Scenario: Near-duplicate saves request different retention deadlines

- **WHEN** a matching normalized body or session/title has a different TTL expiry, including a finite deadline versus no expiry
- **THEN** near-duplicate folding preserves the new save instead of changing its requested deadline

#### Scenario: numeric separators preserve meaning

- **WHEN** two contents differ by a decimal point or other numeric separator
- **THEN** their normalized keys differ and the content rule does not merge them

#### Scenario: code punctuation preserves meaning

- **WHEN** two contents differ by code punctuation or structure, such as `!=` versus `=`, `C#` versus `C`, `user_id` versus `userid`, parentheses grouping, field access, subscripts or quoted values
- **THEN** their normalized keys differ and neither the content rule nor the session/title rule merges them
- **AND** syntax-bearing contents also preserve operand changes, case-sensitive names and whitespace inside strings

#### Scenario: plain prose retains formatting equivalence

- **WHEN** plain prose differs only in case, whitespace runs or trailing sentence marks
- **THEN** it may still fold within the configured window
- **AND** interior punctuation is retained rather than assumed to be formatting

#### Scenario: explicit fact time is preserved

- **WHEN** a save has a different explicit fact-time interval from an existing observation
- **THEN** deduplication does not discard that interval

#### Scenario: repeated explicit fact time deduplicates

- **WHEN** the same content and explicit fact-time interval are saved again
- **THEN** exact deduplication returns the existing observation

#### Scenario: outside the window never merges

- **WHEN** a normalized-identical observation exists but falls outside the near-duplicate window
- **THEN** the new observation is created and the save result reports the existing one as a candidate without merging it

#### Scenario: observational content is traceable

- **WHEN** a near-duplicate merge occurs
- **THEN** a provenance record links the survivor to the merged-away save and holds both its verbatim content and its content hash (plus the normalized hash and the merge reason), and this record does not alter retrieval scores

#### Scenario: a merge is auditable

- **WHEN** an operator inspects a provenance record for a merge
- **THEN** the merged-away content is available verbatim for audit and manual recovery; a repeated save can be merged again

#### Scenario: pre-existing rows are not backfilled

- **WHEN** an observation written before this behaviour existed is compared against a new save
- **THEN** no near-duplicate match is produced from it, its content hash is unchanged, and its stored content is untouched

### Requirement: Deterministic dedup capability reporting

`read_capabilities` SHALL describe `deterministic_dedup` in terms of the behaviour
the layer actually enforces: exact content-hash dedup per `(agent_id, project)` is
unconditional, and near-duplicate merging is bounded by the configured window.

#### Scenario: capability text matches enforcement

- **WHEN** capabilities are read
- **THEN** the dedup entry names both layers and the effective window, and it reports no probabilistic or model-dependent matching

### Requirement: Bulk Fact-Time Retirement

`mnemos retire` SHALL set `valid_until = now` on every observation matching the
supplied selectors, in one transaction, and SHALL report the affected count.

Retirement MUST set only `valid_until`. It MUST NOT modify `created_at`,
`invalidated_at`, or any indexed text column, and it MUST NOT delete rows, so
that a historical query before the retirement instant still returns the
observation.

At least one selector among `--project`, `--importance-max`, `--no-access`, and
`--older-than` MUST be supplied; with none, the command SHALL fail as a usage
error and MUST NOT write.

`--dry-run` SHALL run the identical selection, print the identical report, and
write nothing.

#### Scenario: Retire by project, importance and access

- **WHEN** `mnemos retire --project alpha --importance-max 3 --no-access` runs
- **THEN** observations in project `alpha` with importance 3 or less and zero accesses get `valid_until` set to now, and the count is printed

#### Scenario: Retirement is visible to temporal queries

- **WHEN** an observation is retired and is then queried with `AsOf` set to a time before the retirement
- **THEN** the observation is returned; with `AsOf` after the retirement it is not

#### Scenario: Supersession semantics untouched

- **WHEN** a retired observation is inspected
- **THEN** its `invalidated_at` is unchanged and its text is unchanged

#### Scenario: No selector is refused

- **WHEN** `mnemos retire` runs with no selectors
- **THEN** the command exits with a usage error and no row is modified

#### Scenario: Dry run previews

- **WHEN** `mnemos retire --project alpha --dry-run` runs
- **THEN** the count and sample rows that would be affected are printed and no row is modified

#### Scenario: Bounded sample

- **WHEN** more than five observations match
- **THEN** the report includes a sample of at most five entries and the full count

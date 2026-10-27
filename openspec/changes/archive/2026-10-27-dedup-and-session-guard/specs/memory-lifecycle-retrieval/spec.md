## ADDED Requirements

### Requirement: Near-duplicate save merge

The memory layer SHALL store a normalized form of each observation's content —
case-folded with whitespace and punctuation removed — and SHALL use it to merge
observations that differ only in case, whitespace or punctuation, within the same
`(agent_id, project)` and inside a configurable time window whose default is 24
hours. Every merge SHALL retain the merged-away content verbatim in a provenance
record. The exact content hash SHALL keep its current definition and value, and
SHALL NOT be recomputed for existing rows.

#### Scenario: punctuation, case or whitespace variant merges

- **WHEN** an observation is saved in an `(agent_id, project)` that already holds a live observation whose content differs only in case, whitespace or punctuation, and that observation was last touched inside the near-duplicate window
- **THEN** no second live observation is created, the existing observation's access signal is incremented, a provenance record retains the merged-away content, and the save returns the existing observation's id

#### Scenario: same session and title inside the window merges

- **WHEN** an observation is saved with a session id and a non-empty title that match a live observation inside the near-duplicate window
- **THEN** that observation is merged into under the same rule, the merged-away content is retained verbatim in the provenance record, and the provenance row is marked with the session/title origin

#### Scenario: different content does not merge

- **WHEN** two observations in the same `(agent_id, project)` differ by at least one letter or digit
- **THEN** both are stored as separate observations

#### Scenario: outside the window never merges

- **WHEN** a normalized-identical observation exists but falls outside the near-duplicate window
- **THEN** the new observation is created and the save result reports the existing one as a candidate without merging it

#### Scenario: observational content is traceable

- **WHEN** a near-duplicate merge occurs
- **THEN** a provenance record links the survivor to the merged-away save and holds both its verbatim content and its content hash (plus the normalized hash and the merge reason), and this record does not alter retrieval scores

#### Scenario: a merge is auditable and reversible

- **WHEN** an operator inspects a provenance record for a merge
- **THEN** the merged-away content is available verbatim and can be restored by saving it again, with no dedicated undo command required

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

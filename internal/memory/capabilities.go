package memory

// SpecVersion identifies the mcp-memory-spec revision this build targets.
// Provisional: the spec is a working draft (docs/spec/mcp-memory-spec-draft.md)
// and the tier names below are not yet ratified.
const SpecVersion = "mcp-memory-spec/draft-0"

// Conformance tier names (provisional; see docs/spec/mcp-memory-spec-draft.md §9).
const (
	tierCore       = "memory-core"
	tierProvenance = "provenance"
	tierEmbeddings = "embeddings"
	tierPortable   = "portable"
)

// Capabilities is an honest, behaviour-bound statement of which parts of the
// mcp-memory-spec this store actually satisfies right now. The Tiers list is
// DERIVED from the flags below, never hardcoded: a hybrid-capable build that
// has lost its embedder reports HybridRetrieval=false and drops the embeddings
// tier, so the document can never claim a capability the store cannot deliver
// at call time. That honesty is the whole point of advertising capabilities;
// it is precisely the property a hardcoded compliance string fails to keep.
type Capabilities struct {
	Spec           string   `json:"spec"`
	Implementation string   `json:"implementation"`
	Tiers          []string `json:"tiers"`

	// Schema-level guarantees. Unconditional for this implementation: the
	// bi-temporal columns, provenance substrate, trust tiers, typed links,
	// and content-hash dedup are fixed parts of the schema.
	Bitemporal         bool `json:"bitemporal"`
	Provenance         bool `json:"provenance"`
	TrustTiers         bool `json:"trust_tiers"`
	TypedLinks         bool `json:"typed_links"`
	DeterministicDedup bool `json:"deterministic_dedup"`

	// DeterministicDedupSemantics states, in words, what DeterministicDedup
	// actually enforces: the two layers, their bounds, and the fact that
	// neither is probabilistic or model-derived. A bare flag invites the
	// reader to imagine more matching than exists, so the flag ships with the
	// sentence that limits it.
	DeterministicDedupSemantics string `json:"deterministic_dedup_semantics"`

	// NearDupWindow is the effective near-duplicate merge window as a duration
	// string, or "disabled" when the operator set it to zero. It is the bound
	// the dedup sentence refers to, reported from live service state rather
	// than from a constant.
	NearDupWindow string `json:"near_dup_window"`

	// HybridRetrieval is runtime-bound: true only when an embedder is
	// configured and dimensioned, so it tracks what search can actually do.
	HybridRetrieval bool `json:"hybrid_retrieval"`

	// PortableExport reports fidelity-preserving export/import: a full-store
	// dump round-trips with identity, bi-temporal validity, and provenance
	// intact. This is portability, not live multi-store federation, which is
	// a separate future capability.
	PortableExport bool `json:"portable_export"`

	// PinnedMemory reports first-class core memory support: observations can
	// be pinned to bypass recency decay and earn Tier-0 prewarm delivery.
	PinnedMemory bool `json:"pinned_memory"`

	// GraphRetrieval reports 1-hop expansion along refines and derived_from
	// edges and contradiction-aware scoring during memory search.
	GraphRetrieval bool `json:"graph_retrieval"`
}

// Capabilities returns the live capability document for this service. Read it
// fresh each call so HybridRetrieval reflects current embedder state.
func (s *Service) Capabilities() Capabilities {
	c := Capabilities{
		Spec:               SpecVersion,
		Implementation:     "mnemos",
		Bitemporal:         true,
		Provenance:         true,
		TrustTiers:         true,
		TypedLinks:         true,
		DeterministicDedup: true,
		HybridRetrieval:    s.HybridEnabled(),
		// Fidelity-preserving export/import ships (ROADMAP Bet 2 phase 4):
		// Service.Restore round-trips a full dump verbatim.
		PortableExport: true,
		PinnedMemory:   true,
		GraphRetrieval: true,
	}
	c.NearDupWindow, c.DeterministicDedupSemantics = s.dedupSemantics()
	c.Tiers = c.tiers()
	return c
}

// dedupSemantics renders the dedup sentence and the effective window it refers
// to. Both come from live service state: a store with near-duplicate merging
// switched off (near_dup_window = 0s) must not advertise a window it does not
// enforce, and one with the default must not describe a rule the operator
// turned off. The sentence names both layers, their bounds, and the fact that
// neither is probabilistic or model-derived.
func (s *Service) dedupSemantics() (window, sentence string) {
	if s.nearDupWindow <= 0 {
		return "disabled",
			"exact: content-hash dedup per (agent_id, project) is unconditional; " +
				"near-duplicate: disabled, so no content is folded on normalization or on session and title; " +
				"neither layer is probabilistic or model-derived"
	}
	window = s.nearDupWindow.String()
	return window,
		"exact: content-hash dedup per (agent_id, project) is unconditional; " +
			"near-duplicate: merged into an existing observation only when content normalizes identically " +
			"(letters and digits only, case-folded, whitespace and punctuation ignored) or the save shares the " +
			"survivor's session id and non-empty title, and only inside the " + window + " window, with the " +
			"merged-away content retained verbatim in a provenance record; " +
			"neither layer is probabilistic or model-derived"
}

// tiers derives the conformance tier list from the capability flags. Reading
// the flags rather than a constant is what keeps the advertised tier in step
// with what the store can actually do.
func (c Capabilities) tiers() []string {
	// memory-core (save/search/get/link/invalidate + bi-temporal + dedup) is
	// always satisfied by this implementation.
	t := []string{tierCore}
	if c.Provenance && c.TrustTiers {
		t = append(t, tierProvenance)
	}
	if c.HybridRetrieval {
		t = append(t, tierEmbeddings)
	}
	if c.PortableExport {
		t = append(t, tierPortable)
	}
	return t
}

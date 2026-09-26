package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/polyxmedia/mnemos/internal/injection"
)

// Embedder is the minimal interface memory.Service needs from an embedding
// provider — the full interface lives in internal/embedding. Redeclared
// here to avoid an import cycle and keep the service boundary clean.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	Dimension() int
	Model() string
}

// InjectionRecorder is the narrow surface the memory service needs from
// the injection log: one batched write per Context block. Declared at the
// consumer so tests can capture surfacings with a small fake. Log is
// fire-and-forget — a failed measurement write never fails the Context call.
type InjectionRecorder interface {
	Log(ctx context.Context, channel injection.Channel, agentID, project, sessionID string, refs []injection.Ref) error
}

// Service is the agent-facing API over observations. It owns ID assignment,
// timestamp defaults, ranking, supersession, and token-budgeted context
// packing. Transports (MCP, HTTP, CLI) call Service methods, never the Store
// directly.
type Service struct {
	store      Store
	ranker     *Ranker
	hybrid     HybridParams
	embedder   Embedder
	injections InjectionRecorder
	clock      func() time.Time
}

// Config bundles injected dependencies for the memory service.
type Config struct {
	Store      Store
	RankParams RankParams
	Hybrid     HybridParams
	Embedder   Embedder
	// Injections is optional; when nil, Context blocks are not recorded
	// in the injection-event log.
	Injections InjectionRecorder
	Clock      func() time.Time
	AgentID    string
}

// NewService builds a Service from a Store and ranking params.
func NewService(cfg Config) *Service {
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	params := cfg.RankParams
	if params == (RankParams{}) {
		params = DefaultRankParams()
	}
	hybrid := cfg.Hybrid
	if hybrid == (HybridParams{}) {
		hybrid = DefaultHybridParams()
	}
	hybrid = hybrid.fillDefaults()
	return &Service{
		store:      cfg.Store,
		ranker:     NewRanker(params),
		hybrid:     hybrid,
		embedder:   cfg.Embedder,
		injections: cfg.Injections,
		clock:      cfg.Clock,
	}
}

// HybridEnabled reports whether vector search is active.
func (s *Service) HybridEnabled() bool {
	return s.embedder != nil && s.embedder.Dimension() > 0
}

// Save creates a new observation from agent-provided input, or bumps the
// access counter on an existing identical one (dedup-on-save). Returns
// SaveResult so callers can distinguish a fresh insert from a dedup hit.
func (s *Service) Save(ctx context.Context, in SaveInput) (*SaveResult, error) {
	if strings.TrimSpace(in.Title) == "" {
		return nil, fmt.Errorf("save: title is required")
	}
	if strings.TrimSpace(in.Content) == "" {
		return nil, fmt.Errorf("save: content is required")
	}
	if !in.Type.Valid() {
		return nil, fmt.Errorf("save: invalid obs_type %q", in.Type)
	}
	if in.Importance == 0 {
		in.Importance = 5
	}
	if in.Importance < 1 || in.Importance > 10 {
		return nil, fmt.Errorf("save: importance must be 1..10")
	}

	now := s.clock().UTC()
	validFrom := now
	if in.ValidFrom != nil {
		validFrom = in.ValidFrom.UTC()
	}

	agent := defaultString(in.AgentID, "default")
	hash := hashContent(in.Type, in.Title, in.Content, in.Rationale, in.Structured)

	sourceKind := in.SourceKind
	if sourceKind == "" {
		sourceKind = SourceUser
	}
	if !sourceKind.Valid() {
		return nil, fmt.Errorf("save: invalid source_kind %q", sourceKind)
	}
	trustTier := in.TrustTier
	if trustTier == "" {
		trustTier = TrustCurated
	}
	if !trustTier.Valid() {
		return nil, fmt.Errorf("save: invalid trust_tier %q", trustTier)
	}
	// Quarantine clamp: trust tier must not be writer-opt-in. Content that
	// did not come straight from the user (tool output, the agent's own
	// inference, imports) is forced to the raw tier regardless of what the
	// caller asked for, so a compromised tool or injection-driven agent
	// cannot self-promote into the trusted, searchable set by simply
	// omitting trust_tier. Downgrade is silent rather than an error so
	// honest callers that don't set the field keep working.
	trustTier = clampTrustTier(sourceKind, trustTier)

	// Dedup: if the same (agent, project, content_hash) already lives, bump
	// access and return without writing. Invalidated rows don't count — a
	// re-save can legitimately resurrect a superseded fact.
	if existing, err := s.store.FindByContentHash(ctx, agent, in.Project, hash); err != nil {
		return nil, fmt.Errorf("dedup lookup: %w", err)
	} else if existing != nil {
		if err := s.store.BumpAccess(ctx, existing.ID); err != nil {
			return nil, err
		}
		return &SaveResult{Observation: existing, Deduped: true}, nil
	}

	o := &Observation{
		ID:          ulid.Make().String(),
		SessionID:   in.SessionID,
		AgentID:     agent,
		Project:     in.Project,
		Title:       in.Title,
		Content:     in.Content,
		Type:        in.Type,
		Tags:        in.Tags,
		Importance:  in.Importance,
		CreatedAt:   now,
		ValidFrom:   validFrom,
		ValidUntil:  in.ValidUntil,
		ContentHash: hash,
		Structured:  in.Structured,
		Rationale:   in.Rationale,
		SourceKind:  sourceKind,
		TrustTier:   trustTier,
		DerivedFrom: in.DerivedFrom,
	}
	if in.TTLDays > 0 {
		t := now.AddDate(0, 0, in.TTLDays)
		o.ExpiresAt = &t
	}

	if err := s.store.Insert(ctx, o); err != nil {
		return nil, err
	}

	// Embed in the background-ish way: if an embedder is configured, try
	// to generate the vector and attach it. A failure here is non-fatal —
	// the observation still exists; hybrid search just misses this one
	// candidate until the next backfill pass.
	if s.HybridEnabled() {
		if vec, err := s.embedder.Embed(ctx, embedText(o)); err == nil && len(vec) > 0 {
			o.Embedding = vec
			o.EmbeddingModel = s.embedder.Model()
			_ = s.store.UpdateEmbedding(ctx, o.ID, s.embedder.Model(), vec)
		}
	}

	return &SaveResult{Observation: o, Deduped: false}, nil
}

// Restore reinserts an exported observation verbatim, preserving its ID,
// bi-temporal timestamps, provenance, trust tier, access count, and content
// hash. It is the fidelity-import counterpart to Save: where Save mints a new
// ID, stamps fresh timestamps, runs content-hash dedup, and clamps the trust
// tier, Restore does none of that. It is the trusted path for restoring a
// full-store dump (the JSON equivalent of copying the database file), so it
// does not quarantine or re-scan. Returns true when a row was written, false
// when an observation with the same ID was already present and skipped.
func (s *Service) Restore(ctx context.Context, o *Observation) (bool, error) {
	if strings.TrimSpace(o.Title) == "" {
		return false, fmt.Errorf("restore: title is required")
	}
	if !o.Type.Valid() {
		return false, fmt.Errorf("restore: invalid type %q", o.Type)
	}
	if o.SourceKind != "" && !o.SourceKind.Valid() {
		return false, fmt.Errorf("restore: invalid source_kind %q", o.SourceKind)
	}
	if o.TrustTier != "" && !o.TrustTier.Valid() {
		return false, fmt.Errorf("restore: invalid trust_tier %q", o.TrustTier)
	}
	return s.store.Restore(ctx, o)
}

// clampTrustTier enforces the quarantine invariant: only user-authored
// observations may occupy the tier the caller requested. Every other source
// — tool output, agent inference, imports, and dream — is forced to raw no
// matter what trust_tier the caller asked for. This is the server-side
// backstop behind the Bet 2 provenance model; without it the quarantine is
// opt-in by the writer, so poisoned tool output or an injection-driven agent
// could land straight in the searchable curated set just by leaving
// trust_tier unset.
//
// SourceDream is deliberately NOT trusted here. Nothing internal writes with
// SourceDream (the dream pass journals as the default SourceUser), so the
// only way a row carries it is an external mnemos_save asserting
// source_kind="dream" — which would otherwise be a free pass into the
// trusted tier. If a future internal consolidation path needs to write
// trusted dream content, it must set the tier through a privileged path, not
// by self-asserting this source kind over the public tool surface.
func clampTrustTier(sk SourceKind, requested TrustTier) TrustTier {
	if sk == SourceUser {
		return requested
	}
	return TrustRaw
}

// embedText assembles the text we embed for an observation. Title + content
// + rationale produces a richer signal than content alone.
func embedText(o *Observation) string {
	parts := []string{o.Title, o.Content}
	if o.Rationale != "" {
		parts = append(parts, o.Rationale)
	}
	return strings.Join(parts, "\n")
}

// hashContent produces a stable SHA-256 hex digest over the identity-
// defining fields of an observation. Normalised (whitespace trimmed) so
// trivial formatting differences still dedup.
func hashContent(t ObsType, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(t))
	h.Write([]byte{0})
	for _, p := range parts {
		h.Write([]byte(strings.TrimSpace(p)))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Get returns the full observation (also bumps access count via the store).
func (s *Service) Get(ctx context.Context, id string) (*Observation, error) {
	return s.store.Get(ctx, id)
}

// Delete removes an observation outright. Prefer Supersede for anything that
// was ever true; delete is for saves that were mistakes.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}

// Supersede records that newID replaces oldID: links them and invalidates
// the old observation as of now. This is the right call for "we used to do
// X, now we do Y" — preserves provenance, hides the stale fact from default
// searches.
func (s *Service) Supersede(ctx context.Context, newID, oldID string) error {
	now := s.clock().UTC()
	if err := s.store.Link(ctx, newID, oldID, LinkSupersedes); err != nil {
		return err
	}
	return s.store.Invalidate(ctx, oldID, now)
}

// Invalidate marks an observation as no longer true as of now.
func (s *Service) Invalidate(ctx context.Context, id string) error {
	return s.store.Invalidate(ctx, id, s.clock().UTC())
}

// PromoteInput carries the arguments for moving an observation between
// trust tiers. WhyBetter is the Popper-style justification: one sentence
// stating what concrete signal justifies the tier change. Short, terse
// demands enforce that the caller actually thought about it — the same
// guard the rumination resolve flow uses.
type PromoteInput struct {
	ID        string
	ToTier    TrustTier
	WhyBetter string
}

// minPromoteReasonLen matches mnemos_ruminate_resolve's threshold so the
// UX is consistent: 16 chars is enough to force "revised because we saw
// X" rather than rubber-stamp "ok", cheap enough to not obstruct real use.
const minPromoteReasonLen = 16

// Promote moves an observation between trust tiers. Typical path is
// TrustRaw → TrustCurated when tool-output content has been validated by
// the user or a rumination/dream pass. TrustRaw → TrustSkill is allowed
// (consolidation directly skips the curated step when a skill is the
// right shape). Returns ErrNotFound if the observation doesn't exist.
func (s *Service) Promote(ctx context.Context, in PromoteInput) error {
	if in.ID == "" {
		return fmt.Errorf("promote: id is required")
	}
	if !in.ToTier.Valid() {
		return fmt.Errorf("promote: invalid to_tier %q", in.ToTier)
	}
	if strings.TrimSpace(in.WhyBetter) == "" {
		return fmt.Errorf("promote: why_better is required")
	}
	if len(strings.TrimSpace(in.WhyBetter)) < minPromoteReasonLen {
		return fmt.Errorf("promote: why_better must be at least %d chars — state a concrete signal, not filler", minPromoteReasonLen)
	}
	return s.store.SetTrustTier(ctx, in.ID, in.ToTier)
}

// crossProjectPenalty is the policy-factor multiplier applied to a hit
// whose project differs from SearchInput.PreferProject. It is one of the
// factors of the composite score contract, so a cross-project hit must be
// materially relevant to clear any consumer's gate. The 2026-07-13
// injection log motivated it (83% of prompt_hook surfacings — 4982 of
// 6016 — were cross-project noise); the gate sweep (mnemos verify
// calibrate) re-derived 0.05 on the contract scale, stricter than the
// original BM25-calibrated 0.1 but in the same crush-the-noise regime.
// Re-tune by re-running the sweep against a live corpus.
const crossProjectPenalty = 0.05

// Search runs the recall sources (keyword and, when an embedder is
// configured, vector) unioned before ranking, and applies the composite
// score contract: Score = fused relevance x policy factors (importance,
// recency, access, project affinity), bounded to (0, ScoreCeiling]. See
// docs/ARCHITECTURE.md and the memory-retrieval spec for the contract.
func (s *Service) Search(ctx context.Context, in SearchInput) ([]SearchResult, error) {
	res, _, err := s.SearchWithMode(ctx, in)
	return res, err
}

// SearchWithMode is Search plus the retrieval mode that actually ran for
// this query. The mode is derived from the real per-call outcome (did the
// query embed and did any candidate carry a vector signal), not from
// HybridEnabled(): a hybrid-capable store reports RetrievalFTS when the
// embedder fails open or no candidate carries a vector, so the signal
// never overstates what happened. The mode never changes what Score means.
//
// The score contract (docs/ARCHITECTURE.md, memory-retrieval spec):
// each recall source normalises to absolute relevance in [0,1]; relevance
// is the weighted mean over available signals (missing signals are
// renormalised away, never penalised); Score = relevance x policy, clamped
// to ScoreCeiling; hits with zero relevance are dropped, keeping the bound
// (0, ScoreCeiling] honest.
func (s *Service) SearchWithMode(ctx context.Context, in SearchInput) ([]SearchResult, RetrievalMode, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	// Pull a wider net than the caller asked for so ranking has room to
	// re-order. This is the cheap part; network/context cost is downstream.
	in.Limit = limit * 3

	// Recall source 1: keyword (FTS5 BM25).
	raw, err := s.store.Search(ctx, in)
	if err != nil {
		return nil, RetrievalFTS, err
	}

	// Recall source 2: vector cosine over the whole scoped set, unioned in
	// before ranking. The old path only re-ranked keyword candidates, so a
	// memory sharing no keywords with the query could never surface at any
	// alpha. With no embedder configured (Noop: Dimension 0, HybridEnabled
	// false) this source is skipped entirely and relevance comes from the
	// keyword signal alone. Query embed failures fail open the same way.
	var qvec []float32
	if s.HybridEnabled() && in.Query != "" {
		qvec, _ = s.embedder.Embed(ctx, in.Query)
	}
	if len(qvec) > 0 {
		raw = s.unionVectorRecall(ctx, in, qvec, raw, limit*3)
	}

	now := s.clock().UTC()
	vectorSignal := false
	kept := raw[:0] // filter in place: no signal, no result
	for i := range raw {
		var nBM25 *float64
		if raw[i].BM25 > 0 {
			v := normBM25(raw[i].BM25, s.hybrid.BM25K)
			nBM25 = &v
		}
		var nCos *float64
		if len(qvec) > 0 && vecHasSignal(raw[i].Observation.Embedding) {
			v := normCos(cosine(raw[i].Observation.Embedding, qvec), s.hybrid.CosLow, s.hybrid.CosHigh)
			nCos = &v
			vectorSignal = true
		}
		rel := combineRelevance(nBM25, nCos, s.hybrid.Alpha)
		if rel <= 0 {
			continue
		}
		policy := s.ranker.PolicyFactor(raw[i].Observation, now)
		if in.PreferProject != "" && raw[i].Observation.Project != "" &&
			raw[i].Observation.Project != in.PreferProject {
			policy *= crossProjectPenalty
		}
		raw[i].Relevance = rel
		raw[i].PolicyFactor = policy
		raw[i].Score = rel * policy
		if raw[i].Score > ScoreCeiling {
			raw[i].Score = ScoreCeiling
		}
		kept = append(kept, raw[i])
	}

	mode := RetrievalFTS
	if vectorSignal {
		mode = RetrievalHybrid
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Score > kept[j].Score })

	if len(kept) > limit {
		kept = kept[:limit]
	}
	return kept, mode, nil
}

// unionVectorRecall runs the vector recall source: cosine over every
// embedded row in scope, appending the top cap hits that keyword recall
// did not already surface. The two sources union before ranking, so a
// memory sharing no keywords with the query is retrievable at all. Store
// errors fail open: the keyword candidates pass through unchanged.
func (s *Service) unionVectorRecall(ctx context.Context, in SearchInput, qvec []float32, cands []SearchResult, cap int) []SearchResult {
	rows, err := s.store.ListEmbeddings(ctx, in)
	if err != nil || len(rows) == 0 {
		return cands
	}
	seen := make(map[string]bool, len(cands))
	for _, c := range cands {
		seen[c.Observation.ID] = true
	}
	type scored struct {
		o   Observation
		cos float64
	}
	recall := make([]scored, 0, len(rows))
	for _, o := range rows {
		if seen[o.ID] {
			continue
		}
		recall = append(recall, scored{o: o, cos: cosine(o.Embedding, qvec)})
	}
	sort.SliceStable(recall, func(i, j int) bool { return recall[i].cos > recall[j].cos })
	if len(recall) > cap {
		recall = recall[:cap]
	}
	for _, r := range recall {
		cands = append(cands, SearchResult{
			Observation: r.o,
			Snippet:     snippetFrom(r.o.Content),
		})
	}
	return cands
}

// snippetFrom renders a one-line preview for vector-recall hits, which —
// unlike keyword hits — have no FTS5 snippet to show.
func snippetFrom(content string) string {
	s := strings.Join(strings.Fields(content), " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// Context returns a pre-budgeted block of memory ready for injection into
// agent context. The block never exceeds MaxTokens (estimated at ~4 chars
// per token), and items are included in descending rank until the budget is
// spent.
func (s *Service) Context(ctx context.Context, in ContextInput) (*ContextBlock, error) {
	maxTokens := in.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2000
	}

	results, err := s.Search(ctx, SearchInput{
		Query:   in.Query,
		AgentID: in.AgentID,
		Project: in.Project,
		Limit:   50,
	})
	if err != nil {
		return nil, err
	}

	block := &ContextBlock{}
	var sb strings.Builder
	budget := maxTokens
	for _, r := range results {
		entry := formatContextEntry(r.Observation)
		cost := estimateTokens(entry)
		if cost > budget {
			continue
		}
		sb.WriteString(entry)
		sb.WriteString("\n\n")
		budget -= cost
		block.Observations = append(block.Observations, r.Observation)
		if budget < 64 {
			break
		}
	}
	block.Text = strings.TrimRight(sb.String(), "\n")
	block.TokenEstimate = maxTokens - budget

	// Fire-and-forget: the surfacing log is measurement, and measurement
	// must never break the surface being measured.
	if s.injections != nil && len(block.Observations) > 0 {
		refs := make([]injection.Ref, 0, len(block.Observations))
		for _, o := range block.Observations {
			refs = append(refs, injection.Ref{Kind: injection.KindObservation, ID: o.ID})
		}
		_ = s.injections.Log(ctx, injection.ChannelContext, in.AgentID, in.Project, "", refs)
	}
	return block, nil
}

// RecordSurfaced bumps access_count for memories that actually reached the
// agent's context. Best-effort and idempotent-ish by design: a failure on
// one ID does not stop the rest, and the error is advisory.
//
// Until this existed, access_count only moved on an explicit Get, which no
// hook path calls — 9543 injections across 603 sessions left all 59 stored
// memories on access_count = 0. That made Ranker.Score's access term
// 1 + AccessBoost*ln(1+0), a constant 1.0, so a signal the ranker was
// designed around had never once fired.
//
// Callers must pass only memories that survived suppression, never every
// candidate considered. Suppression caps a memory at one surfacing per
// project per injection.SuppressWindow, which is what keeps this from
// becoming a rich-get-richer loop where a memory ranks high because it was
// injected often and gets injected often because it ranks high.
func (s *Service) RecordSurfaced(ctx context.Context, ids []string) error {
	var firstErr error
	for _, id := range ids {
		if id == "" {
			continue
		}
		if err := s.store.BumpAccess(ctx, id); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("record surfaced %s: %w", id, err)
		}
	}
	return firstErr
}

// Stats proxies to the store and tags live/total counts.
func (s *Service) Stats(ctx context.Context) (Stats, error) {
	return s.store.Stats(ctx)
}

// Prune removes expired observations.
func (s *Service) Prune(ctx context.Context) (int64, error) {
	return s.store.Prune(ctx, s.clock().UTC())
}

// Link records an arbitrary edge between two observations.
func (s *Service) Link(ctx context.Context, sourceID, targetID string, linkType LinkType) error {
	if !linkType.Valid() {
		return fmt.Errorf("invalid link type %q", linkType)
	}
	if linkType == LinkSupersedes {
		return s.Supersede(ctx, sourceID, targetID)
	}
	return s.store.Link(ctx, sourceID, targetID, linkType)
}

func formatContextEntry(o Observation) string {
	tags := ""
	if len(o.Tags) > 0 {
		tags = " [" + strings.Join(o.Tags, ",") + "]"
	}
	return fmt.Sprintf("## %s (%s)%s\n%s", o.Title, o.Type, tags, o.Content)
}

// estimateTokens is a conservative ~4 chars/token heuristic. Not exact, but
// good enough for budget-sized decisions; the MCP layer can swap in a real
// tokenizer later without touching callers.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

func defaultString(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

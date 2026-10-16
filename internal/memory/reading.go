package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrInvalidReadInput identifies an invalid scoped reading request.
var ErrInvalidReadInput = errors.New("invalid scoped read input")

// ReadStatus describes availability without exposing why a record is filtered.
type ReadStatus string

const (
	// ReadOK means a complete detail or bounded summary is returned.
	ReadOK ReadStatus = "ok"
	// ReadUnavailable covers missing records and records outside visibility.
	ReadUnavailable ReadStatus = "unavailable"
	// ReadOmittedForSize means no detail or summary fit the remaining budget.
	ReadOmittedForSize ReadStatus = "omitted_for_size"
)

// GetManyInput requests bounded details from an explicit project and agent.
type GetManyInput struct {
	IDs          []string  `json:"ids"`
	Project      string    `json:"project"`
	AgentID      string    `json:"agent_id,omitempty"`
	IncludeRaw   bool      `json:"include_raw,omitempty"`
	IncludeStale bool      `json:"include_stale,omitempty"`
	AsOf         time.Time `json:"as_of,omitempty"`
	MaxBytes     int       `json:"max_bytes,omitempty"`
}

// ReadResult identifies one unique requested ID and optionally its detail.
type ReadResult struct {
	ID          string       `json:"id"`
	Status      ReadStatus   `json:"status"`
	Observation *Observation `json:"observation,omitempty"`
}

// GetManyResult reports ordered detail outcomes and request-local byte usage.
type GetManyResult struct {
	Items          []ReadResult `json:"items"`
	BytesReturned  int          `json:"bytes_returned"`
	MaxBytes       int          `json:"max_bytes"`
	ContentLimited bool         `json:"content_limited"`
}

// NeighborhoodInput requests visible same-session summaries around ID.
type NeighborhoodInput struct {
	ID           string    `json:"id"`
	Project      string    `json:"project"`
	AgentID      string    `json:"agent_id,omitempty"`
	IncludeRaw   bool      `json:"include_raw,omitempty"`
	IncludeStale bool      `json:"include_stale,omitempty"`
	AsOf         time.Time `json:"as_of,omitempty"`
	MaxBytes     int       `json:"max_bytes,omitempty"`
	Before       *int      `json:"before,omitempty"`
	After        *int      `json:"after,omitempty"`
}

// ObservationSummary retains visibility metadata without a full detail body.
type ObservationSummary struct {
	// TopicKey and Evidence are bounded by the complete encoded-summary budget.
	TopicKey         string              `json:"topic_key,omitempty"`
	Evidence         []EvidenceReference `json:"evidence,omitempty"`
	ID               string              `json:"id"`
	SessionID        string              `json:"session_id,omitempty"`
	AgentID          string              `json:"agent_id"`
	Project          string              `json:"project"`
	Title            string              `json:"title"`
	Type             ObsType             `json:"type"`
	Snippet          string              `json:"snippet"`
	TitleTruncated   bool                `json:"title_truncated"`
	SnippetTruncated bool                `json:"snippet_truncated"`
	CreatedAt        time.Time           `json:"created_at"`
	ValidFrom        time.Time           `json:"valid_from"`
	ValidUntil       *time.Time          `json:"valid_until,omitempty"`
	InvalidatedAt    *time.Time          `json:"invalidated_at,omitempty"`
	ExpiresAt        *time.Time          `json:"expires_at,omitempty"`
	SourceKind       SourceKind          `json:"source_kind"`
	TrustTier        TrustTier           `json:"trust_tier"`
}

// SummaryResult describes one visible timeline entry or unavailable anchor.
type SummaryResult struct {
	ID      string              `json:"id"`
	Status  ReadStatus          `json:"status"`
	Summary *ObservationSummary `json:"summary,omitempty"`
}

// NeighborhoodWindow counts visible selected neighbours in a coherent snapshot.
type NeighborhoodWindow struct {
	BeforeCount   int  `json:"before_count"`
	AfterCount    int  `json:"after_count"`
	BeforeLimit   int  `json:"before_limit"`
	AfterLimit    int  `json:"after_limit"`
	HasMoreBefore bool `json:"has_more_before"`
	HasMoreAfter  bool `json:"has_more_after"`
}

// NeighborhoodResult contains bounded chronological summaries, never details.
type NeighborhoodResult struct {
	AnchorID       string              `json:"anchor_id"`
	Items          []SummaryResult     `json:"items"`
	NoSession      bool                `json:"no_session"`
	Window         *NeighborhoodWindow `json:"window,omitempty"`
	BytesReturned  int                 `json:"bytes_returned"`
	MaxBytes       int                 `json:"max_bytes"`
	ContentLimited bool                `json:"content_limited"`
}

// ScopedReadInput carries a validated hard scope and a fixed visibility instant.
type ScopedReadInput struct {
	IDs                      []string
	Project, AgentID         string
	IncludeRaw, IncludeStale bool
	AsOf, Now                time.Time
}

// NeighborhoodReadInput carries a validated anchor and bounded visible counts.
type NeighborhoodReadInput struct {
	Scope         ScopedReadInput
	ID            string
	Before, After int
}

// NeighborhoodSnapshot is a coherent non-mutating storage view.
type NeighborhoodSnapshot struct {
	Anchor                      *Observation
	Before, After               []Observation
	HasMoreBefore, HasMoreAfter bool
}

// ScopedReadStore is consumed only by the new progressive reading operations.
// Reads drain all cursors before returning; access updates run afterwards.
type ScopedReadStore interface {
	ReadScoped(ctx context.Context, in ScopedReadInput) ([]Observation, error)
	ReadNeighborhoodSnapshot(ctx context.Context, in NeighborhoodReadInput) (*NeighborhoodSnapshot, error)
	IncrementAccessMany(ctx context.Context, ids []string, at time.Time) error
}

// DefaultReadMaxBytes is the default aggregate encoded-object response budget.
const DefaultReadMaxBytes = 32 * 1024

// MaxReadBytes caps the aggregate encoded-object response budget.
const MaxReadBytes = 256 * 1024

func readScope(project, agent string, raw, stale bool, asOf, now time.Time) (ScopedReadInput, error) {
	if strings.TrimSpace(project) == "" || len(project) > 4096 {
		return ScopedReadInput{}, fmt.Errorf("memory: project must be non-empty and at most 4096 bytes: %w", ErrInvalidReadInput)
	}
	if strings.TrimSpace(agent) == "" && agent != "" {
		return ScopedReadInput{}, fmt.Errorf("memory: agent cannot be blank: %w", ErrInvalidReadInput)
	}
	if len(agent) > 256 {
		return ScopedReadInput{}, fmt.Errorf("memory: agent exceeds 256 bytes: %w", ErrInvalidReadInput)
	}
	return ScopedReadInput{Project: project, AgentID: defaultString(agent, "default"), IncludeRaw: raw, IncludeStale: stale, AsOf: asOf.UTC(), Now: now.UTC()}, nil
}

func readBudget(n int) (int, error) {
	if n == 0 {
		return DefaultReadMaxBytes, nil
	}
	if n < 1 || n > MaxReadBytes {
		return 0, fmt.Errorf("memory: max_bytes must be 1..%d: %w", MaxReadBytes, ErrInvalidReadInput)
	}
	return n, nil
}

func readID(id string) error {
	if strings.TrimSpace(id) == "" || len(id) > 256 || !utf8.ValidString(id) {
		return fmt.Errorf("memory: id must be valid UTF-8 and 1..256 bytes: %w", ErrInvalidReadInput)
	}
	return nil
}

// GetMany returns bounded complete details in first-requested ID order. Only
// successful returned bodies increment access, in one atomic update after reads.
func (s *Service) GetMany(ctx context.Context, in GetManyInput) (*GetManyResult, error) {
	if len(in.IDs) < 1 || len(in.IDs) > 32 {
		return nil, fmt.Errorf("memory: batch requires 1..32 IDs: %w", ErrInvalidReadInput)
	}
	scope, err := readScope(in.Project, in.AgentID, in.IncludeRaw, in.IncludeStale, in.AsOf, s.clock())
	if err != nil {
		return nil, err
	}
	budget, err := readBudget(in.MaxBytes)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, id := range in.IDs {
		if err := readID(id); err != nil {
			return nil, err
		}
		if !seen[id] {
			scope.IDs = append(scope.IDs, id)
			seen[id] = true
		}
	}
	store, ok := s.store.(ScopedReadStore)
	if !ok {
		return nil, fmt.Errorf("memory: store does not support scoped reading")
	}
	rows, err := store.ReadScoped(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("memory: read scoped details: %w", err)
	}
	byID := map[string]Observation{}
	for _, o := range rows {
		byID[o.ID] = o
	}
	out := &GetManyResult{Items: []ReadResult{}, MaxBytes: budget}
	var accessed []string
	for _, id := range scope.IDs {
		item := ReadResult{ID: id, Status: ReadUnavailable}
		if o, ok := byID[id]; ok {
			// Embeddings are retrieval internals, not progressive-read payloads.
			o.Embedding = nil
			o.EmbeddingModel = ""
			o.AccessCount++
			at := scope.Now
			o.LastAccessedAt = &at
			encoded, err := json.Marshal(o)
			if err != nil {
				return nil, fmt.Errorf("memory: encode detail size: %w", err)
			}
			if out.BytesReturned+len(encoded) > budget {
				item.Status = ReadOmittedForSize
				out.ContentLimited = true
			} else {
				item.Status = ReadOK
				item.Observation = &o
				out.BytesReturned += len(encoded)
				accessed = append(accessed, id)
			}
		}
		out.Items = append(out.Items, item)
	}
	if len(accessed) > 0 {
		if err := store.IncrementAccessMany(ctx, accessed, scope.Now); err != nil {
			return nil, fmt.Errorf("memory: record detail access: %w", err)
		}
	}
	return out, nil
}

func neighborhoodLimit(n *int) (int, error) {
	if n == nil {
		return 3, nil
	}
	if *n < 0 || *n > 10 {
		return 0, fmt.Errorf("memory: before/after must be 0..10: %w", ErrInvalidReadInput)
	}
	return *n, nil
}

// Neighborhood lists bounded visible same-session summaries from one snapshot.
// It neither increments detail access nor records injection events.
func (s *Service) Neighborhood(ctx context.Context, in NeighborhoodInput) (*NeighborhoodResult, error) {
	if err := readID(in.ID); err != nil {
		return nil, err
	}
	scope, err := readScope(in.Project, in.AgentID, in.IncludeRaw, in.IncludeStale, in.AsOf, s.clock())
	if err != nil {
		return nil, err
	}
	budget, err := readBudget(in.MaxBytes)
	if err != nil {
		return nil, err
	}
	before, err := neighborhoodLimit(in.Before)
	if err != nil {
		return nil, err
	}
	after, err := neighborhoodLimit(in.After)
	if err != nil {
		return nil, err
	}
	store, ok := s.store.(ScopedReadStore)
	if !ok {
		return nil, fmt.Errorf("memory: store does not support scoped reading")
	}
	view, err := store.ReadNeighborhoodSnapshot(ctx, NeighborhoodReadInput{Scope: scope, ID: in.ID, Before: before, After: after})
	if err != nil {
		return nil, fmt.Errorf("memory: read neighborhood: %w", err)
	}
	out := &NeighborhoodResult{AnchorID: in.ID, Items: []SummaryResult{}, MaxBytes: budget}
	if view.Anchor == nil {
		out.Items = append(out.Items, SummaryResult{ID: in.ID, Status: ReadUnavailable})
		return out, nil
	}
	out.NoSession = view.Anchor.SessionID == ""
	if !out.NoSession {
		out.Window = &NeighborhoodWindow{BeforeCount: len(view.Before), AfterCount: len(view.After), BeforeLimit: before, AfterLimit: after, HasMoreBefore: view.HasMoreBefore, HasMoreAfter: view.HasMoreAfter}
	}
	rows := append(append(append([]Observation{}, view.Before...), *view.Anchor), view.After...)
	for _, o := range rows {
		if err := readID(o.ID); err != nil {
			return nil, fmt.Errorf("memory: stored neighborhood ID exceeds response bounds")
		}
		title, titleCut := boundedSummary(o.Title, 256)
		snippet, snippetCut := boundedSummary(o.Content, 512)
		summary := &ObservationSummary{TopicKey: o.TopicKey, Evidence: o.Evidence, ID: o.ID, SessionID: o.SessionID, AgentID: o.AgentID, Project: o.Project, Title: title, Type: o.Type, Snippet: snippet, TitleTruncated: titleCut, SnippetTruncated: snippetCut, CreatedAt: o.CreatedAt, ValidFrom: o.ValidFrom, ValidUntil: o.ValidUntil, InvalidatedAt: o.InvalidatedAt, ExpiresAt: o.ExpiresAt, SourceKind: o.SourceKind, TrustTier: o.TrustTier}
		encoded, err := json.Marshal(summary)
		if err != nil {
			return nil, fmt.Errorf("memory: encode summary size: %w", err)
		}
		item := SummaryResult{ID: o.ID, Status: ReadOK, Summary: summary}
		if out.BytesReturned+len(encoded) > budget {
			item.Status = ReadOmittedForSize
			item.Summary = nil
			out.ContentLimited = true
		} else {
			out.BytesReturned += len(encoded)
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func boundedSummary(text string, n int) (string, bool) {
	valid := strings.ToValidUTF8(text, "\uFFFD")
	changed := valid != text
	text = valid
	if len(text) <= n {
		return text, changed
	}
	for n > 0 && !utf8.RuneStart(text[n]) {
		n--
	}
	return text[:n], true
}

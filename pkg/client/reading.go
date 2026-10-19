package client

import (
	"context"
	"time"
)

// GetManyInput requests scoped details for 1-32 IDs with a shared encoded-object byte budget.
type GetManyInput struct {
	IDs          []string  `json:"ids"`
	Project      string    `json:"project"`
	AgentID      string    `json:"agent_id,omitempty"`
	IncludeRaw   bool      `json:"include_raw,omitempty"`
	IncludeStale bool      `json:"include_stale,omitempty"`
	AsOf         time.Time `json:"as_of,omitzero"`
	MaxBytes     int       `json:"max_bytes,omitempty"`
}

// ReadStatus distinguishes returned, unavailable and size-omitted observations.
type ReadStatus string

const (
	// ReadOK identifies a returned detail or summary.
	ReadOK ReadStatus = "ok"
	// ReadUnavailable deliberately does not distinguish missing from filtered records.
	ReadUnavailable ReadStatus = "unavailable"
	// ReadOmittedForSize identifies an eligible record whose body could not fit.
	ReadOmittedForSize ReadStatus = "omitted_for_size"
)

// ReadResult is one unique requested ID and its optional detail body.
type ReadResult struct {
	ID          string       `json:"id"`
	Status      ReadStatus   `json:"status"`
	Observation *Observation `json:"observation,omitempty"`
}

// GetManyResult describes detail selection and its aggregate encoded-object byte limit.
type GetManyResult struct {
	Items          []ReadResult `json:"items"`
	BytesReturned  int          `json:"bytes_returned"`
	MaxBytes       int          `json:"max_bytes"`
	ContentLimited bool         `json:"content_limited"`
}

// NeighborhoodInput requests a visible anchor and bounded same-session summaries.
// Nil Before/After default to three; pointers to zero explicitly disable that side.
type NeighborhoodInput struct {
	ID           string    `json:"id"`
	Project      string    `json:"project"`
	AgentID      string    `json:"agent_id,omitempty"`
	Before       *int      `json:"before,omitempty"`
	After        *int      `json:"after,omitempty"`
	IncludeRaw   bool      `json:"include_raw,omitempty"`
	IncludeStale bool      `json:"include_stale,omitempty"`
	AsOf         time.Time `json:"as_of,omitzero"`
	MaxBytes     int       `json:"max_bytes,omitempty"`
}

// ObservationSummary is bounded navigation text with original visibility metadata.
type ObservationSummary struct {
	TopicKey         string              `json:"topic_key"`
	Evidence         []EvidenceReference `json:"evidence,omitempty"`
	ID               string              `json:"id"`
	Title            string              `json:"title"`
	Snippet          string              `json:"snippet"`
	TitleTruncated   bool                `json:"title_truncated"`
	SnippetTruncated bool                `json:"snippet_truncated"`
	Type             string              `json:"type"`
	AgentID          string              `json:"agent_id"`
	Project          string              `json:"project"`
	SessionID        string              `json:"session_id"`
	CreatedAt        time.Time           `json:"created_at"`
	ValidFrom        time.Time           `json:"valid_from"`
	ValidUntil       *time.Time          `json:"valid_until,omitempty"`
	InvalidatedAt    *time.Time          `json:"invalidated_at,omitempty"`
	ExpiresAt        *time.Time          `json:"expires_at,omitempty"`
	TrustTier        string              `json:"trust_tier"`
	SourceKind       string              `json:"source_kind"`
}

// SummaryResult is an available bounded summary or an explicit unavailable/size status.
type SummaryResult struct {
	ID      string              `json:"id"`
	Status  ReadStatus          `json:"status"`
	Summary *ObservationSummary `json:"summary,omitempty"`
}

// NeighborhoodWindow reports visible counts, requested limits and remaining neighbours.
type NeighborhoodWindow struct {
	BeforeCount   int  `json:"before_count"`
	AfterCount    int  `json:"after_count"`
	BeforeLimit   int  `json:"before_limit"`
	AfterLimit    int  `json:"after_limit"`
	HasMoreBefore bool `json:"has_more_before"`
	HasMoreAfter  bool `json:"has_more_after"`
}

// NeighborhoodResult contains ordered summaries and explicit window/content limits.
type NeighborhoodResult struct {
	AnchorID       string              `json:"anchor_id"`
	Items          []SummaryResult     `json:"items"`
	NoSession      bool                `json:"no_session"`
	Window         *NeighborhoodWindow `json:"window,omitempty"`
	BytesReturned  int                 `json:"bytes_returned"`
	MaxBytes       int                 `json:"max_bytes"`
	ContentLimited bool                `json:"content_limited"`
}

// GetMany returns selected details; only unique ok results increment access counts.
func (c *Client) GetMany(ctx context.Context, in GetManyInput) (*GetManyResult, error) {
	var out GetManyResult
	if err := c.do(ctx, "POST", "/v1/observations/batch", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Neighborhood browses summaries without incrementing detail access counters.
func (c *Client) Neighborhood(ctx context.Context, in NeighborhoodInput) (*NeighborhoodResult, error) {
	var out NeighborhoodResult
	if err := c.do(ctx, "POST", "/v1/observations/neighborhood", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

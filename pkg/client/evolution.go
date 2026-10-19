package client

import (
	"context"
	"net/url"
	"time"
)

// EvidenceReference locates version-bound material without copying its body.
// Status is only the latest caller-reported check, not independent certification.
type EvidenceReference struct {
	Key         string     `json:"key"`
	Locator     string     `json:"locator"`
	Relation    string     `json:"relation"`
	Revision    string     `json:"revision,omitempty"`
	Selector    string     `json:"selector,omitempty"`
	Fingerprint string     `json:"fingerprint,omitempty"`
	Status      string     `json:"status"`
	CheckedAt   *time.Time `json:"checked_at,omitempty"`
}

// EvidenceCheckInput supplies an explicit check of an existing reference.
// Matched requires a previously recorded fingerprint; the server timestamps it.
type EvidenceCheckInput struct {
	ObservationID string `json:"-"`
	EvidenceKey   string `json:"evidence_key"`
	Status        string `json:"status"`
}

// CheckEvidence records only the latest reported check without fetching a source
// or changing observation trust, validity, pinning, importance or ranking.
func (c *Client) CheckEvidence(ctx context.Context, in EvidenceCheckInput) (*EvidenceReference, error) {
	var out EvidenceReference
	if err := c.do(ctx, "POST", "/v1/observations/"+url.PathEscape(in.ObservationID)+"/evidence/check", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

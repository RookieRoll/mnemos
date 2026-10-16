package mcp

import (
	"context"
	"errors"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/polyxmedia/mnemos/internal/memory"
)

type evidenceCheckArgs struct {
	ObservationID string `json:"observation_id"`
	EvidenceKey   string `json:"evidence_key"`
	Status        string `json:"status" jsonschema:"caller-reported unverified|matched|changed|unavailable; matched requires recorded fingerprint baseline"`
}

func (s *Server) handleEvidenceCheck(ctx context.Context, _ *mcpsdk.CallToolRequest, a evidenceCheckArgs) (*mcpsdk.CallToolResult, any, error) {
	out, err := s.cfg.Memory.CheckEvidence(ctx, memory.EvidenceCheckInput{ObservationID: a.ObservationID, EvidenceKey: a.EvidenceKey, Status: a.Status})
	if err != nil {
		return evolutionError(err)
	}
	return jsonResult(out)
}

func evolutionError(err error) (*mcpsdk.CallToolResult, any, error) {
	if !errors.Is(err, memory.ErrConflict) {
		return nil, nil, err
	}
	res, _, marshalErr := jsonResult(map[string]any{"code": "conflict", "error": err.Error()})
	if marshalErr != nil {
		return nil, nil, marshalErr
	}
	res.IsError = true
	return res, nil, nil
}

// evidenceSaveArgs exposes immutable reference identity, never imported check markers.
type evidenceSaveArgs struct {
	Locator     string `json:"locator"`
	Relation    string `json:"relation" jsonschema:"supports|refutes"`
	Revision    string `json:"revision,omitempty"`
	Selector    string `json:"selector,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty" jsonschema:"64 hexadecimal SHA-256 characters when supplied"`
}

func toEvidence(in []evidenceSaveArgs) []memory.EvidenceReference {
	out := make([]memory.EvidenceReference, 0, len(in))
	for _, e := range in {
		out = append(out, memory.EvidenceReference{Locator: e.Locator, Relation: e.Relation, Revision: e.Revision, Selector: e.Selector, Fingerprint: e.Fingerprint})
	}
	return out
}

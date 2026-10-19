package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

// ContinuityOpenInput explicitly opts into continuity v1 and identifies one startup invocation.
type ContinuityOpenInput struct {
	ProtocolVersion int    `json:"protocol_version"`
	AgentID         string `json:"agent_id,omitempty"`
	CWD             string `json:"cwd,omitempty"`
	WorkspaceRoot   string `json:"workspace_root,omitempty"`
	Project         string `json:"project,omitempty"`
	Goal            string `json:"goal,omitempty"`
	HarnessKind     string `json:"harness_kind"`
	HostSessionID   string `json:"host_session_id,omitempty"`
	StartKey        string `json:"start_key"`
	SourceSessionID string `json:"source_session_id,omitempty"`
	Task            string `json:"task,omitempty"`
	ResumeIntent    bool   `json:"resume_intent,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	MaxBytes        int    `json:"max_bytes,omitempty"`
}

// ContinuityRef names an emitted progress body, not memory consumption.
type ContinuityRef struct {
	SessionID  string `json:"session_id"`
	Revision   int64  `json:"revision"`
	SourceKind string `json:"source_kind"`
}

// ContinuityOpenResult distinguishes committed identity from context availability.
type ContinuityOpenResult struct {
	ProtocolVersion int             `json:"protocol_version"`
	Status          string          `json:"status"`
	SessionID       string          `json:"session_id"`
	StartedAt       time.Time       `json:"started_at"`
	AgentID         string          `json:"agent_id"`
	Project         string          `json:"project"`
	WorkspaceKey    string          `json:"workspace_key"`
	WorkspaceRoot   string          `json:"workspace_root"`
	ContextStatus   string          `json:"context_status"`
	Prewarm         string          `json:"prewarm,omitempty"`
	ContinuityRefs  []ContinuityRef `json:"continuity_refs,omitempty"`
	prewarmPresent  bool
}

// UnmarshalJSON retains required-field presence so a missing startup context is
// not mistaken for a valid empty context after a potentially committed open.
func (r *ContinuityOpenResult) UnmarshalJSON(data []byte) error {
	type wire ContinuityOpenResult
	var out wire
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if raw, ok := fields["prewarm"]; ok {
		var text *string
		if err := json.Unmarshal(raw, &text); err != nil {
			return err
		}
		out.prewarmPresent = text != nil
	}
	*r = ContinuityOpenResult(out)
	return nil
}

// ContinuityWorkspaceState is sampled branch/HEAD metadata, not a clean-files guarantee.
type ContinuityWorkspaceState struct {
	Branch string `json:"branch"`
	HEAD   string `json:"head"`
	Known  bool   `json:"known"`
}

// ContinuityReference is a locator that never grants the source owner's access rights.
type ContinuityReference struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// ContinuityPayload contains explicit reported progress, with a complete 16 KiB limit.
type ContinuityPayload struct {
	Task       string                   `json:"task"`
	State      string                   `json:"state"`
	Summary    string                   `json:"summary"`
	NextSteps  []string                 `json:"next_steps,omitempty"`
	Blockers   []string                 `json:"blockers,omitempty"`
	References []ContinuityReference    `json:"references,omitempty"`
	Workspace  ContinuityWorkspaceState `json:"workspace"`
}

// ContinuityCheckpointInput replaces or clears an exact owned session's checkpoint.
type ContinuityCheckpointInput struct {
	ProtocolVersion  int                `json:"protocol_version"`
	AgentID          string             `json:"agent_id,omitempty"`
	CWD              string             `json:"cwd,omitempty"`
	WorkspaceRoot    string             `json:"workspace_root,omitempty"`
	SessionID        string             `json:"session_id"`
	Action           string             `json:"action"`
	ExpectedRevision *int64             `json:"expected_revision"`
	RequestID        string             `json:"request_id"`
	Visibility       string             `json:"visibility,omitempty"`
	Payload          *ContinuityPayload `json:"payload,omitempty"`
}

// ContinuityCheckpointResult confirms one committed revision or its exact retry.
type ContinuityCheckpointResult struct {
	ProtocolVersion int       `json:"protocol_version"`
	Status          string    `json:"status"`
	SessionID       string    `json:"session_id"`
	Revision        int64     `json:"revision"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ContinuityContextInput reads candidates or a precise visible source without side effects.
type ContinuityContextInput struct {
	ProtocolVersion int    `json:"protocol_version"`
	AgentID         string `json:"agent_id,omitempty"`
	CWD             string `json:"cwd,omitempty"`
	WorkspaceRoot   string `json:"workspace_root,omitempty"`
	SessionID       string `json:"session_id"`
	SourceSessionID string `json:"source_session_id,omitempty"`
	Task            string `json:"task,omitempty"`
	ResumeIntent    bool   `json:"resume_intent,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	MaxBytes        int    `json:"max_bytes,omitempty"`
}

// ContinuitySource is a visible checkpoint or same-owner ended summary.
type ContinuitySource struct {
	SourceKind       string             `json:"source_kind"`
	SessionID        string             `json:"session_id"`
	Revision         int64              `json:"revision"`
	AgentID          string             `json:"agent_id"`
	HarnessKind      string             `json:"harness_kind"`
	RecordedAt       time.Time          `json:"recorded_at"`
	State            string             `json:"state,omitempty"`
	SessionClosed    bool               `json:"session_closed"`
	Visibility       string             `json:"visibility"`
	Payload          *ContinuityPayload `json:"payload,omitempty"`
	Summary          string             `json:"summary,omitempty"`
	Stale            bool               `json:"stale"`
	WorkspaceChanged bool               `json:"workspace_changed"`
	WorkspaceUnknown bool               `json:"workspace_unknown"`
}

// ContinuityContextResult accounts for the complete encoded JSON response budget.
type ContinuityContextResult struct {
	ProtocolVersion int                `json:"protocol_version"`
	Status          string             `json:"status"`
	Source          *ContinuitySource  `json:"source,omitempty"`
	Candidates      []ContinuitySource `json:"candidates,omitempty"`
	Ambiguous       bool               `json:"ambiguous"`
	HasMore         bool               `json:"has_more"`
	OmittedForSize  int                `json:"omitted_for_size"`
	BytesReturned   int                `json:"bytes_returned"`
	MaxBytes        int                `json:"max_bytes"`
}

// ContinuityCapabilities reports explicit protocol support without opening a session.
type ContinuityCapabilities struct {
	ProtocolVersion int  `json:"protocol_version"`
	Supported       bool `json:"supported"`
}

// OutcomeUnknownError means a mutation may have committed; retry its identical key and input.
type OutcomeUnknownError struct{ Cause error }

// Error describes the unknown outcome without treating a transport failure as a receipt.
func (e *OutcomeUnknownError) Error() string {
	return "mnemos continuity: outcome_unknown: " + e.Cause.Error()
}

// Unwrap preserves the underlying transport or decoding error.
func (e *OutcomeUnknownError) Unwrap() error { return e.Cause }

// ContinuityCapabilities queries explicit support without creating progress state.
func (c *Client) ContinuityCapabilities(ctx context.Context) (*ContinuityCapabilities, error) {
	var out struct {
		ProtocolVersion int   `json:"protocol_version"`
		Supported       *bool `json:"supported"`
	}
	if err := c.doJSON(ctx, "GET", "/v1/continuity/capabilities", nil, &out, true); err != nil {
		return nil, err
	}
	if out.ProtocolVersion != 1 || out.Supported == nil {
		return nil, fmt.Errorf("client: unsupported continuity capability version")
	}
	return &ContinuityCapabilities{ProtocolVersion: out.ProtocolVersion, Supported: *out.Supported}, nil
}

// ContinuityOpen opens or confirms one startup; context failure preserves its committed ID.
func (c *Client) ContinuityOpen(ctx context.Context, in ContinuityOpenInput) (*ContinuityOpenResult, error) {
	var out ContinuityOpenResult
	if err := c.continuityMutation(ctx, "/v1/continuity/open", in, &out); err != nil {
		return nil, err
	}
	if out.ProtocolVersion != 1 || !receiptStatus(out.Status) || !validContinuityID(out.SessionID) || out.StartedAt.IsZero() || (out.ContextStatus != "available" && out.ContextStatus != "unavailable") || (out.ContextStatus == "available" && !out.prewarmPresent) {
		return nil, &OutcomeUnknownError{Cause: fmt.Errorf("client: invalid continuity startup receipt")}
	}
	return &out, nil
}

// ContinuityCheckpoint saves or clears progress with explicit CAS and retry identity.
func (c *Client) ContinuityCheckpoint(ctx context.Context, in ContinuityCheckpointInput) (*ContinuityCheckpointResult, error) {
	var out ContinuityCheckpointResult
	if err := c.continuityMutation(ctx, "/v1/continuity/checkpoint", in, &out); err != nil {
		return nil, err
	}
	if out.ProtocolVersion != 1 || !receiptStatus(out.Status) || out.SessionID != in.SessionID || in.ExpectedRevision == nil || *in.ExpectedRevision == math.MaxInt64 || out.Revision != *in.ExpectedRevision+1 || out.Revision < 1 || out.UpdatedAt.IsZero() {
		return nil, &OutcomeUnknownError{Cause: fmt.Errorf("client: invalid continuity checkpoint receipt")}
	}
	return &out, nil
}

// ContinuityContext reads progress without claiming or consuming it.
func (c *Client) ContinuityContext(ctx context.Context, in ContinuityContextInput) (*ContinuityContextResult, error) {
	var out ContinuityContextResult
	if err := c.doJSON(ctx, "POST", "/v1/continuity/context", in, &out, true); err != nil {
		return nil, err
	}
	if out.ProtocolVersion != 1 {
		return nil, fmt.Errorf("client: unsupported continuity response version")
	}
	return &out, nil
}

func (c *Client) continuityMutation(ctx context.Context, path string, in, out any) error {
	// Marshal before dispatch: a local encoding failure proves no request ran.
	if _, err := json.Marshal(in); err != nil {
		return fmt.Errorf("client: encode continuity input: %w", err)
	}
	if err := c.doJSON(ctx, "POST", path, in, out, true); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			// Proxies can emit HTTP failures after the origin committed. Only a
			// valid protocol rejection with the matching status proves rejection.
			var failure struct {
				ProtocolVersion int    `json:"protocol_version"`
				Code            string `json:"code"`
			}
			if json.Unmarshal([]byte(apiErr.Body), &failure) == nil && failure.ProtocolVersion == 1 {
				status := map[string]int{"validation": 400, "unavailable": 404, "conflict": 409, "unsupported": 501}
				if want, ok := status[failure.Code]; ok && want == apiErr.Status {
					return err
				}
			}
		}
		return &OutcomeUnknownError{Cause: err}
	}
	return nil
}

func receiptStatus(status string) bool { return status == "applied" || status == "replayed" }

func validContinuityID(id string) bool {
	return utf8.ValidString(id) && len(id) <= 256 && strings.TrimSpace(id) != ""
}

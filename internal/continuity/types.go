// Package continuity provides bounded, explicitly shared session progress.
// Checkpoints are reported work state, not knowledge or executable instructions.
package continuity

import (
	"context"
	"errors"
	"time"
)

// ProtocolVersion is the supported continuity wire protocol.
const ProtocolVersion = 1

// Input and output limits bound complete encoded objects, not only their bodies.
const (
	MaxRequestBytes      = 32 * 1024
	MaxPayloadBytes      = 16 * 1024
	DefaultResponseBytes = 32 * 1024
	MaxResponseBytes     = 64 * 1024
)

// Stable error classes are shared by all transports.
var (
	ErrValidation  = errors.New("continuity: validation")
	ErrUnavailable = errors.New("continuity: unavailable")
	ErrConflict    = errors.New("continuity: conflict")
	ErrUnsupported = errors.New("continuity: unsupported")
)

// Error exposes safe protocol metadata without including submitted content.
type Error struct {
	Code            string `json:"code"`
	Message         string `json:"message"`
	CurrentRevision *int64 `json:"current_revision,omitempty"`
	Cause           error  `json:"-"`
}

// Error returns safe diagnostic text without submitted content.
func (e *Error) Error() string { return "continuity: " + e.Message }

// Unwrap supports errors.Is for the protocol error classes.
func (e *Error) Unwrap() error { return e.Cause }

// ErrorCode returns the stable public category, including unclassified storage failures.
func ErrorCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	switch {
	case errors.Is(err, ErrValidation):
		return "validation"
	case errors.Is(err, ErrUnavailable):
		return "unavailable"
	case errors.Is(err, ErrConflict):
		return "conflict"
	case errors.Is(err, ErrUnsupported):
		return "unsupported"
	default:
		return "storage_failure"
	}
}

// WorkspaceState is sampled metadata, never proof of unchanged working files.
type WorkspaceState struct {
	Branch string `json:"branch"`
	HEAD   string `json:"head"`
	Known  bool   `json:"known"`
}

// Workspace is a verified local identity independent of the legacy project label.
type Workspace struct {
	Key   string         `json:"key"`
	Root  string         `json:"root"`
	State WorkspaceState `json:"state"`
}

// Resolver resolves roots and samples current repository metadata without writes.
type Resolver interface {
	Resolve(ctx context.Context, cwd, explicitRoot string) (Workspace, error)
}

// WorkspaceSampler samples repository metadata only when an operation needs it.
type WorkspaceSampler interface {
	Sample(ctx context.Context, root string) WorkspaceState
}

// OpenInput establishes a new independent session or confirms the same invocation.
type OpenInput struct {
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

// OpenResult confirms a committed session identity, independently of context delivery.
type OpenResult struct {
	ProtocolVersion int       `json:"protocol_version"`
	Status          string    `json:"status"`
	SessionID       string    `json:"session_id"`
	StartedAt       time.Time `json:"started_at"`
	AgentID         string    `json:"agent_id"`
	Project         string    `json:"project"`
	WorkspaceKey    string    `json:"workspace_key"`
	WorkspaceRoot   string    `json:"workspace_root"`
}

// Reference is a locator only; continuity never dereferences it.
type Reference struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Payload is the complete latest agent-authored checkpoint. Workspace is server sampled.
type Payload struct {
	Task       string         `json:"task"`
	State      string         `json:"state"`
	Summary    string         `json:"summary"`
	NextSteps  []string       `json:"next_steps,omitempty"`
	Blockers   []string       `json:"blockers,omitempty"`
	References []Reference    `json:"references,omitempty"`
	Workspace  WorkspaceState `json:"workspace,omitempty"`
}

// CheckpointInput saves or clears the exact owner's current session checkpoint.
type CheckpointInput struct {
	ProtocolVersion  int      `json:"protocol_version"`
	AgentID          string   `json:"agent_id,omitempty"`
	CWD              string   `json:"cwd,omitempty"`
	WorkspaceRoot    string   `json:"workspace_root,omitempty"`
	SessionID        string   `json:"session_id"`
	Action           string   `json:"action"`
	ExpectedRevision *int64   `json:"expected_revision"`
	RequestID        string   `json:"request_id"`
	Visibility       string   `json:"visibility,omitempty"`
	Payload          *Payload `json:"payload,omitempty"`
}

// CheckpointResult is emitted only after commit or verification of an existing commit.
type CheckpointResult struct {
	ProtocolVersion int       `json:"protocol_version"`
	Status          string    `json:"status"`
	SessionID       string    `json:"session_id"`
	Revision        int64     `json:"revision"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ContextInput reads candidates or an exact source in the current bound workspace.
type ContextInput struct {
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

// Source is a bounded, visible source from one consistent read snapshot.
type Source struct {
	SourceKind       string    `json:"source_kind"`
	SessionID        string    `json:"session_id"`
	Revision         int64     `json:"revision"`
	AgentID          string    `json:"agent_id"`
	HarnessKind      string    `json:"harness_kind"`
	RecordedAt       time.Time `json:"recorded_at"`
	State            string    `json:"state,omitempty"`
	SessionClosed    bool      `json:"session_closed"`
	Visibility       string    `json:"visibility"`
	Payload          *Payload  `json:"payload,omitempty"`
	Summary          string    `json:"summary,omitempty"`
	Stale            bool      `json:"stale"`
	WorkspaceChanged bool      `json:"workspace_changed"`
	WorkspaceUnknown bool      `json:"workspace_unknown"`
}

// Ref identifies an actually delivered source without implying consumption.
type Ref struct {
	SessionID  string `json:"session_id"`
	Revision   int64  `json:"revision"`
	SourceKind string `json:"source_kind"`
}

// ContextResult includes complete-envelope byte accounting and conservative ambiguity.
type ContextResult struct {
	ProtocolVersion int      `json:"protocol_version"`
	Status          string   `json:"status"`
	Source          *Source  `json:"source,omitempty"`
	Candidates      []Source `json:"candidates,omitempty"`
	Ambiguous       bool     `json:"ambiguous"`
	HasMore         bool     `json:"has_more"`
	OmittedForSize  int      `json:"omitted_for_size"`
	BytesReturned   int      `json:"bytes_returned"`
	MaxBytes        int      `json:"max_bytes"`
}

// OpenRecord is validated input for the atomic persistence boundary.
type OpenRecord struct {
	Input       OpenInput
	Workspace   Workspace
	RequestHash string
	SessionID   string
	Now         time.Time
}

// Mutation is validated checkpoint input and its stable normalized retry identity.
type Mutation struct {
	Input       CheckpointInput
	Workspace   Workspace
	RequestHash string
	Now         time.Time
	Sample      func(context.Context) WorkspaceState
}

// ReadScope selects one owned bound session and visible source records atomically.
type ReadScope struct {
	AgentID         string
	WorkspaceKey    string
	SessionID       string
	SourceSessionID string
	// Task filters the full candidate set exactly; explicit source reads ignore it.
	Task string
	// Limit is the normalized display window (1..20), not the match-count cap.
	Limit int
}

// Snapshot carries a bounded visible window and the bound root from one read transaction.
type Snapshot struct {
	Root    string
	Sources []Source
	// Matched counts the complete visible, task-matching set, including bodies
	// outside Sources. A display window must never manufacture uniqueness.
	Matched int64
}

// Store is the narrow persistence boundary consumed by the continuity service.
type Store interface {
	Open(ctx context.Context, in OpenRecord) (*OpenResult, error)
	Checkpoint(ctx context.Context, in Mutation) (*CheckpointResult, error)
	Read(ctx context.Context, in ReadScope) (*Snapshot, error)
}

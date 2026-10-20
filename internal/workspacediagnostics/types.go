// Package workspacediagnostics provides transient, metadata-only advice about
// recorded workspace identities. It never registers or merges a workspace.
package workspacediagnostics

import (
	"context"
	"fmt"
	"os"
	"time"
)

// Counts describes affected continuity metadata, not ordinary project memory.
type Counts struct {
	Sessions             int64 `json:"sessions"`
	OpenSessions         int64 `json:"open_sessions"`
	Checkpoints          int64 `json:"checkpoints"`
	ClearMarkers         int64 `json:"clear_markers"`
	OwnerCheckpoints     int64 `json:"owner_checkpoints"`
	WorkspaceCheckpoints int64 `json:"workspace_checkpoints"`
	OrphanSessions       int64 `json:"orphan_sessions"`
}

// Identity is one recorded root/key pair; the root alone is not a retry scope.
type Identity struct {
	Root string `json:"root"`
	Key  string `json:"workspace_key"`
}

// Metadata is the aggregate for one identity in a single database snapshot.
type Metadata struct {
	Identity
	Counts Counts `json:"counts"`
}

// RetryConflict counts colliding startup scopes without exposing their values.
type RetryConflict struct {
	Left  Identity `json:"left"`
	Right Identity `json:"right"`
	Count int64    `json:"count"`
}

// Snapshot contains only administrative metadata from one read transaction.
type Snapshot struct {
	Exists                bool
	TotalRoots            int
	Roots                 []Metadata
	RetryOverlap          []RetryConflict
	RetryOverlapTruncated bool
}

// Reader is the read-only metadata boundary consumed by the diagnostic service.
type Reader interface {
	Snapshot(ctx context.Context, maxRoots int) (Snapshot, error)
}

// UnsupportedSchemaError reports schema limitations without upgrading anything.
type UnsupportedSchemaError struct{ Reason string }

// Error returns an actionable, content-free schema limitation.
func (e *UnsupportedSchemaError) Error() string {
	return fmt.Sprintf("workspace diagnostics: unsupported schema: %s", e.Reason)
}

// PathState describes a local probe of an already recorded root.
type PathState struct {
	Status    string      `json:"status"` // live, missing, unknown
	Canonical string      `json:"canonical_root,omitempty"`
	Git       string      `json:"git"` // git, non_git, unknown
	GitRoot   string      `json:"git_root,omitempty"`
	Reason    string      `json:"reason,omitempty"`
	Info      os.FileInfo `json:"-"`
}

// Probe inspects local filesystem/Git metadata without crawling source files.
type Probe interface {
	Inspect(ctx context.Context, root string) PathState
}

// Config supplies explicit scan budgets and constructor-injected dependencies.
type Config struct {
	Reader        Reader
	Probe         Probe
	MaxRoots      int
	MaxCandidates int
	Timeout       time.Duration
}

// Root is a recorded identity and its current, potentially unknown path state.
type Root struct {
	Metadata
	Path PathState `json:"path"`
}

// Candidate distinguishes observed evidence from historical merge safety.
type Candidate struct {
	Left              Metadata `json:"left"`
	Right             Metadata `json:"right"`
	Classification    string   `json:"classification"`
	Evidence          []string `json:"evidence"`
	Blockers          []string `json:"blockers"`
	RetryOverlap      int64    `json:"retry_identity_overlaps"`
	RetryOverlapKnown bool     `json:"retry_overlap_known"`
	Consolidation     bool     `json:"consolidation_candidate"`
	MergeSafety       string   `json:"merge_safety"`
}

// Report is an advisory result. Partial coverage never means a clean scan.
type Report struct {
	ProtocolVersion int         `json:"protocol_version"`
	Status          string      `json:"status"`
	ReadOnly        bool        `json:"read_only"`
	Partial         bool        `json:"partial"`
	Reasons         []string    `json:"incomplete_reasons"`
	RootCountKnown  bool        `json:"root_count_known"`
	TotalRoots      int         `json:"total_roots"`
	ScannedRoots    int         `json:"scanned_roots"`
	ComparedPairs   int64       `json:"compared_pairs"`
	PossiblePairs   int64       `json:"possible_pairs"`
	MaxRoots        int         `json:"max_roots"`
	MaxCandidates   int         `json:"max_candidates"`
	Roots           []Root      `json:"roots"`
	Candidates      []Candidate `json:"candidates"`
}

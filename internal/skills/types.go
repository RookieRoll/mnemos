// Package skills holds procedural memory: reusable step-by-step procedures
// the agent has learned work. Skills are explicitly saved by the agent
// (or promoted by consolidation), versioned, and tracked for effectiveness.
package skills

import (
	"context"
	"errors"
	"time"

	"github.com/polyxmedia/mnemos/internal/maintenance"
)

// ErrNotFound is returned when a skill lookup misses.
var ErrNotFound = errors.New("skill not found")

// ErrMergeConflict reports that a merge's selection went stale: a row it meant
// to write was changed or removed by another writer between the selection and
// the transactional write. Wrapping it is how the store says "nothing was
// written, re-select and retry".
var ErrMergeConflict = errors.New("skill merge conflict")

// Skill is one reusable procedure.
type Skill struct {
	ID             string
	AgentID        string
	Name           string
	Description    string
	Procedure      string
	Pitfalls       string
	Tags           []string
	SourceSessions []string
	UseCount       int
	SuccessCount   int
	Effectiveness  float64
	Version        int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// SaveInput is the agent-provided payload for skill_save. If a skill with the
// same (agent_id, name) exists, it is versioned up, not duplicated.
type SaveInput struct {
	AgentID        string
	Name           string
	Description    string
	Procedure      string
	Pitfalls       string
	Tags           []string
	SourceSessions []string
}

// MatchInput is the payload for skill_match.
type MatchInput struct {
	AgentID string
	Query   string
	Tags    []string
	Limit   int
}

// Match is a ranked skill lookup.
type Match struct {
	Skill Skill
	Score float64
}

// FeedbackInput records whether a skill worked. Updates effectiveness.
type FeedbackInput struct {
	ID      string
	Success bool
}

// Scope narrows a skill maintenance selection. An empty AgentID matches every
// agent and an empty Project matches every project. Project is matched against
// the "project:<name>" tag the promotion path writes, because the skills table
// has no project column: a skill that carries no project tag has no project
// attribution, so a project filter never selects it.
type Scope struct {
	AgentID string
	Project string
}

// DeleteInput selects the skills Delete removes. Name is matched exactly
// (surrounding whitespace ignored); Scope narrows by agent and project. DryRun
// runs the same selection and writes nothing, which is what --dry-run previews.
type DeleteInput struct {
	Name   string
	Scope  Scope
	DryRun bool
}

// DeleteResult carries the exact selection Delete worked on. Rows is the same
// slice a DryRun preview returns, so the rows a caller prints are always the
// rows a real run writes.
type DeleteResult struct {
	Rows   []Skill
	DryRun bool
}

// MergeInput selects the skills Merge folds together. KeepVersion names the
// version to keep (zero keeps the highest version); a negative value is rejected
// before any selection runs, and an unmatched version is an error — including
// when the name itself matches nothing, so a caller that asked for a specific
// version never gets a silent success. DryRun returns the same keeper and
// superseded rows and writes nothing.
type MergeInput struct {
	Name        string
	Scope       Scope
	KeepVersion int
	DryRun      bool
}

// MergeResult reports the keeper and the rows absorbed into it. Keeper is the
// post-merge row (nil when the name matched nothing) and Superseded lists the
// rows that were folded in and removed. Both are populated for a DryRun
// preview as well, where nothing is written.
type MergeResult struct {
	Keeper     *Skill
	Superseded []Skill
	DryRun     bool
}

// ScoreReport bundles the components of a skill's measured quality. The
// composite Score is a single 0-1 number callers can rank by; the
// individual fields are exposed so callers building dashboards or
// "skills ranked by lift" registries can choose their own weighting.
//
// AdjustedEffectiveness applies Bayesian shrinkage toward the 0.5 prior
// when UseCount is small — a skill that succeeded once out of one
// invocation should not outrank one that succeeded 9 times out of 10.
//
// Recency is a [0,1] factor that decays linearly from 1 at the moment
// of last use to 0 after stalenessHorizon days. Forgotten skills lose
// score even if their effectiveness was high in their day.
type ScoreReport struct {
	SkillID               string
	Effectiveness         float64 // raw success_count / use_count, 0 if unused
	UseCount              int
	SuccessCount          int
	AdjustedEffectiveness float64 // Bayesian-shrunk against 0.5 prior
	Recency               float64 // 1.0 = used today, 0.0 = stale
	Score                 float64 // composite 0-1, the headline number
	UpdatedAt             time.Time
}

// Store persists skills.
type Store interface {
	Upsert(ctx context.Context, in SaveInput) (*Skill, error)
	// Restore inserts a skill verbatim (preserving ID, version, effectiveness,
	// counts, timestamps), skipping on an existing ID or UNIQUE(agent, name).
	// The fidelity-import counterpart to Upsert, which version-bumps. Returns
	// true when a row was written.
	Restore(ctx context.Context, sk *Skill) (bool, error)
	Get(ctx context.Context, id string) (*Skill, error)
	Match(ctx context.Context, in MatchInput) ([]Match, error)
	List(ctx context.Context, agentID string) ([]Skill, error)
	RecordUse(ctx context.Context, in FeedbackInput) error
	// Delete removes one skill by ID. Writes are funnelled through the store so
	// the derived full-text mirror stays in step with the table: the database
	// triggers on skills keep the two consistent on every INSERT, UPDATE, and
	// DELETE, and callers never touch the index themselves.
	Delete(ctx context.Context, id string) error
	Count(ctx context.Context) (int64, error)
}

// FencedStore adds dream-only writes that commit under an active maintenance
// owner, cycle, and unexpired DB-clock lease.
type FencedStore interface {
	UpsertFenced(ctx context.Context, fence maintenance.Fence, in SaveInput) (*Skill, error)
}

// SkillRef names one selected skill and the version the selection observed. A
// merge only touches rows it can still find at exactly that version, which is
// what turns a concurrent write into a reported conflict instead of a silent
// overwrite.
type SkillRef struct {
	ID      string
	Version int
}

// MergeWriteInput is the transactional payload behind Service.Merge. The service
// does the selection and the content folding — both pure functions of the rows it
// read — and the store applies the two halves of the merge, the keeper rewrite
// and the superseded-row removal, inside one transaction.
//
// KeeperVersion is the keeper's version when the rows were selected: a row that
// moved on aborts the merge with ErrMergeConflict rather than overwriting the
// concurrent write. Losers carries the same expectation per removed row.
//
// A payload whose content equals what the keeper already holds removes the
// superseded rows without bumping the version, so a repeated merge neither
// re-versions the keeper nor accumulates duplicate text.
type MergeWriteInput struct {
	KeeperID       string
	KeeperVersion  int
	Description    string
	Procedure      string
	Pitfalls       string
	Tags           []string
	SourceSessions []string
	Losers         []SkillRef
}

// MergingStore is the store surface Merge needs: the keeper rewrite and the
// superseded-row deletes commit together or not at all, and both are guarded by
// the versions the selection observed. A store without it cannot promise atomic
// or conflict-aware merging, so Merge refuses instead of degrading to a
// multi-step write that a failure or a second writer can corrupt.
type MergingStore interface {
	Merge(ctx context.Context, in MergeWriteInput) (*Skill, error)
}

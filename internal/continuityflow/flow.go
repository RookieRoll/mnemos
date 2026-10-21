// Package continuityflow composes task-session receipts with best-effort
// context delivery and maintenance. It never creates a second session when
// context delivery is unavailable.
package continuityflow

import (
	"context"
	"fmt"
	"time"

	"github.com/polyxmedia/mnemos/internal/contextfmt"
	"github.com/polyxmedia/mnemos/internal/continuity"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
)

// Core is the continuity domain boundary consumed by startup composition.
type Core interface {
	Open(context.Context, continuity.OpenInput) (*continuity.OpenResult, error)
	Checkpoint(context.Context, continuity.CheckpointInput) (*continuity.CheckpointResult, error)
	Context(context.Context, continuity.ContextInput) (*continuity.ContextResult, error)
}

// LegacySessions is the legacy lifecycle boundary required by task starts.
// Implementations keep these sessions distinct from continuity-bound records.
type LegacySessions interface {
	Open(context.Context, session.OpenInput) (*session.Session, error)
	Get(context.Context, string) (*session.Session, error)
	Resolve(context.Context, string, string) (*session.Session, error)
	ListLegacyOpen(context.Context, string) ([]session.Session, error)
	ListLegacyOpenByHost(context.Context, string) ([]session.Session, error)
	Close(context.Context, session.CloseInput) error
	CloseForHost(context.Context, string, string, session.CloseInput) (int, error)
	SetGoalIfEmpty(context.Context, string, string) error
}

// Composer builds context for an exact existing session without opening one.
type Composer interface {
	Build(context.Context, prewarm.Request) (*prewarm.Block, error)
}

// Maintenance is the best-effort opportunity check run after a task-session
// receipt commits. Implementations own their configured timeout and due gate.
type Maintenance interface {
	MaybeRun(context.Context) error
}

// Config injects the domain services used by the shared lifecycle entry point.
type Config struct {
	Core           Core
	LegacySessions LegacySessions
	Composer       Composer
	Maintenance    Maintenance
	// PrewarmMaxTokens bounds startup context including the v1 session identity.
	// Zero preserves the historical 500-token default.
	PrewarmMaxTokens int
}

// Service coordinates legacy or v1 receipts, context composition, and
// best-effort maintenance after an effective task-session start.
type Service struct {
	core             Core
	legacySessions   LegacySessions
	composer         Composer
	maintenance      Maintenance
	prewarmMaxTokens int
}

// New constructs an explicitly wired task-session facade.
func New(cfg Config) *Service {
	if cfg.PrewarmMaxTokens <= 0 {
		cfg.PrewarmMaxTokens = 500
	}
	return &Service{
		core: cfg.Core, legacySessions: cfg.LegacySessions, composer: cfg.Composer,
		maintenance: cfg.Maintenance, prewarmMaxTokens: cfg.PrewarmMaxTokens,
	}
}

// SupportsContinuity reports whether the facade has the explicit v1 progress
// service wired. Legacy-only adapters must not advertise the v1 protocol.
func (s *Service) SupportsContinuity() bool { return s != nil && s.core != nil }

// TaskSessionInput is the normalized application input for one effective
// task-session start. Continuity selects the v1 receipt path; otherwise the
// existing legacy lifecycle is used. Knowledge labels stay independent from
// continuity owner/workspace/session identity.
type TaskSessionInput struct {
	AgentID       string
	Project       string
	Goal          string
	HostSessionID string
	// Continuity selects the validated v1 owner/workspace/session protocol.
	Continuity *continuity.OpenInput
	// AdoptLegacy preserves the host-hook's existing legacy session adoption
	// policy. It is ignored for v1 and never infers a continuity workspace.
	AdoptLegacy bool
	// ExistingLegacySessionID reuses a known legacy receipt for a session-start
	// event. Recovery and ordinary context calls must use Compose instead.
	ExistingLegacySessionID string
}

// TaskSessionReceipt carries the underlying committed lifecycle receipt.
type TaskSessionReceipt struct {
	LegacySession *session.Session
	Continuity    *continuity.OpenResult
}

// TaskSessionResult preserves both lifecycle receipts and their composed
// context without imposing any transport-specific response envelope.
type TaskSessionResult struct {
	Receipt        TaskSessionReceipt
	ContextStatus  string
	Prewarm        string
	ContinuityRefs []continuity.Ref
	LegacyBlock    *prewarm.Block
}

// Start commits or reuses a task-session receipt, composes context for that
// receipt, and then makes a best-effort maintenance opportunity check. A
// composition or maintenance failure never removes the committed receipt.
func (s *Service) Start(ctx context.Context, in TaskSessionInput) (*TaskSessionResult, error) {
	if in.Continuity != nil {
		if in.AdoptLegacy || in.ExistingLegacySessionID != "" {
			return nil, fmt.Errorf("continuityflow: legacy reuse cannot accompany a v1 start")
		}
		v1 := *in.Continuity
		if in.AgentID != "" {
			v1.AgentID = in.AgentID
		}
		if in.Project != "" {
			v1.Project = in.Project
		}
		if in.Goal != "" {
			v1.Goal = in.Goal
		}
		out, err := s.openContinuity(ctx, v1)
		if err != nil {
			return nil, err
		}
		return &TaskSessionResult{
			Receipt:        TaskSessionReceipt{Continuity: &out.OpenResult},
			ContextStatus:  out.ContextStatus,
			Prewarm:        out.Prewarm,
			ContinuityRefs: out.ContinuityRefs,
		}, nil
	}

	if s.legacySessions == nil {
		return nil, fmt.Errorf("continuityflow: legacy session service unavailable")
	}
	var sess *session.Session
	var err error
	switch {
	case in.ExistingLegacySessionID != "":
		sess, err = s.legacySessions.Get(ctx, in.ExistingLegacySessionID)
		if err == nil && (sess == nil || sess.ContinuityBound || sess.EndedAt != nil) {
			err = fmt.Errorf("continuityflow: legacy session receipt unavailable")
		}
	case in.AdoptLegacy:
		sess, err = s.adoptLegacy(ctx, in.HostSessionID, in.Project)
		// Adoption is best-effort: the legacy hook previously opened a new
		// session when an adoption lookup failed.
		if err != nil {
			err = nil
		}
		if sess == nil {
			sess, err = s.legacySessions.Open(ctx, session.OpenInput{
				AgentID: in.AgentID, Project: in.Project, Goal: in.Goal, HostSessionID: in.HostSessionID,
			})
		}
	default:
		sess, err = s.legacySessions.Open(ctx, session.OpenInput{
			AgentID: in.AgentID, Project: in.Project, Goal: in.Goal, HostSessionID: in.HostSessionID,
		})
	}
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, fmt.Errorf("continuityflow: legacy session receipt unavailable")
	}

	out := &TaskSessionResult{
		Receipt:       TaskSessionReceipt{LegacySession: sess},
		ContextStatus: "unavailable",
	}
	if s.composer != nil {
		block, buildErr := s.composer.Build(ctx, prewarm.Request{
			Mode: prewarm.ModeSessionStart, AgentID: in.AgentID, Project: in.Project,
			Goal: in.Goal, SessionID: sess.ID,
		})
		if buildErr == nil && block != nil {
			out.LegacyBlock = block
			out.Prewarm = block.Text
			out.ContextStatus = "available"
		}
	}
	s.runMaintenance(ctx)
	return out, nil
}

// Open preserves the explicit v1 continuity response while sharing the same
// receipt, composition, and maintenance path as Start.
func (s *Service) Open(ctx context.Context, in continuity.OpenInput) (*OpenResult, error) {
	out, err := s.Start(ctx, TaskSessionInput{Continuity: &in})
	if err != nil {
		return nil, err
	}
	if out.Receipt.Continuity == nil {
		return nil, fmt.Errorf("continuityflow: v1 receipt unavailable")
	}
	return &OpenResult{
		OpenResult: *out.Receipt.Continuity, ContextStatus: out.ContextStatus,
		Prewarm: out.Prewarm, ContinuityRefs: out.ContinuityRefs,
	}, nil
}

// OpenResult preserves the committed v1 receipt even when context is unavailable.
type OpenResult struct {
	continuity.OpenResult
	ContextStatus  string           `json:"context_status"`
	Prewarm        string           `json:"prewarm"`
	ContinuityRefs []continuity.Ref `json:"continuity_refs,omitempty"`
}

// openContinuity commits identity first, then composes for precisely that
// session and owner. Maintenance runs only after this effective task start.
func (s *Service) openContinuity(ctx context.Context, in continuity.OpenInput) (*OpenResult, error) {
	if s.core == nil {
		return nil, continuity.ErrUnsupported
	}
	receipt, err := s.core.Open(ctx, in)
	if err != nil {
		return nil, err
	}
	// Startup hooks deliver only the context string, so make the current write
	// target explicit and distinct from a recovery source. Account for this
	// framing before asking the composer to spend the remaining configured budget.
	identity := fmt.Sprintf("Current Mnemos session (checkpoint target, distinct from recovery source):\nmnemos_session_id: %s", receipt.SessionID)
	out := &OpenResult{OpenResult: *receipt, ContextStatus: "unavailable", Prewarm: identity}
	if s.composer != nil {
		progress, readErr := s.core.Context(ctx, continuity.ContextInput{
			ProtocolVersion: continuity.ProtocolVersion, AgentID: receipt.AgentID,
			CWD: receipt.WorkspaceRoot, WorkspaceRoot: receipt.WorkspaceRoot,
			SessionID: receipt.SessionID, SourceSessionID: in.SourceSessionID,
			Task: in.Task, ResumeIntent: in.ResumeIntent, Limit: in.Limit, MaxBytes: in.MaxBytes,
		})
		if readErr == nil {
			block, buildErr := s.composer.Build(ctx, prewarm.Request{
				Mode: prewarm.ModeSessionStart, AgentID: receipt.AgentID, Project: receipt.Project,
				Goal: in.Goal, SessionID: receipt.SessionID, Continuity: progress,
				MaxTokens: max(0, s.prewarmMaxTokens-contextfmt.Estimate(identity+"\n\n")),
			})
			if buildErr == nil && block != nil {
				out.ContextStatus = "available"
				if block.Text != "" {
					out.Prewarm += "\n\n" + block.Text
				}
				out.ContinuityRefs = block.ContinuityRefs
			}
		}
	}
	s.runMaintenance(ctx)
	return out, nil
}

// Compose builds context for a caller that already has a lifecycle receipt.
// It is the recovery/read-only path and never invokes opportunistic maintenance.
func (s *Service) Compose(ctx context.Context, req prewarm.Request) (*prewarm.Block, error) {
	if s.composer == nil {
		return nil, nil
	}
	return s.composer.Build(ctx, req)
}

// End closes a legacy session without starting a maintenance opportunity.
func (s *Service) End(ctx context.Context, in session.CloseInput) error {
	if s.legacySessions == nil {
		return fmt.Errorf("continuityflow: legacy session service unavailable")
	}
	return s.legacySessions.Close(ctx, in)
}

// EndForHost closes every eligible legacy session owned by one host
// conversation, preserving the legacy hook's project scoping behavior.
func (s *Service) EndForHost(ctx context.Context, hostSessionID, project string, in session.CloseInput) (int, error) {
	if s.legacySessions == nil {
		return 0, fmt.Errorf("continuityflow: legacy session service unavailable")
	}
	return s.legacySessions.CloseForHost(ctx, hostSessionID, project, in)
}

// ResolveLegacy resolves an existing hook session without opening a session
// or triggering opportunistic maintenance.
func (s *Service) ResolveLegacy(ctx context.Context, hostSessionID, project string) (*session.Session, error) {
	if s.legacySessions == nil {
		return nil, fmt.Errorf("continuityflow: legacy session service unavailable")
	}
	return s.legacySessions.Resolve(ctx, hostSessionID, project)
}

// GetLegacy loads a known legacy receipt without opening a task session.
func (s *Service) GetLegacy(ctx context.Context, sessionID string) (*session.Session, error) {
	if s.legacySessions == nil {
		return nil, fmt.Errorf("continuityflow: legacy session service unavailable")
	}
	return s.legacySessions.Get(ctx, sessionID)
}

// SetGoalIfEmpty records the first real prompt on a legacy prewarmed session.
func (s *Service) SetGoalIfEmpty(ctx context.Context, sessionID, goal string) error {
	if s.legacySessions == nil {
		return fmt.Errorf("continuityflow: legacy session service unavailable")
	}
	return s.legacySessions.SetGoalIfEmpty(ctx, sessionID, goal)
}

// Checkpoint delegates exact, versioned checkpoint mutations to the domain service.
func (s *Service) Checkpoint(ctx context.Context, in continuity.CheckpointInput) (*continuity.CheckpointResult, error) {
	if s.core == nil {
		return nil, continuity.ErrUnsupported
	}
	return s.core.Checkpoint(ctx, in)
}

// Context delegates scoped, read-only recovery to the domain service.
func (s *Service) Context(ctx context.Context, in continuity.ContextInput) (*continuity.ContextResult, error) {
	if s.core == nil {
		return nil, continuity.ErrUnsupported
	}
	return s.core.Context(ctx, in)
}

func (s *Service) runMaintenance(ctx context.Context) {
	if s.maintenance != nil {
		_ = s.maintenance.MaybeRun(ctx)
	}
}

func (s *Service) adoptLegacy(ctx context.Context, hostSessionID, project string) (*session.Session, error) {
	if hostSessionID != "" {
		open, err := s.legacySessions.ListLegacyOpenByHost(ctx, hostSessionID)
		if err != nil || len(open) == 0 {
			return nil, err
		}
		return &open[0], nil
	}
	if project == "" {
		return nil, nil
	}
	open, err := s.legacySessions.ListLegacyOpen(ctx, project)
	if err != nil || len(open) == 0 {
		return nil, err
	}
	newest := open[0]
	if time.Since(newest.StartedAt) > legacyAdoptWindow {
		return nil, nil
	}
	return &newest, nil
}

const legacyAdoptWindow = 90 * time.Second

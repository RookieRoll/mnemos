// Package continuityflow composes context only after a continuity session commits.
// It never creates a second session when context delivery is unavailable.
package continuityflow

import (
	"context"
	"fmt"

	"github.com/polyxmedia/mnemos/internal/continuity"
	"github.com/polyxmedia/mnemos/internal/prewarm"
)

// Core is the continuity domain boundary consumed by startup composition.
type Core interface {
	Open(context.Context, continuity.OpenInput) (*continuity.OpenResult, error)
	Checkpoint(context.Context, continuity.CheckpointInput) (*continuity.CheckpointResult, error)
	Context(context.Context, continuity.ContextInput) (*continuity.ContextResult, error)
}

// Composer builds context for an exact existing session without opening one.
type Composer interface {
	Build(context.Context, prewarm.Request) (*prewarm.Block, error)
}

// Config injects the domain service and optional context composer.
type Config struct {
	Core     Core
	Composer Composer
}

// Service coordinates committed identity and best-effort context delivery.
type Service struct {
	core     Core
	composer Composer
}

// New constructs an explicitly wired continuity facade.
func New(cfg Config) *Service { return &Service{core: cfg.Core, composer: cfg.Composer} }

// OpenResult preserves the committed receipt even when context is unavailable.
type OpenResult struct {
	continuity.OpenResult
	ContextStatus  string           `json:"context_status"`
	Prewarm        string           `json:"prewarm"`
	ContinuityRefs []continuity.Ref `json:"continuity_refs,omitempty"`
}

// Open commits identity first, then composes for precisely that session and owner.
func (s *Service) Open(ctx context.Context, in continuity.OpenInput) (*OpenResult, error) {
	receipt, err := s.core.Open(ctx, in)
	if err != nil {
		return nil, err
	}
	// Startup hooks deliver only the context string, so make the current write
	// target explicit and distinct from a recovery source. Account for this
	// framing before asking the composer to spend the remaining 500-token budget.
	identity := fmt.Sprintf("Current Mnemos session (checkpoint target, distinct from recovery source):\nmnemos_session_id: %s", receipt.SessionID)
	out := &OpenResult{OpenResult: *receipt, ContextStatus: "unavailable", Prewarm: identity}
	if s.composer == nil {
		return out, nil
	}
	progress, err := s.core.Context(ctx, continuity.ContextInput{
		ProtocolVersion: continuity.ProtocolVersion, AgentID: receipt.AgentID,
		CWD: receipt.WorkspaceRoot, WorkspaceRoot: receipt.WorkspaceRoot,
		SessionID: receipt.SessionID, SourceSessionID: in.SourceSessionID,
		Task: in.Task, ResumeIntent: in.ResumeIntent, Limit: in.Limit, MaxBytes: in.MaxBytes,
	})
	if err != nil {
		return out, nil
	}
	block, err := s.composer.Build(ctx, prewarm.Request{
		Mode: prewarm.ModeSessionStart, AgentID: receipt.AgentID, Project: receipt.Project,
		Goal: in.Goal, SessionID: receipt.SessionID, Continuity: progress,
		MaxTokens: 500 - (len(identity)+2+3)/4,
	})
	if err != nil || block == nil {
		return out, nil
	}
	out.ContextStatus = "available"
	if block.Text != "" {
		out.Prewarm += "\n\n" + block.Text
	}
	out.ContinuityRefs = block.ContinuityRefs
	return out, nil
}

// Checkpoint delegates exact, versioned checkpoint mutations to the domain service.
func (s *Service) Checkpoint(ctx context.Context, in continuity.CheckpointInput) (*continuity.CheckpointResult, error) {
	return s.core.Checkpoint(ctx, in)
}

// Context delegates scoped, read-only recovery to the domain service.
func (s *Service) Context(ctx context.Context, in continuity.ContextInput) (*continuity.ContextResult, error) {
	return s.core.Context(ctx, in)
}

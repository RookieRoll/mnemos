package continuityflow

import (
	"context"
	"testing"

	"github.com/polyxmedia/mnemos/internal/contextfmt"
	"github.com/polyxmedia/mnemos/internal/continuity"
	"github.com/polyxmedia/mnemos/internal/prewarm"
)

type budgetCore struct{}

func (budgetCore) Open(context.Context, continuity.OpenInput) (*continuity.OpenResult, error) {
	return &continuity.OpenResult{SessionID: "session-id", AgentID: "agent", Project: "repo", WorkspaceRoot: "/repo"}, nil
}
func (budgetCore) Checkpoint(context.Context, continuity.CheckpointInput) (*continuity.CheckpointResult, error) {
	return nil, nil
}
func (budgetCore) Context(context.Context, continuity.ContextInput) (*continuity.ContextResult, error) {
	return &continuity.ContextResult{Status: "empty"}, nil
}

type budgetComposer struct{ got int }

func (c *budgetComposer) Build(_ context.Context, in prewarm.Request) (*prewarm.Block, error) {
	c.got = in.MaxTokens
	return &prewarm.Block{Text: "context"}, nil
}

func TestOpenPassesConfiguredBudgetAfterIdentityFraming(t *testing.T) {
	composer := &budgetComposer{}
	const configured = 360
	flow := New(Config{Core: budgetCore{}, Composer: composer, PrewarmMaxTokens: configured})
	result, err := flow.Open(context.Background(), continuity.OpenInput{})
	if err != nil {
		t.Fatal(err)
	}
	identity := "Current Mnemos session (checkpoint target, distinct from recovery source):\nmnemos_session_id: session-id\n\n"
	want := configured - contextfmt.Estimate(identity)
	if composer.got != want {
		t.Fatalf("composer budget = %d, want %d", composer.got, want)
	}
	if result.ContextStatus != "available" {
		t.Fatalf("context status = %q", result.ContextStatus)
	}
}

func TestOpenDefaultsPrewarmBudgetToFiveHundred(t *testing.T) {
	composer := &budgetComposer{}
	flow := New(Config{Core: budgetCore{}, Composer: composer})
	if _, err := flow.Open(context.Background(), continuity.OpenInput{}); err != nil {
		t.Fatal(err)
	}
	if composer.got != 500-contextfmt.Estimate("Current Mnemos session (checkpoint target, distinct from recovery source):\nmnemos_session_id: session-id\n\n") {
		t.Fatalf("default budget = %d", composer.got)
	}
}

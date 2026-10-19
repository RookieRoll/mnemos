package continuityflow_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/continuity"
	"github.com/polyxmedia/mnemos/internal/continuityflow"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/storage"
)

type failingComposer struct{ requests []prewarm.Request }

func (f *failingComposer) Build(_ context.Context, req prewarm.Request) (*prewarm.Block, error) {
	f.requests = append(f.requests, req)
	return nil, errors.New("injected context failure")
}

func TestCommittedIdentitySurvivesContextFailureAndRetry(t *testing.T) {
	ctx := t.Context()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := t.TempDir()
	composer := &failingComposer{}
	flow := continuityflow.New(continuityflow.Config{Core: continuity.New(continuity.Config{Store: db.Continuity()}), Composer: composer})
	in := continuity.OpenInput{ProtocolVersion: 1, CWD: root, WorkspaceRoot: root, HarnessKind: "test", StartKey: "one", AgentID: "a"}
	first, err := flow.Open(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := flow.Open(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.SessionID != second.SessionID || first.Status != "applied" || second.Status != "replayed" || first.ContextStatus != "unavailable" || !strings.Contains(first.Prewarm, first.SessionID) {
		t.Fatalf("identity lost: %#v %#v", first, second)
	}
	if len(composer.requests) != 2 || composer.requests[0].SessionID != first.SessionID || composer.requests[0].Continuity == nil || composer.requests[0].AgentID != "a" {
		t.Fatal("composer not scoped to exact committed session")
	}
	var count int
	if err := db.SQL().QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&count); err != nil || count != 1 {
		t.Fatalf("opened duplicate sessions: %d %v", count, err)
	}
}

func TestSuccessfulCompositionKeepsCurrentTargetWithinBudget(t *testing.T) {
	ctx := t.Context()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := t.TempDir()
	core := continuity.New(continuity.Config{Store: db.Continuity()})
	pw := prewarm.NewService(prewarm.Config{Observations: db.Observations(), Sessions: db.Sessions(), Skills: db.Skills(), Touches: db.Touches()})
	flow := continuityflow.New(continuityflow.Config{Core: core, Composer: pw})
	out, err := flow.Open(ctx, continuity.OpenInput{ProtocolVersion: 1, CWD: root, WorkspaceRoot: root, HarnessKind: "test", StartKey: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if out.ContextStatus != "available" || !strings.Contains(out.Prewarm, "mnemos_session_id: "+out.SessionID) || (len(out.Prewarm)+3)/4 > 500 {
		t.Fatalf("invalid delivery: %#v", out)
	}
}

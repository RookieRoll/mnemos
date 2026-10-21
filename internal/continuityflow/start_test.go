package continuityflow_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/continuity"
	"github.com/polyxmedia/mnemos/internal/continuityflow"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
)

type captureCore struct {
	opened  continuity.OpenInput
	read    continuity.ContextInput
	openedN int
	readErr error
}

func (c *captureCore) Open(_ context.Context, in continuity.OpenInput) (*continuity.OpenResult, error) {
	c.opened, c.openedN = in, c.openedN+1
	return &continuity.OpenResult{
		ProtocolVersion: continuity.ProtocolVersion, Status: "applied", SessionID: "session-1",
		AgentID: in.AgentID, Project: in.Project, WorkspaceKey: "workspace-key", WorkspaceRoot: in.WorkspaceRoot,
	}, nil
}
func (c *captureCore) Checkpoint(context.Context, continuity.CheckpointInput) (*continuity.CheckpointResult, error) {
	return &continuity.CheckpointResult{Status: "applied"}, nil
}
func (c *captureCore) Context(_ context.Context, in continuity.ContextInput) (*continuity.ContextResult, error) {
	c.read = in
	if c.readErr != nil {
		return nil, c.readErr
	}
	return &continuity.ContextResult{Status: "empty"}, nil
}

type captureComposer struct {
	requests []prewarm.Request
	err      error
}

func (c *captureComposer) Build(_ context.Context, req prewarm.Request) (*prewarm.Block, error) {
	c.requests = append(c.requests, req)
	if c.err != nil {
		return nil, c.err
	}
	return &prewarm.Block{Text: "context"}, nil
}

type captureMaintenance struct {
	calls int
	err   error
}

func (m *captureMaintenance) MaybeRun(context.Context) error {
	m.calls++
	return m.err
}

type fakeLegacySessions struct {
	opened           *session.Session
	openErr          error
	byHost           []session.Session
	byProject        []session.Session
	byHostErr        error
	byProjectErr     error
	get              *session.Session
	closeCalls       int
	closeForHostCall int
	setGoalCalls     int
}

func (f *fakeLegacySessions) Open(context.Context, session.OpenInput) (*session.Session, error) {
	return f.opened, f.openErr
}
func (f *fakeLegacySessions) Get(context.Context, string) (*session.Session, error) {
	return f.get, nil
}
func (f *fakeLegacySessions) Resolve(context.Context, string, string) (*session.Session, error) {
	return f.get, nil
}
func (f *fakeLegacySessions) ListLegacyOpen(context.Context, string) ([]session.Session, error) {
	return f.byProject, f.byProjectErr
}
func (f *fakeLegacySessions) ListLegacyOpenByHost(context.Context, string) ([]session.Session, error) {
	return f.byHost, f.byHostErr
}
func (f *fakeLegacySessions) Close(context.Context, session.CloseInput) error {
	f.closeCalls++
	return nil
}
func (f *fakeLegacySessions) CloseForHost(context.Context, string, string, session.CloseInput) (int, error) {
	f.closeForHostCall++
	return 1, nil
}
func (f *fakeLegacySessions) SetGoalIfEmpty(context.Context, string, string) error {
	f.setGoalCalls++
	return nil
}

func TestStartUsesCommittedV1ReceiptAndBestEffortMaintenance(t *testing.T) {
	core := &captureCore{}
	composer := &captureComposer{}
	maintenance := &captureMaintenance{err: errors.New("expired opportunity budget")}
	flow := continuityflow.New(continuityflow.Config{Core: core, Composer: composer, Maintenance: maintenance})
	root := t.TempDir()
	in := continuity.OpenInput{ProtocolVersion: 1, CWD: root, WorkspaceRoot: root, HarnessKind: "test", StartKey: "start", AgentID: "outer-agent", Project: "outer-label", Goal: "outer goal"}
	out, err := flow.Start(t.Context(), continuityflow.TaskSessionInput{
		AgentID: "outer-agent", Project: "outer-label", Goal: "outer goal", Continuity: &in,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Receipt.Continuity == nil || out.Receipt.Continuity.SessionID != "session-1" || out.ContextStatus != "available" {
		t.Fatalf("receipt/context lost: %#v", out)
	}
	if core.openedN != 1 || core.read.SessionID != "session-1" || core.read.WorkspaceRoot != root || core.read.AgentID != "outer-agent" {
		t.Fatalf("context was not bound to committed receipt: open=%#v read=%#v", core.opened, core.read)
	}
	if maintenance.calls != 1 {
		t.Fatalf("maintenance calls = %d, want 1", maintenance.calls)
	}

	// Open is a wire-compatible wrapper over the same flow path.
	maintenance.calls = 0
	wrapper, err := flow.Open(t.Context(), in)
	if err != nil || wrapper.SessionID != "session-1" || maintenance.calls != 1 {
		t.Fatalf("Open wrapper result=%#v calls=%d err=%v", wrapper, maintenance.calls, err)
	}
}

func TestLegacyAdoptionLookupFailureFallsBackToOpen(t *testing.T) {
	store := &fakeLegacySessions{
		opened:       &session.Session{ID: "fresh", Project: "same-label"},
		byHostErr:    errors.New("host lookup failed"),
		byProjectErr: errors.New("project lookup failed"),
	}
	composer := &captureComposer{}
	maintenance := &captureMaintenance{}
	flow := continuityflow.New(continuityflow.Config{LegacySessions: store, Composer: composer, Maintenance: maintenance})
	out, err := flow.Start(t.Context(), continuityflow.TaskSessionInput{
		Project: "same-label", HostSessionID: "host-id", AdoptLegacy: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Receipt.LegacySession == nil || out.Receipt.LegacySession.ID != "fresh" || len(composer.requests) != 1 || composer.requests[0].SessionID != "fresh" {
		t.Fatalf("legacy fallback did not open/compose fresh receipt: %#v %#v", out, composer.requests)
	}
	if maintenance.calls != 1 {
		t.Fatalf("maintenance calls = %d, want 1", maintenance.calls)
	}
}

func TestComposeAndLegacyLifecycleDoNotRunMaintenance(t *testing.T) {
	store := &fakeLegacySessions{get: &session.Session{ID: "known", Project: "p"}}
	composer := &captureComposer{}
	maintenance := &captureMaintenance{}
	core := &captureCore{}
	flow := continuityflow.New(continuityflow.Config{Core: core, LegacySessions: store, Composer: composer, Maintenance: maintenance})
	if _, err := flow.Compose(t.Context(), prewarm.Request{Mode: prewarm.ModeCompactionRecovery, SessionID: "known"}); err != nil {
		t.Fatal(err)
	}
	if _, err := flow.Context(t.Context(), continuity.ContextInput{ProtocolVersion: 1, SessionID: "known"}); err != nil {
		t.Fatal(err)
	}
	if _, err := flow.Checkpoint(t.Context(), continuity.CheckpointInput{ProtocolVersion: 1, SessionID: "known"}); err != nil {
		t.Fatal(err)
	}
	if _, err := flow.ResolveLegacy(t.Context(), "host", "p"); err != nil {
		t.Fatal(err)
	}
	if err := flow.SetGoalIfEmpty(t.Context(), "known", "goal"); err != nil {
		t.Fatal(err)
	}
	if err := flow.End(t.Context(), session.CloseInput{ID: "known"}); err != nil {
		t.Fatal(err)
	}
	if _, err := flow.EndForHost(t.Context(), "host", "p", session.CloseInput{}); err != nil {
		t.Fatal(err)
	}
	if maintenance.calls != 0 || store.closeCalls != 1 || store.closeForHostCall != 1 || store.setGoalCalls != 1 {
		t.Fatalf("maintenance/lifecycle calls: maintenance=%d close=%d hostClose=%d setGoal=%d", maintenance.calls, store.closeCalls, store.closeForHostCall, store.setGoalCalls)
	}
}

func TestLegacyProjectAdoptionUsesNinetySecondWindow(t *testing.T) {
	store := &fakeLegacySessions{
		byProject: []session.Session{{ID: "recent", StartedAt: time.Now().Add(-time.Second)}},
	}
	flow := continuityflow.New(continuityflow.Config{LegacySessions: store})
	out, err := flow.Start(t.Context(), continuityflow.TaskSessionInput{Project: "p", AdoptLegacy: true})
	if err != nil || out.Receipt.LegacySession == nil || out.Receipt.LegacySession.ID != "recent" {
		t.Fatalf("recent project session not adopted: %#v %v", out, err)
	}
}

func TestStartPreservesReceiptWhenContextCompositionFails(t *testing.T) {
	core := &captureCore{readErr: errors.New("context read failed")}
	composer := &captureComposer{}
	maintenance := &captureMaintenance{}
	flow := continuityflow.New(continuityflow.Config{Core: core, Composer: composer, Maintenance: maintenance})
	root := t.TempDir()
	out, err := flow.Start(t.Context(), continuityflow.TaskSessionInput{
		Continuity: &continuity.OpenInput{
			ProtocolVersion: 1, CWD: root, WorkspaceRoot: root, HarnessKind: "test", StartKey: "context-error",
		},
	})
	if err != nil {
		t.Fatalf("context failure must not hide committed receipt: %v", err)
	}
	if out.Receipt.Continuity == nil || out.Receipt.Continuity.SessionID != "session-1" || out.ContextStatus != "unavailable" {
		t.Fatalf("receipt/context status = %#v", out)
	}
	if core.openedN != 1 || maintenance.calls != 1 {
		t.Fatalf("open calls=%d maintenance calls=%d; want one each", core.openedN, maintenance.calls)
	}
	if core.read.SessionID != "session-1" {
		t.Fatalf("context read session = %q; want committed receipt session", core.read.SessionID)
	}
	if len(composer.requests) != 0 {
		t.Fatalf("composer called after context read failure: %d requests", len(composer.requests))
	}
}

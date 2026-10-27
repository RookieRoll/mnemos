package session_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/polyxmedia/mnemos/internal/continuity"
	"github.com/polyxmedia/mnemos/internal/continuityflow"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// endFixture wires one session service to its database so ownership cases can
// open legacy and continuity-bound sessions and inspect what stayed open.
type endFixture struct {
	svc *session.Service
	db  *storage.DB
	now time.Time
}

func newEndFixture(t *testing.T) *endFixture {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := &endFixture{db: db, now: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	f.svc = session.NewService(session.Config{
		Store: db.Sessions(),
		Clock: func() time.Time { return f.now },
	})
	return f
}

// open inserts one legacy session, stepping the fixture clock so ordering is
// deterministic on coarse platform clocks.
func (f *endFixture) open(t *testing.T, in session.OpenInput) *session.Session {
	t.Helper()
	f.now = f.now.Add(time.Second)
	sess, err := f.svc.Open(context.Background(), in)
	if err != nil {
		t.Fatalf("open %+v: %v", in, err)
	}
	return sess
}

// openContinuity commits one continuity-bound session through the real
// continuity service, giving it the receipt the guard can consult.
func (f *endFixture) openContinuity(t *testing.T, agent string) *session.Session {
	t.Helper()
	root := t.TempDir()
	core := continuity.New(continuity.Config{Store: f.db.Continuity()})
	out, err := core.Open(context.Background(), continuity.OpenInput{
		ProtocolVersion: 1, AgentID: agent, CWD: root, WorkspaceRoot: root,
		HarnessKind: "test", StartKey: "start-" + agent,
	})
	if err != nil {
		t.Fatalf("open continuity session: %v", err)
	}
	sess, err := f.svc.Get(context.Background(), out.SessionID)
	if err != nil {
		t.Fatalf("read continuity session: %v", err)
	}
	if !sess.ContinuityBound {
		t.Fatal("continuity session is not marked bound")
	}
	return sess
}

func (f *endFixture) ended(t *testing.T, id string) bool {
	t.Helper()
	sess, err := f.svc.Get(context.Background(), id)
	if errors.Is(err, session.ErrNotFound) {
		return false
	}
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return sess.EndedAt != nil
}

// fakeReceipts answers the receipt seam from a fixed policy and records which
// sessions it was asked about.
type fakeReceipts struct {
	allow map[string]bool
	err   error
	calls []string
}

func (f *fakeReceipts) AuthorizeSessionEnd(_ context.Context, target *session.Session) (bool, error) {
	f.calls = append(f.calls, target.ID)
	if f.err != nil {
		return false, f.err
	}
	return f.allow[target.ID], nil
}

func TestEndOwnedSessionOwnership(t *testing.T) {
	receiptFailure := errors.New("receipt store unavailable")
	cases := []struct {
		name             string
		setup            func(t *testing.T, f *endFixture) (string, session.EndInvocation)
		wantNotOwned     bool
		wantClosed       bool
		wantOperational  error
		wantReceiptCalls int
		wantErrText      []string
	}{
		{
			name: "bound session closes",
			setup: func(t *testing.T, f *endFixture) (string, session.EndInvocation) {
				sess := f.open(t, session.OpenInput{Project: "api", Goal: "g"})
				return sess.ID, session.EndInvocation{CurrentSessionID: sess.ID, Project: "api"}
			},
			wantClosed: true,
		},
		{
			name: "bound host conversation closes even when a newer terminal exists",
			setup: func(t *testing.T, f *endFixture) (string, session.EndInvocation) {
				mine := f.open(t, session.OpenInput{Project: "api", HostSessionID: "claude-a"})
				f.open(t, session.OpenInput{Project: "api", HostSessionID: "claude-b"})
				return mine.ID, session.EndInvocation{HostSessionID: "claude-a", Project: "api"}
			},
			wantClosed: true,
		},
		{
			name: "project-less invocation cannot infer the newest session",
			setup: func(t *testing.T, f *endFixture) (string, session.EndInvocation) {
				sess := f.open(t, session.OpenInput{Project: "api"})
				return sess.ID, session.EndInvocation{}
			},
			wantNotOwned: true,
		},
		{
			name: "latest session of another host is not owned by a project label",
			setup: func(t *testing.T, f *endFixture) (string, session.EndInvocation) {
				foreign := f.open(t, session.OpenInput{Project: "api", HostSessionID: "other-terminal"})
				return foreign.ID, session.EndInvocation{Project: "api"}
			},
			wantNotOwned: true,
		},
		{
			name: "foreign same-project id rejected and stays open",
			setup: func(t *testing.T, f *endFixture) (string, session.EndInvocation) {
				foreign := f.open(t, session.OpenInput{Project: "api"})
				f.open(t, session.OpenInput{Project: "api"})
				return foreign.ID, session.EndInvocation{Project: "api"}
			},
			wantNotOwned: true,
		},
		{
			name: "same-agent id in another project rejected",
			setup: func(t *testing.T, f *endFixture) (string, session.EndInvocation) {
				foreign := f.open(t, session.OpenInput{Project: "web", AgentID: "agent-a"})
				f.open(t, session.OpenInput{Project: "api", AgentID: "agent-a"})
				return foreign.ID, session.EndInvocation{Project: "api"}
			},
			wantNotOwned: true,
		},
		{
			name: "unknown id rejected with the same sentinel",
			setup: func(t *testing.T, f *endFixture) (string, session.EndInvocation) {
				f.open(t, session.OpenInput{Project: "api"})
				return ulid.Make().String(), session.EndInvocation{Project: "api"}
			},
			wantNotOwned: true,
		},
		{
			name: "no resolvable binding and no receipt rejected",
			setup: func(t *testing.T, f *endFixture) (string, session.EndInvocation) {
				foreign := f.open(t, session.OpenInput{Project: "web"})
				return foreign.ID, session.EndInvocation{Project: "api"}
			},
			wantNotOwned: true,
		},
		{
			name: "receipt in workspace authorizes a continuity session",
			setup: func(t *testing.T, f *endFixture) (string, session.EndInvocation) {
				target := f.openContinuity(t, "agent-a")
				receipts := &fakeReceipts{allow: map[string]bool{target.ID: true}}
				return target.ID, session.EndInvocation{WorkspaceRoot: "/workspace/one", Receipts: receipts}
			},
			wantClosed:       true,
			wantReceiptCalls: 1,
		},
		{
			name: "receipt refusal keeps the continuity session open",
			setup: func(t *testing.T, f *endFixture) (string, session.EndInvocation) {
				target := f.openContinuity(t, "agent-a")
				receipts := &fakeReceipts{allow: map[string]bool{}}
				return target.ID, session.EndInvocation{WorkspaceRoot: "/workspace/one", Receipts: receipts}
			},
			wantNotOwned:     true,
			wantReceiptCalls: 1,
		},
		{
			name: "legacy sessions never consult the receipt seam",
			setup: func(t *testing.T, f *endFixture) (string, session.EndInvocation) {
				foreign := f.open(t, session.OpenInput{Project: "web"})
				receipts := &fakeReceipts{allow: map[string]bool{foreign.ID: true}}
				return foreign.ID, session.EndInvocation{Project: "api", Receipts: receipts}
			},
			wantNotOwned:     true,
			wantReceiptCalls: 0,
		},
		{
			name: "receipt failure surfaces as an operational error",
			setup: func(t *testing.T, f *endFixture) (string, session.EndInvocation) {
				target := f.openContinuity(t, "agent-a")
				receipts := &fakeReceipts{err: receiptFailure}
				return target.ID, session.EndInvocation{WorkspaceRoot: "/workspace/one", Receipts: receipts}
			},
			wantOperational:  receiptFailure,
			wantReceiptCalls: 1,
		},
		{
			name: "rejection carries the id and workspace",
			setup: func(t *testing.T, f *endFixture) (string, session.EndInvocation) {
				foreign := f.open(t, session.OpenInput{Project: "api"})
				f.open(t, session.OpenInput{Project: "api"})
				return foreign.ID, session.EndInvocation{Project: "api", WorkspaceRoot: "/workspace/two"}
			},
			wantNotOwned: true,
			wantErrText:  []string{"session not owned", "/workspace/two"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newEndFixture(t)
			target, inv := tc.setup(t, f)
			err := f.svc.EndOwned(context.Background(), session.CloseInput{
				ID: target, Summary: "done", Status: session.StatusOK,
			}, inv)

			switch {
			case tc.wantOperational != nil:
				if !errors.Is(err, tc.wantOperational) {
					t.Fatalf("want the operational failure wrapped, got %v", err)
				}
				if errors.Is(err, session.ErrSessionNotOwned) {
					t.Fatalf("operational failure must not be reported as not-owned: %v", err)
				}
				if f.ended(t, target) {
					t.Fatal("receipt failure closed the session")
				}
			case tc.wantNotOwned:
				if !errors.Is(err, session.ErrSessionNotOwned) {
					t.Fatalf("want ErrSessionNotOwned, got %v", err)
				}
				var rejected *session.NotOwnedError
				if !errors.As(err, &rejected) || rejected.SessionID != target {
					t.Fatalf("rejection lost the rejected id: %v", err)
				}
				if rejected.WorkspaceRoot != inv.WorkspaceRoot {
					t.Errorf("rejection workspace %q, want %q", rejected.WorkspaceRoot, inv.WorkspaceRoot)
				}
				for _, want := range tc.wantErrText {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("rejection text %q must name %q", err.Error(), want)
					}
				}
				if f.ended(t, target) {
					t.Fatal("rejected session was closed")
				}
			default:
				if err != nil {
					t.Fatalf("EndOwned: %v", err)
				}
				if !f.ended(t, target) {
					t.Fatal("accepted session stayed open")
				}
			}
			if fake, ok := inv.Receipts.(*fakeReceipts); ok && len(fake.calls) != tc.wantReceiptCalls {
				t.Errorf("receipt seam calls = %d, want %d", len(fake.calls), tc.wantReceiptCalls)
			}
		})
	}
}

func TestEndOwnedRejectsEmptyID(t *testing.T) {
	f := newEndFixture(t)
	if err := f.svc.EndOwned(context.Background(), session.CloseInput{}, session.EndInvocation{}); err == nil {
		t.Fatal("empty id must be rejected")
	}
}

func TestHookLifecycleEntryPointsBypassTheOwnershipGuard(t *testing.T) {
	ctx := context.Background()

	t.Run("EndForHost resolves the host conversation", func(t *testing.T) {
		f := newEndFixture(t)
		mine := f.open(t, session.OpenInput{Project: "api", HostSessionID: "claude-a"})
		sibling := f.open(t, session.OpenInput{Project: "api", HostSessionID: "claude-b"})
		flow := continuityflow.New(continuityflow.Config{LegacySessions: f.svc})

		closed, err := flow.EndForHost(ctx, "claude-a", "api", session.CloseInput{Status: session.StatusOK})
		if err != nil {
			t.Fatalf("hook close for host: %v", err)
		}
		if closed != 1 {
			t.Fatalf("hook closed %d sessions, want 1", closed)
		}
		if !f.ended(t, mine.ID) {
			t.Error("the host conversation's session must close without an ownership check")
		}
		if f.ended(t, sibling.ID) {
			t.Error("the hook path must leave the other terminal's session open")
		}
	})

	t.Run("End closes a resolved neutral-format session", func(t *testing.T) {
		f := newEndFixture(t)
		// The neutral hook format resolves the session itself and then calls
		// End directly; that path must not pass through the MCP guard.
		foreign := f.open(t, session.OpenInput{Project: "web"})
		flow := continuityflow.New(continuityflow.Config{LegacySessions: f.svc})

		if err := flow.End(ctx, session.CloseInput{ID: foreign.ID, Status: session.StatusOK, Summary: "hook"}); err != nil {
			t.Fatalf("neutral hook close: %v", err)
		}
		if !f.ended(t, foreign.ID) {
			t.Error("the neutral hook path must close the resolved session")
		}
	})
}

func TestCloseEachRemainsTheTrustedMaintenanceChannel(t *testing.T) {
	f := newEndFixture(t)
	ctx := context.Background()
	first := f.open(t, session.OpenInput{Project: "api"})
	second := f.open(t, session.OpenInput{Project: "web"})

	closed, err := f.svc.CloseEach(ctx, []session.Session{*first, *second}, session.CloseInput{
		Status: session.StatusAbandoned, Summary: "operator sweep",
	})
	if err != nil {
		t.Fatalf("trusted close each: %v", err)
	}
	if closed != 2 {
		t.Fatalf("trusted channel closed %d sessions, want 2", closed)
	}
	for _, id := range []string{first.ID, second.ID} {
		if !f.ended(t, id) {
			t.Errorf("local maintenance close must bypass the guard for %s", id)
		}
	}
}

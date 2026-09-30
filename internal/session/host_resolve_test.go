package session_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/session"
)

func mustOpen(t *testing.T, svc *session.Service, in session.OpenInput) *session.Session {
	t.Helper()
	sess, err := svc.Open(context.Background(), in)
	if err != nil {
		t.Fatalf("open %+v: %v", in, err)
	}
	return sess
}

func TestResolvePrefersTheHostConversationsSession(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	svc := newClockedService(t, &now)

	mine := mustOpen(t, svc, session.OpenInput{Project: "taken", HostSessionID: "claude-a"})
	now = now.Add(time.Minute)
	// A newer session in another terminal of the same project, and a newer
	// one in another project. Neither may be picked for claude-a.
	mustOpen(t, svc, session.OpenInput{Project: "taken", HostSessionID: "claude-b"})
	now = now.Add(time.Minute)
	mustOpen(t, svc, session.OpenInput{Project: "wayframer", HostSessionID: "claude-c"})

	got, err := svc.Resolve(ctx, "claude-a", "taken")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.ID != mine.ID {
		t.Errorf("resolved %s (%s), want the claude-a session %s", got.ID, got.HostSessionID, mine.ID)
	}
}

func TestResolveNeverCrossesProjects(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	svc := newClockedService(t, &now)

	mustOpen(t, svc, session.OpenInput{Project: "wayframer", HostSessionID: "claude-b"})
	mustOpen(t, svc, session.OpenInput{Project: "wayframer"})

	for _, tc := range []struct{ host, project string }{
		{"claude-a", "taken"},
		{"", "taken"},
		{"claude-a", ""},
		{"", ""},
	} {
		if got, err := svc.Resolve(ctx, tc.host, tc.project); !errors.Is(err, session.ErrNotFound) {
			t.Errorf("Resolve(%q, %q) = %+v, %v; want ErrNotFound", tc.host, tc.project, got, err)
		}
	}
}

func TestResolveFallsBackOnlyToUnownedProjectSessions(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	svc := newClockedService(t, &now)

	// Opened before host IDs were recorded, so nobody owns it.
	legacy := mustOpen(t, svc, session.OpenInput{Project: "taken"})
	now = now.Add(time.Minute)
	owned := mustOpen(t, svc, session.OpenInput{Project: "taken", HostSessionID: "claude-b"})

	got, err := svc.Resolve(ctx, "claude-a", "taken")
	if err != nil {
		t.Fatalf("resolve with unknown host: %v", err)
	}
	if got.ID != legacy.ID {
		t.Errorf("unknown host must fall back to the unowned session %s, got %s", legacy.ID, got.ID)
	}

	// With no host ID at all, the newest open session in the project wins.
	got, err = svc.Resolve(ctx, "", "taken")
	if err != nil {
		t.Fatalf("resolve without host: %v", err)
	}
	if got.ID != owned.ID {
		t.Errorf("no host must resolve to the newest project session %s, got %s", owned.ID, got.ID)
	}
}

func TestCloseForHostClosesOnlyThatConversation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	svc := newClockedService(t, &now)

	twinA := mustOpen(t, svc, session.OpenInput{Project: "taken", HostSessionID: "claude-a"})
	now = now.Add(100 * time.Millisecond)
	twinB := mustOpen(t, svc, session.OpenInput{Project: "taken", HostSessionID: "claude-a"})
	// Another terminal in the same project, started inside the twin window.
	// The project-wide twin rule would have closed it; the host rule must not.
	now = now.Add(10 * time.Second)
	sibling := mustOpen(t, svc, session.OpenInput{Project: "taken", HostSessionID: "claude-b"})

	closed, err := svc.CloseForHost(ctx, "claude-a", "taken", session.CloseInput{Status: session.StatusOK})
	if err != nil {
		t.Fatalf("close for host: %v", err)
	}
	if closed != 2 {
		t.Errorf("want both claude-a sessions closed, got %d", closed)
	}
	for _, id := range []string{twinA.ID, twinB.ID} {
		if got, _ := svc.Get(ctx, id); got.EndedAt == nil {
			t.Errorf("claude-a session %s must be closed", id)
		}
	}
	if got, _ := svc.Get(ctx, sibling.ID); got.EndedAt != nil {
		t.Error("the other terminal's session must stay open")
	}
}

func TestCloseForHostWithNeitherHostNorProjectClosesNothing(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	svc := newClockedService(t, &now)

	live := mustOpen(t, svc, session.OpenInput{Project: "wayframer"})

	closed, err := svc.CloseForHost(ctx, "", "", session.CloseInput{Status: session.StatusOK})
	if err != nil {
		t.Fatalf("close for host: %v", err)
	}
	if closed != 0 {
		t.Errorf("an unattributable SessionEnd must close nothing, closed %d", closed)
	}
	if got, _ := svc.Get(ctx, live.ID); got.EndedAt != nil {
		t.Error("a live session in another project must stay open")
	}
}

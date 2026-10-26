package session_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/session"
)

// seededSession describes one session to open before a test: which agent and
// project it belongs to, exactly when it started, and whether it was closed.
type seededSession struct {
	agent   string
	project string
	started time.Time
	closed  bool
}

// seedAt opens a session at seed.started by moving the fixture clock, and
// optionally closes it. The exact start times let the stale-selection tests
// pin the cutoff boundary without sleeping.
func seedAt(t *testing.T, svc *session.Service, now *time.Time, seed seededSession) *session.Session {
	t.Helper()
	*now = seed.started
	sess := mustOpen(t, svc, session.OpenInput{
		AgentID: seed.agent,
		Project: seed.project,
		Goal:    "seed",
	})
	if seed.closed {
		if err := svc.Close(context.Background(), session.CloseInput{
			ID: sess.ID, Summary: "seed", Status: session.StatusOK,
		}); err != nil {
			t.Fatalf("close seeded session %s: %v", sess.ID, err)
		}
	}
	return sess
}

func TestOpenBacklog(t *testing.T) {
	base := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		seeds      []seededSession
		wantCount  int
		wantOldest time.Time // zero means no open session at all
	}{
		{
			name:      "empty store reports zero and no age",
			wantCount: 0,
		},
		{
			name: "all sessions closed reports zero",
			seeds: []seededSession{
				{project: "alpha", started: base.Add(-48 * time.Hour), closed: true},
				{project: "alpha", started: base.Add(-2 * time.Hour), closed: true},
			},
			wantCount: 0,
		},
		{
			name: "counts open sessions across projects and agents, oldest wins",
			seeds: []seededSession{
				{project: "alpha", started: base.Add(-2 * time.Hour)},
				{agent: "agent-b", project: "beta", started: base.Add(-48 * time.Hour)},
				{project: "alpha", started: base.Add(-24 * time.Hour)},
			},
			wantCount:  3,
			wantOldest: base.Add(-48 * time.Hour),
		},
		{
			name: "closed sessions feed neither count nor age",
			seeds: []seededSession{
				{project: "alpha", started: base.Add(-72 * time.Hour), closed: true},
				{project: "alpha", started: base.Add(-2 * time.Hour)},
			},
			wantCount:  1,
			wantOldest: base.Add(-2 * time.Hour),
		},
		{
			name: "oldest is found regardless of insertion order",
			seeds: []seededSession{
				{project: "alpha", started: base.Add(-96 * time.Hour)},
				{project: "alpha", started: base.Add(-12 * time.Hour)},
				{project: "alpha", started: base.Add(-3 * time.Hour)},
			},
			wantCount:  3,
			wantOldest: base.Add(-96 * time.Hour),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			now := base
			svc := newClockedService(t, &now)
			for _, seed := range tc.seeds {
				seedAt(t, svc, &now, seed)
			}
			got, err := svc.OpenBacklog(ctx)
			if err != nil {
				t.Fatalf("open backlog: %v", err)
			}
			if got.Count != tc.wantCount {
				t.Errorf("count = %d, want %d", got.Count, tc.wantCount)
			}
			if tc.wantOldest.IsZero() {
				if !got.OldestStartedAt.IsZero() {
					t.Errorf("oldest = %s, want the zero time", got.OldestStartedAt)
				}
				return
			}
			if !got.OldestStartedAt.Equal(tc.wantOldest) {
				t.Errorf("oldest = %s, want %s", got.OldestStartedAt, tc.wantOldest)
			}
		})
	}
}

func TestStaleCutoff(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	svc := newClockedService(t, &now)

	cutoff, err := svc.StaleCutoff(24 * time.Hour)
	if err != nil {
		t.Fatalf("stale cutoff: %v", err)
	}
	if want := now.Add(-24 * time.Hour); !cutoff.Equal(want) {
		t.Errorf("cutoff = %s, want %s", cutoff, want)
	}

	tests := []struct {
		name string
		d    time.Duration
	}{
		{"zero", 0},
		{"negative", -time.Hour},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.StaleCutoff(tc.d); err == nil {
				t.Errorf("StaleCutoff(%s) = nil error, want rejection", tc.d)
			}
		})
	}
}

func TestSelectStaleOpen(t *testing.T) {
	base := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	cutoff := base.Add(-24 * time.Hour)
	tests := []struct {
		name  string
		seeds []seededSession
		in    session.StaleOpenInput
		want  []int // indices into seeds, in expected order
	}{
		{
			name:  "nothing older than the cutoff",
			seeds: []seededSession{{project: "alpha", started: base.Add(-time.Hour)}},
			in:    session.StaleOpenInput{Before: cutoff},
			want:  nil,
		},
		{
			name: "started exactly at the cutoff is not stale",
			seeds: []seededSession{
				{project: "alpha", started: cutoff},
				{project: "alpha", started: cutoff.Add(-time.Nanosecond)},
				{project: "alpha", started: cutoff.Add(time.Nanosecond)},
			},
			in:   session.StaleOpenInput{Before: cutoff},
			want: []int{1},
		},
		{
			name: "project filter keeps other projects out",
			seeds: []seededSession{
				{project: "alpha", started: base.Add(-72 * time.Hour)},
				{project: "beta", started: base.Add(-72 * time.Hour)},
				{project: "alpha", started: base.Add(-time.Hour)},
			},
			in:   session.StaleOpenInput{Before: cutoff, Project: "alpha"},
			want: []int{0},
		},
		{
			name: "empty project selects every project, oldest first",
			seeds: []seededSession{
				{project: "beta", started: base.Add(-30 * time.Hour)},
				{project: "alpha", started: base.Add(-72 * time.Hour)},
				{project: "beta", started: base.Add(-48 * time.Hour)},
			},
			in:   session.StaleOpenInput{Before: cutoff},
			want: []int{1, 2, 0},
		},
		{
			name: "closed sessions are never selected",
			seeds: []seededSession{
				{project: "alpha", started: base.Add(-96 * time.Hour), closed: true},
				{project: "alpha", started: base.Add(-48 * time.Hour)},
			},
			in:   session.StaleOpenInput{Before: cutoff},
			want: []int{1},
		},
		{
			name: "limit truncates to the oldest stale sessions",
			seeds: []seededSession{
				{project: "alpha", started: base.Add(-30 * time.Hour)},
				{project: "alpha", started: base.Add(-72 * time.Hour)},
				{project: "alpha", started: base.Add(-48 * time.Hour)},
			},
			in:   session.StaleOpenInput{Before: cutoff, Limit: 2},
			want: []int{1, 2},
		},
		{
			name: "zero limit means no cap",
			seeds: []seededSession{
				{project: "alpha", started: base.Add(-30 * time.Hour)},
				{project: "alpha", started: base.Add(-72 * time.Hour)},
			},
			in:   session.StaleOpenInput{Before: cutoff},
			want: []int{1, 0},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			now := base
			svc := newClockedService(t, &now)
			seeded := make([]*session.Session, len(tc.seeds))
			for i, seed := range tc.seeds {
				seeded[i] = seedAt(t, svc, &now, seed)
			}

			got, err := svc.SelectStaleOpen(ctx, tc.in)
			if err != nil {
				t.Fatalf("select stale open: %v", err)
			}
			var wantIDs []string
			for _, i := range tc.want {
				wantIDs = append(wantIDs, seeded[i].ID)
			}
			var gotIDs []string
			for _, sess := range got {
				gotIDs = append(gotIDs, sess.ID)
			}
			if !reflect.DeepEqual(gotIDs, wantIDs) {
				t.Fatalf("selected %v, want %v", gotIDs, wantIDs)
			}
			for i := 1; i < len(got); i++ {
				if got[i].StartedAt.Before(got[i-1].StartedAt) {
					t.Errorf("selection not oldest first: %s before %s", got[i].StartedAt, got[i-1].StartedAt)
				}
			}
		})
	}
}

func TestSelectStaleOpenRequiresCutoff(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	svc := newClockedService(t, &now)
	if _, err := svc.SelectStaleOpen(context.Background(), session.StaleOpenInput{}); err == nil {
		t.Error("selection without a cutoff must be rejected")
	}
}

func TestSelectStaleOpenDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	now := base
	svc := newClockedService(t, &now)

	seedAt(t, svc, &now, seededSession{project: "alpha", started: base.Add(-72 * time.Hour)})
	seedAt(t, svc, &now, seededSession{project: "alpha", started: base.Add(-time.Hour)})
	seedAt(t, svc, &now, seededSession{project: "beta", started: base.Add(-96 * time.Hour), closed: true})

	before, err := svc.Recent(ctx, "", 100)
	if err != nil {
		t.Fatalf("recent before: %v", err)
	}
	selected, err := svc.SelectStaleOpen(ctx, session.StaleOpenInput{Before: base.Add(-24 * time.Hour)})
	if err != nil {
		t.Fatalf("select stale open: %v", err)
	}
	if len(selected) != 1 {
		t.Fatalf("want the one stale open session selected, got %d", len(selected))
	}
	after, err := svc.Recent(ctx, "", 100)
	if err != nil {
		t.Fatalf("recent after: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("selection mutated the store: before %+v, after %+v", before, after)
	}
	open, err := svc.ListOpen(ctx, "")
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	if len(open) != 2 {
		t.Errorf("want 2 sessions still open after selection, got %d", len(open))
	}
}

func TestCloseEachClosesExactlyTheSelection(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	now := base
	svc := newClockedService(t, &now)

	oldest := seedAt(t, svc, &now, seededSession{project: "alpha", started: base.Add(-72 * time.Hour)})
	older := seedAt(t, svc, &now, seededSession{project: "alpha", started: base.Add(-48 * time.Hour)})
	fresh := seedAt(t, svc, &now, seededSession{project: "alpha", started: base.Add(-time.Hour)})
	seedAt(t, svc, &now, seededSession{project: "alpha", started: base.Add(-96 * time.Hour), closed: true})

	sel := session.StaleOpenInput{Before: base.Add(-24 * time.Hour)}
	targets, err := svc.SelectStaleOpen(ctx, sel)
	if err != nil {
		t.Fatalf("select stale open: %v", err)
	}
	if len(targets) != 2 || targets[0].ID != oldest.ID || targets[1].ID != older.ID {
		t.Fatalf("selection = %+v, want the two stale sessions oldest first", targets)
	}

	closed, err := svc.CloseEach(ctx, targets, session.CloseInput{
		Summary: "reclaimed", Status: session.StatusAbandoned,
	})
	if err != nil {
		t.Fatalf("close each: %v", err)
	}
	if closed != 2 {
		t.Errorf("closed %d, want 2", closed)
	}

	for _, target := range []*session.Session{oldest, older} {
		got, err := svc.Get(ctx, target.ID)
		if err != nil {
			t.Fatalf("get %s: %v", target.ID, err)
		}
		if got.Status != session.StatusAbandoned || got.Summary != "reclaimed" || got.EndedAt == nil {
			t.Errorf("session %s = status %q summary %q ended %v, want abandoned/reclaimed/closed",
				target.ID, got.Status, got.Summary, got.EndedAt)
		}
	}
	open, err := svc.ListOpen(ctx, "")
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	if len(open) != 1 || open[0].ID != fresh.ID {
		t.Errorf("open = %+v, want only the fresh session %s", open, fresh.ID)
	}

	// A second pass over the same cutoff is a clean no-op: the closed rows
	// are no longer open, so the selection is empty and CloseEach writes
	// nothing.
	again, err := svc.SelectStaleOpen(ctx, sel)
	if err != nil {
		t.Fatalf("select stale open again: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second selection = %d sessions, want 0", len(again))
	}
	if n, err := svc.CloseEach(ctx, again, session.CloseInput{Status: session.StatusAbandoned}); err != nil || n != 0 {
		t.Errorf("second close each = %d, %v; want 0, nil", n, err)
	}
}

func TestCloseEachRejectsInvalidStatus(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	svc := newClockedService(t, &now)
	if _, err := svc.CloseEach(context.Background(), nil, session.CloseInput{
		Status: session.Status("garbage"),
	}); err == nil {
		t.Error("invalid status must be rejected even with an empty selection")
	}
}

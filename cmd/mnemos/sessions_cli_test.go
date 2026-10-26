package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/session"
)

// restoreSession inserts a session with explicit timestamps and end state, so a
// test can build a stale backlog without waiting or faking the clock.
func restoreSession(t *testing.T, ctx context.Context, id, project string, started time.Time, ended *time.Time, status session.Status) {
	t.Helper()
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	defer d.close()
	if _, err := d.sess.Restore(ctx, &session.Session{
		ID: id, AgentID: "default", Project: project, Goal: "fixture " + id,
		Status: status, StartedAt: started, EndedAt: ended,
	}); err != nil {
		t.Fatalf("restore session %s: %v", id, err)
	}
}

// openSessionIDs lists the ids of every open session, newest first.
func openSessionIDs(t *testing.T, ctx context.Context) []string {
	t.Helper()
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	defer d.close()
	open, err := d.sess.ListOpen(ctx, "")
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	ids := make([]string, 0, len(open))
	for _, s := range open {
		ids = append(ids, s.ID)
	}
	return ids
}

func TestSessionsCloseStale(t *testing.T) {
	cases := []struct {
		name      string
		orphans   int
		status    string
		summary   string
		wantStat  session.Status
		wantSumm  string
		useStatus bool
	}{
		{name: "nothing to reclaim", orphans: 0},
		{name: "one orphan", orphans: 1, status: "abandoned", wantStat: session.StatusAbandoned, useStatus: true},
		{name: "three orphans with summary", orphans: 3, status: "failed", summary: "reclaimed by test", wantStat: session.StatusFailed, wantSumm: "reclaimed by test", useStatus: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			homeWithConfig(t, "")
			ctx := context.Background()
			now := time.Now().UTC()
			for i := 0; i < tc.orphans; i++ {
				restoreSession(t, ctx, "stale-"+string(rune('a'+i)), "mnemos", now.Add(-48*time.Hour), nil, session.StatusOK)
			}
			// Two sessions started just now must survive the sweep.
			restoreSession(t, ctx, "live-1", "mnemos", now, nil, session.StatusOK)
			restoreSession(t, ctx, "live-2", "other", now, nil, session.StatusOK)

			args := []string{"close", "--stale", "24h"}
			if tc.useStatus {
				args = append(args, "--status", tc.status)
			}
			if tc.summary != "" {
				args = append(args, "--summary", tc.summary)
			}
			out := captureStdout(t, func() {
				if err := runSessions(ctx, args); err != nil {
					t.Fatalf("sessions close: %v", err)
				}
			})
			if !strings.Contains(out, "closed "+strconv.Itoa(tc.orphans)+" session(s)") {
				t.Fatalf("report must name the count: %q", out)
			}

			open := openSessionIDs(t, ctx)
			if len(open) != 2 {
				t.Fatalf("open sessions after sweep = %v, want the two fresh ones", open)
			}
			for i := 0; i < tc.orphans; i++ {
				id := "stale-" + string(rune('a'+i))
				d, err := loadDeps(ctx)
				if err != nil {
					t.Fatalf("loadDeps: %v", err)
				}
				got, err := d.sess.Get(ctx, id)
				d.close()
				if err != nil {
					t.Fatalf("get %s: %v", id, err)
				}
				if got.EndedAt == nil {
					t.Fatalf("%s was not closed", id)
				}
				if tc.useStatus && got.Status != tc.wantStat {
					t.Errorf("%s status = %q, want %q", id, got.Status, tc.wantStat)
				}
				if got.Summary != tc.wantSumm {
					t.Errorf("%s summary = %q, want %q", id, got.Summary, tc.wantSumm)
				}
			}
		})
	}
}

func TestSessionsCloseStaleDryRunWritesNothing(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		restoreSession(t, ctx, "stale-"+string(rune('a'+i)), "mnemos", now.Add(-48*time.Hour), nil, session.StatusOK)
	}
	before := openSessionIDs(t, ctx)

	out := captureStdout(t, func() {
		if err := runSessions(ctx, []string{"close", "--stale", "24h", "--dry-run"}); err != nil {
			t.Fatalf("sessions close --dry-run: %v", err)
		}
	})
	if !strings.Contains(out, "would close 3 session(s)") {
		t.Fatalf("dry run must print the same count: %q", out)
	}
	after := openSessionIDs(t, ctx)
	if len(before) != len(after) {
		t.Fatalf("dry run changed the open set: %v -> %v", before, after)
	}
}

func TestSessionsCloseStaleRejectsNonPositiveDuration(t *testing.T) {
	for _, stale := range []string{"0", "-1h"} {
		t.Run(stale, func(t *testing.T) {
			homeWithConfig(t, "")
			restoreSession(t, context.Background(), "stale-a", "mnemos", time.Now().UTC().Add(-48*time.Hour), nil, session.StatusOK)
			var stdout, stderr bytes.Buffer
			code := dispatch(context.Background(), []string{"sessions", "close", "--stale", stale}, &stdout, &stderr)
			if code != exitUsage {
				t.Fatalf("exit = %d, want %d (stderr %q)", code, exitUsage, stderr.String())
			}
			open := openSessionIDs(t, context.Background())
			if len(open) != 1 {
				t.Fatalf("a rejected duration closed sessions: %v", open)
			}
		})
	}
}

func TestSessionsListFiltersAndPagination(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()
	base := time.Now().UTC().Add(-5 * time.Hour)
	closed := base.Add(30 * time.Minute)
	// Newest first: s1, s2, s3, s4, s5. s2 and s4 are closed.
	restoreSession(t, ctx, "s1", "alpha", base.Add(4*time.Hour), nil, session.StatusOK)
	restoreSession(t, ctx, "s2", "alpha", base.Add(3*time.Hour), &closed, session.StatusOK)
	restoreSession(t, ctx, "s3", "beta", base.Add(2*time.Hour), nil, session.StatusOK)
	restoreSession(t, ctx, "s4", "beta", base.Add(1*time.Hour), &closed, session.StatusFailed)
	restoreSession(t, ctx, "s5", "gamma", base, nil, session.StatusOK)

	t.Run("paginate", func(t *testing.T) {
		out := captureStdout(t, func() {
			if err := runSessions(ctx, []string{"--limit", "2", "--offset", "2"}); err != nil {
				t.Fatalf("sessions: %v", err)
			}
		})
		if !strings.Contains(out, "s3") || !strings.Contains(out, "s4") {
			t.Fatalf("page 2 must list s3 and s4: %q", out)
		}
		if strings.Contains(out, "s1 ") || strings.Contains(out, "s5") {
			t.Fatalf("page 2 leaked other rows: %q", out)
		}
	})

	t.Run("status open", func(t *testing.T) {
		out := captureStdout(t, func() {
			if err := runSessions(ctx, []string{"--status", "open"}); err != nil {
				t.Fatalf("sessions: %v", err)
			}
		})
		for _, want := range []string{"s1", "s3", "s5"} {
			if !strings.Contains(out, want) {
				t.Errorf("open filter missing %s: %q", want, out)
			}
		}
		for _, unwanted := range []string{"s2", "s4"} {
			if strings.Contains(out, unwanted) {
				t.Errorf("open filter leaked closed session %s: %q", unwanted, out)
			}
		}
	})

	t.Run("json", func(t *testing.T) {
		out := captureStdout(t, func() {
			if err := runSessions(ctx, []string{"--json", "--limit", "1"}); err != nil {
				t.Fatalf("sessions --json: %v", err)
			}
		})
		var page []map[string]any
		if err := json.Unmarshal([]byte(out), &page); err != nil {
			t.Fatalf("json page did not parse: %v: %s", err, out)
		}
		if len(page) != 1 {
			t.Fatalf("json page length = %d, want 1", len(page))
		}
		if page[0]["id"] != "s1" || page[0]["project"] != "alpha" || page[0]["status"] != "ok" {
			t.Fatalf("unexpected json row: %v", page[0])
		}
		if _, ok := page[0]["started_at"]; !ok {
			t.Fatalf("json row missing started_at: %v", page[0])
		}
	})
}

func TestSessionsListRejectsBadFilters(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown status", []string{"sessions", "--status", "bogus"}, "invalid --status"},
		{"zero limit", []string{"sessions", "--limit", "0"}, "--limit must be positive"},
		{"negative offset", []string{"sessions", "--offset", "-1"}, "--offset must not be negative"},
		{"positional", []string{"sessions", "junk"}, "unexpected positional arguments"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeWithConfig(t, "")
			var stdout, stderr bytes.Buffer
			code := dispatch(context.Background(), tc.args, &stdout, &stderr)
			if code != exitUsage {
				t.Fatalf("exit = %d, want %d (stderr %q)", code, exitUsage, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr missing %q: %q", tc.want, stderr.String())
			}
		})
	}
}

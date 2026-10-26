package main

import (
	"bytes"
	"context"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// seedRetireObservation saves one observation and returns its id. access > 0
// bumps the access counter through the normal read path, which is what
// --no-access filters on.
func seedRetireObservation(t *testing.T, ctx context.Context, title, project string, importance, access int) string {
	t.Helper()
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	defer d.close()
	res, err := d.mem.Save(ctx, memory.SaveInput{
		Title: title, Content: "synthetic content for " + title,
		Type: memory.TypeContext, Project: project, Importance: importance,
	})
	if err != nil {
		t.Fatalf("save %s: %v", title, err)
	}
	id := res.Observation.ID
	for i := 0; i < access; i++ {
		if _, err := d.mem.Get(ctx, id); err != nil {
			t.Fatalf("bump access %s: %v", title, err)
		}
	}
	return id
}

// retireState maps observation title to whether it carries a valid_until, the
// only field retirement writes.
func retireState(t *testing.T, ctx context.Context) map[string]bool {
	t.Helper()
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	defer d.close()
	all, err := d.db.Observations().Export(ctx)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	out := make(map[string]bool, len(all))
	for _, o := range all {
		out[o.Title] = o.ValidUntil != nil
	}
	return out
}

func retiredTitles(t *testing.T, ctx context.Context) []string {
	t.Helper()
	state := retireState(t, ctx)
	var out []string
	for title, retired := range state {
		if retired {
			out = append(out, title)
		}
	}
	sort.Strings(out)
	return out
}

func TestRetireSelectorsRequireOne(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()
	seedRetireObservation(t, ctx, "keep me", "alpha", 2, 0)

	var stdout, stderr bytes.Buffer
	code := dispatch(ctx, []string{"retire"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit = %d, want %d (stderr %q)", code, exitUsage, stderr.String())
	}
	if !strings.Contains(stderr.String(), "at least one selector") {
		t.Fatalf("stderr must explain the missing selector: %q", stderr.String())
	}
	if got := retiredTitles(t, ctx); len(got) != 0 {
		t.Fatalf("a selector-less retire wrote: %v", got)
	}
}

func TestRetireDryRunWritesNothing(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()
	for _, title := range []string{"a", "b", "c"} {
		seedRetireObservation(t, ctx, title, "alpha", 2, 0)
	}
	out := captureStdout(t, func() {
		if err := runRetire(ctx, []string{"--project", "alpha", "--importance-max", "3", "--dry-run"}); err != nil {
			t.Fatalf("retire --dry-run: %v", err)
		}
	})
	if !strings.Contains(out, "would retire 3 observation(s)") {
		t.Fatalf("dry run report: %q", out)
	}
	if got := retiredTitles(t, ctx); len(got) != 0 {
		t.Fatalf("dry run wrote valid_until on: %v", got)
	}
}

func TestRetireRespectsSelectors(t *testing.T) {
	// Seed once per subtest so each case starts from a clean store: Save
	// dedups by (agent, project, title), so reusing titles across cases in one
	// store would silently collapse rows.
	type seed struct {
		title      string
		project    string
		importance int
		access     int
	}
	cases := []struct {
		name     string
		args     []string
		wantRows []string
	}{
		{
			name:     "project only",
			args:     []string{"--project", "alpha"},
			wantRows: []string{"alpha-low", "alpha-high", "alpha-used"},
		},
		{
			name:     "importance only",
			args:     []string{"--importance-max", "2"},
			wantRows: []string{"alpha-low", "alpha-used", "beta-low"},
		},
		{
			name:     "no access only",
			args:     []string{"--no-access"},
			wantRows: []string{"alpha-high", "alpha-low", "beta-low"},
		},
		{
			name:     "project and importance",
			args:     []string{"--project", "alpha", "--importance-max", "2"},
			wantRows: []string{"alpha-low", "alpha-used"},
		},
		{
			name:     "all three selectors",
			args:     []string{"--project", "alpha", "--importance-max", "2", "--no-access"},
			wantRows: []string{"alpha-low"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			homeWithConfig(t, "")
			ctx := context.Background()
			for _, s := range []seed{
				{"alpha-low", "alpha", 2, 0},
				{"alpha-high", "alpha", 8, 0},
				{"alpha-used", "alpha", 2, 1},
				{"beta-low", "beta", 2, 0},
			} {
				seedRetireObservation(t, ctx, s.title, s.project, s.importance, s.access)
			}
			// Preview is the shared selection path; the write path is covered by
			// TestRetireSetsValidUntilOnly once the store advertises the capability.
			args := append(append([]string{}, tc.args...), "--dry-run")
			out := captureStdout(t, func() {
				if err := runRetire(ctx, args); err != nil {
					t.Fatalf("retire %v: %v", args, err)
				}
			})
			wantMessage := "would retire " + strconv.Itoa(len(tc.wantRows)) + " observation(s)"
			if !strings.Contains(out, wantMessage) {
				t.Fatalf("report missing %q: %q", wantMessage, out)
			}
			for _, title := range tc.wantRows {
				if !strings.Contains(out, "  "+title) {
					t.Errorf("selection missing %s: %q", title, out)
				}
			}
			if got := retiredTitles(t, ctx); len(got) != 0 {
				t.Fatalf("dry run wrote valid_until on: %v", got)
			}
		})
	}
}

func TestRetireSetsValidUntilOnly(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()
	probe, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	_, retires := probe.db.Observations().(memory.RetirementStore)
	probe.close()
	if !retires {
		t.Fatal("internal/storage must implement memory.RetirementStore: the CLI write path delegates the bulk retirement to it")
	}
	seedRetireObservation(t, ctx, "retire me", "alpha", 2, 0)

	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	before, err := d.db.Observations().Export(ctx)
	if err != nil {
		t.Fatalf("export before: %v", err)
	}
	d.close()

	if err := runRetire(ctx, []string{"--project", "alpha"}); err != nil {
		t.Fatalf("retire: %v", err)
	}

	d, err = loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	defer d.close()
	after, err := d.db.Observations().Export(ctx)
	if err != nil {
		t.Fatalf("export after: %v", err)
	}
	if len(before) != 1 || len(after) != 1 {
		t.Fatalf("unexpected observation counts: %d -> %d", len(before), len(after))
	}
	b, a := before[0], after[0]
	if a.ValidUntil == nil {
		t.Fatal("retirement did not set valid_until")
	}
	if a.InvalidatedAt != nil || b.InvalidatedAt != nil {
		t.Fatalf("retirement touched invalidated_at: %v -> %v", b.InvalidatedAt, a.InvalidatedAt)
	}
	if !a.CreatedAt.Equal(b.CreatedAt) || a.Content != b.Content || a.Title != b.Title {
		t.Fatal("retirement changed created_at or text")
	}
}

func TestRetireSampleOutputBounded(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()
	for i := 0; i < 7; i++ {
		seedRetireObservation(t, ctx, "sample-"+strconv.Itoa(i), "alpha", 1, 0)
	}
	out := captureStdout(t, func() {
		if err := runRetire(ctx, []string{"--project", "alpha", "--dry-run"}); err != nil {
			t.Fatalf("retire --dry-run: %v", err)
		}
	})
	if !strings.Contains(out, "would retire 7 observation(s)") {
		t.Fatalf("full count missing: %q", out)
	}
	if !strings.Contains(out, "... 2 more not shown") {
		t.Fatalf("bounded sample marker missing: %q", out)
	}
	sampleLines := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  sample-") {
			sampleLines++
		}
	}
	if sampleLines > 5 {
		t.Fatalf("sample printed %d rows, want at most 5: %q", sampleLines, out)
	}
}

// backdateRetireObservation moves an observation's created_at into the past so
// --older-than has an age to compare against. Save stamps created_at from the
// command's own clock, so an age fixture can only come from the store.
func backdateRetireObservation(t *testing.T, ctx context.Context, id string, createdAt time.Time) {
	t.Helper()
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	defer d.close()
	if _, err := d.db.SQL().ExecContext(ctx,
		`UPDATE observations SET created_at = ? WHERE id = ?`,
		createdAt.UTC(), id,
	); err != nil {
		t.Fatalf("backdate %s: %v", id, err)
	}
}

// TestRetireOlderThanSelector pins the --older-than selector: the duration is
// relative to the invocation and selects rows created strictly earlier, it
// satisfies the "one selector required" guard on its own, and it ANDs with the
// other selectors. Every case previews, so nothing is written.
func TestRetireOlderThanSelector(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantRows []string
		notRows  []string
	}{
		{
			name:     "older than a day takes the two-day-old rows",
			args:     []string{"--older-than", "24h"},
			wantRows: []string{"old-alpha", "old-beta"},
			notRows:  []string{"fresh-alpha"},
		},
		{
			name:    "older than three days takes nothing",
			args:    []string{"--older-than", "72h"},
			notRows: []string{"old-alpha", "fresh-alpha", "old-beta"},
		},
		{
			name:     "older-than ANDs with project",
			args:     []string{"--older-than", "24h", "--project", "alpha"},
			wantRows: []string{"old-alpha"},
			notRows:  []string{"fresh-alpha", "old-beta"},
		},
		{
			name:     "older-than ANDs with importance",
			args:     []string{"--older-than", "24h", "--importance-max", "1"},
			wantRows: []string{"old-beta"},
			notRows:  []string{"old-alpha", "fresh-alpha"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			homeWithConfig(t, "")
			ctx := context.Background()
			seeds := []struct {
				title      string
				project    string
				importance int
				age        time.Duration
			}{
				{title: "old-alpha", project: "alpha", importance: 2, age: 48 * time.Hour},
				{title: "fresh-alpha", project: "alpha", importance: 2, age: time.Hour},
				{title: "old-beta", project: "beta", importance: 1, age: 48 * time.Hour},
			}
			for _, seed := range seeds {
				id := seedRetireObservation(t, ctx, seed.title, seed.project, seed.importance, 0)
				backdateRetireObservation(t, ctx, id, time.Now().UTC().Add(-seed.age))
			}

			args := append(append([]string{}, tc.args...), "--dry-run")
			out := captureStdout(t, func() {
				if err := runRetire(ctx, args); err != nil {
					t.Fatalf("retire %v: %v", args, err)
				}
			})
			wantMessage := "would retire " + strconv.Itoa(len(tc.wantRows)) + " observation(s)"
			if !strings.Contains(out, wantMessage) {
				t.Fatalf("report missing %q: %q", wantMessage, out)
			}
			for _, title := range tc.wantRows {
				if !strings.Contains(out, "  "+title) {
					t.Errorf("selection missing %s: %q", title, out)
				}
			}
			for _, title := range tc.notRows {
				if strings.Contains(out, "  "+title) {
					t.Errorf("selection must not contain %s: %q", title, out)
				}
			}
			if got := retiredTitles(t, ctx); len(got) != 0 {
				t.Fatalf("dry run wrote valid_until on: %v", got)
			}
		})
	}
}

// TestRetireOlderThanRejectsUnusableDuration covers the two ways the duration
// itself can be wrong: a negative value is refused as a usage error, and zero
// is "flag omitted" rather than "older than zero", so on its own it leaves the
// invocation without a selector and is refused too. Neither writes.
func TestRetireOlderThanRejectsUnusableDuration(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()
	seedRetireObservation(t, ctx, "keep me", "alpha", 2, 0)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "negative duration",
			args: []string{"retire", "--older-than", "-5m"},
			want: "must not be negative",
		},
		{
			name: "zero duration is not a selector",
			args: []string{"retire", "--older-than", "0s"},
			want: "at least one selector",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := dispatch(ctx, tc.args, &stdout, &stderr); code != exitUsage {
				t.Fatalf("exit = %d, want %d (stderr %q)", code, exitUsage, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr must explain %q, got %q", tc.want, stderr.String())
			}
			if got := retiredTitles(t, ctx); len(got) != 0 {
				t.Fatalf("a refused retire wrote valid_until on: %v", got)
			}
		})
	}
}

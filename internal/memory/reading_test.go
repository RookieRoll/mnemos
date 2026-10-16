package memory_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func readingFixture(t *testing.T) (*memory.Service, *storage.DB, time.Time) {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "reading.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	return memory.NewService(memory.Config{Store: db.Observations(), Clock: func() time.Time { return now }}), db, now
}

func seedReading(t *testing.T, db *storage.DB, now time.Time, ids ...string) {
	t.Helper()
	for _, id := range ids {
		o := memory.Observation{ID: id, AgentID: "default", Project: "project", Title: id, Content: "detail " + id, Type: memory.TypePattern, Importance: 5, CreatedAt: now.Add(-time.Hour), ValidFrom: now.Add(-24 * time.Hour), TrustTier: memory.TrustCurated, SourceKind: memory.SourceUser}
		if _, err := db.Observations().Restore(context.Background(), &o); err != nil {
			t.Fatal(err)
		}
	}
}

func detailAccess(t *testing.T, db *storage.DB, id string) int {
	t.Helper()
	var n int
	if err := db.SQL().QueryRow("SELECT access_count FROM observations WHERE id = ?", id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestScopedReadValidation(t *testing.T) {
	svc, _, _ := readingFixture(t)
	for name, in := range map[string]memory.GetManyInput{
		"missing project": {IDs: []string{"a"}},
		"missing IDs":     {Project: "project"},
		"too many":        {Project: "project", IDs: make([]string, 33)},
		"empty ID":        {Project: "project", IDs: []string{""}},
		"oversized ID":    {Project: "project", IDs: []string{strings.Repeat("x", 257)}},
		"negative budget": {Project: "project", IDs: []string{"a"}, MaxBytes: -1},
		"large budget":    {Project: "project", IDs: []string{"a"}, MaxBytes: memory.MaxReadBytes + 1},
		"blank agent":     {Project: "project", AgentID: "  ", IDs: []string{"a"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.GetMany(context.Background(), in); !errors.Is(err, memory.ErrInvalidReadInput) {
				t.Fatalf("invalid input error=%v", err)
			}
		})
	}
	minus, over := -1, 11
	for _, in := range []memory.NeighborhoodInput{{Project: "project"}, {ID: "a"}, {ID: "a", Project: "project", Before: &minus}, {ID: "a", Project: "project", After: &over}, {ID: "a", Project: "project", MaxBytes: -1}} {
		if _, err := svc.Neighborhood(context.Background(), in); !errors.Is(err, memory.ErrInvalidReadInput) {
			t.Fatalf("invalid neighborhood error=%v", err)
		}
	}
}

func TestScopedVisibilityAndHistoricalFactTime(t *testing.T) {
	svc, db, now := readingFixture(t)
	seedReading(t, db, now, "live", "raw", "old", "expired", "future", "other-project", "other-agent", "later-created")
	for _, change := range []struct {
		id, field string
		value     any
	}{
		{"raw", "trust_tier", "raw"}, {"old", "invalidated_at", now.Add(-time.Hour)}, {"old", "valid_until", now.Add(-time.Hour)},
		{"expired", "expires_at", now.Add(-time.Minute)}, {"future", "valid_from", now.Add(time.Hour)},
		{"other-project", "project", "secret-project"}, {"other-agent", "agent_id", "secret-agent"},
		{"later-created", "created_at", now.Add(time.Hour)},
	} {
		if _, err := db.SQL().Exec("UPDATE observations SET "+change.field+" = ? WHERE id = ?", change.value, change.id); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, id   string
		raw, stale bool
		asOf       time.Time
		want       memory.ReadStatus
	}{
		{"live", "live", false, false, time.Time{}, memory.ReadOK},
		{"quarantine", "raw", false, false, time.Time{}, memory.ReadUnavailable},
		{"raw opt in", "raw", true, false, time.Time{}, memory.ReadOK},
		{"invalidated", "old", false, false, time.Time{}, memory.ReadUnavailable},
		{"expired", "expired", false, false, time.Time{}, memory.ReadUnavailable},
		{"future fact", "future", false, false, time.Time{}, memory.ReadUnavailable},
		{"stale inspection", "future", false, true, time.Time{}, memory.ReadOK},
		{"historical fact", "old", false, false, now.Add(-2 * time.Hour), memory.ReadOK},
		{"no knowledge snapshot", "later-created", false, false, now.Add(-2 * time.Hour), memory.ReadOK},
		{"project hard selector", "other-project", true, true, time.Time{}, memory.ReadUnavailable},
		{"default agent hard selector", "other-agent", true, true, time.Time{}, memory.ReadUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := svc.GetMany(context.Background(), memory.GetManyInput{IDs: []string{tc.id}, Project: "project", IncludeRaw: tc.raw, IncludeStale: tc.stale, AsOf: tc.asOf})
			if err != nil {
				t.Fatal(err)
			}
			if out.Items[0].Status != tc.want {
				t.Fatalf("status=%s want=%s", out.Items[0].Status, tc.want)
			}
			if tc.want != memory.ReadOK && out.Items[0].Observation != nil {
				t.Fatal("filtered detail leaked")
			}
		})
	}
}

func TestBatchOrderBudgetAndDetailMeasurement(t *testing.T) {
	svc, db, now := readingFixture(t)
	seedReading(t, db, now, "good", "large-content", "large-metadata", "raw")
	for _, q := range []string{
		"UPDATE observations SET content = '" + strings.Repeat("x", 4000) + "' WHERE id='large-content'",
		"UPDATE observations SET rationale = '" + strings.Repeat("x", 4000) + "' WHERE id='large-metadata'",
		"UPDATE observations SET trust_tier = 'raw' WHERE id='raw'",
	} {
		if _, err := db.SQL().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := svc.GetMany(ctx, memory.GetManyInput{IDs: []string{"large-metadata", "good", "good", "large-content", "missing", "raw"}, Project: "project", MaxBytes: 2048})
	if err != nil {
		t.Fatal(err)
	}
	var got []memory.ReadStatus
	for _, item := range out.Items {
		got = append(got, item.Status)
		if item.Status != memory.ReadOK && item.Observation != nil {
			t.Fatal("omitted body leaked")
		}
	}
	want := []memory.ReadStatus{memory.ReadOmittedForSize, memory.ReadOK, memory.ReadOmittedForSize, memory.ReadUnavailable, memory.ReadUnavailable}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("statuses=%v", got)
	}
	if !reflect.DeepEqual([]string{out.Items[0].ID, out.Items[1].ID, out.Items[2].ID, out.Items[3].ID, out.Items[4].ID}, []string{"large-metadata", "good", "large-content", "missing", "raw"}) {
		t.Fatal("first-requested order changed")
	}
	encoded, err := json.Marshal(out.Items[1].Observation)
	if err != nil {
		t.Fatal(err)
	}
	if out.BytesReturned != len(encoded) || !out.ContentLimited || out.BytesReturned > out.MaxBytes {
		t.Fatal("incorrect complete-object byte accounting")
	}
	for _, id := range []string{"good", "large-content", "large-metadata", "raw"} {
		want := 0
		if id == "good" {
			want = 1
		}
		if n := detailAccess(t, db, id); n != want {
			t.Fatalf("access %s=%d want=%d", id, n, want)
		}
	}
	var events int
	if err := db.SQL().QueryRow("SELECT count(*) FROM injections").Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatal("detail reads created injection events")
	}
}

func TestNeighborhoodVisibleWindowAndSummaries(t *testing.T) {
	svc, db, now := readingFixture(t)
	ids := []string{"a", "b-raw", "c-old", "d", "e-anchor", "f", "g-other", "h-other-agent", "i"}
	seedReading(t, db, now, ids...)
	if err := db.Sessions().Insert(context.Background(), &session.Session{ID: "session", AgentID: "default", StartedAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec("UPDATE observations SET session_id='session'"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"UPDATE observations SET trust_tier='raw' WHERE id='b-raw'",
		"UPDATE observations SET invalidated_at=CURRENT_TIMESTAMP WHERE id='c-old'",
		"UPDATE observations SET project='secret' WHERE id='g-other'",
		"UPDATE observations SET agent_id='secret' WHERE id='h-other-agent'",
	} {
		if _, err := db.SQL().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	// Use fixed-time invalidation so tests do not depend on the workstation clock.
	if _, err := db.SQL().Exec("UPDATE observations SET invalidated_at=? WHERE id='c-old'", now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	limit := 2
	for repeat := 0; repeat < 2; repeat++ {
		out, err := svc.Neighborhood(context.Background(), memory.NeighborhoodInput{ID: "e-anchor", Project: "project", Before: &limit, After: &limit})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, item := range out.Items {
			got = append(got, item.ID)
		}
		if !reflect.DeepEqual(got, []string{"a", "d", "e-anchor", "f", "i"}) {
			t.Fatalf("window=%v", got)
		}
		if out.Window == nil || out.Window.BeforeCount != 2 || out.Window.AfterCount != 2 || out.Window.HasMoreBefore || out.Window.HasMoreAfter {
			t.Fatalf("bad visible limits: %+v", out.Window)
		}
	}
	inspected, err := svc.Neighborhood(context.Background(), memory.NeighborhoodInput{ID: "e-anchor", Project: "project", IncludeRaw: true, IncludeStale: true})
	if err != nil {
		t.Fatal(err)
	}
	var inspectedIDs []string
	for _, item := range inspected.Items {
		inspectedIDs = append(inspectedIDs, item.ID)
	}
	if !reflect.DeepEqual(inspectedIDs, []string{"b-raw", "c-old", "d", "e-anchor", "f", "i"}) {
		t.Fatalf("historical opt-in window=%v", inspectedIDs)
	}
	historical, err := svc.Neighborhood(context.Background(), memory.NeighborhoodInput{ID: "c-old", Project: "project", AsOf: now.Add(-2 * time.Hour)})
	if err != nil || historical.Items[0].Status == memory.ReadUnavailable {
		t.Fatalf("historical anchor unavailable: %+v %v", historical, err)
	}
	zero := 0
	anchorOnly, err := svc.Neighborhood(context.Background(), memory.NeighborhoodInput{ID: "e-anchor", Project: "project", Before: &zero, After: &zero})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchorOnly.Items) != 1 || !anchorOnly.Window.HasMoreBefore || !anchorOnly.Window.HasMoreAfter {
		t.Fatal("explicit zero became defaults")
	}
	hidden, err := svc.Neighborhood(context.Background(), memory.NeighborhoodInput{ID: "b-raw", Project: "project"})
	if err != nil {
		t.Fatal(err)
	}
	if hidden.Window != nil || len(hidden.Items) != 1 || hidden.Items[0].Status != memory.ReadUnavailable || hidden.Items[0].Summary != nil {
		t.Fatal("hidden anchor leaked siblings")
	}
	for _, id := range ids {
		if detailAccess(t, db, id) != 0 {
			t.Fatalf("summary browsing changed access for %s", id)
		}
	}
	seedReading(t, db, now, "standalone")
	if _, err := db.SQL().Exec("UPDATE observations SET title=?, content=? WHERE id='standalone'", strings.Repeat("中", 200), strings.Repeat("文", 300)); err != nil {
		t.Fatal(err)
	}
	one, err := svc.Neighborhood(context.Background(), memory.NeighborhoodInput{ID: "standalone", Project: "project"})
	if err != nil {
		t.Fatal(err)
	}
	summary := one.Items[0].Summary
	if !one.NoSession || one.Window != nil || !summary.TitleTruncated || !summary.SnippetTruncated || !utf8.ValidString(summary.Title) || !utf8.ValidString(summary.Snippet) || len(summary.Title) > 256 || len(summary.Snippet) > 512 {
		t.Fatalf("invalid bounded no-session summary: %+v", one)
	}
}

type accessFailureStore struct {
	memory.Store
	memory.ScopedReadStore
}

func (accessFailureStore) IncrementAccessMany(context.Context, []string, time.Time) error {
	return errors.New("bookkeeping failure")
}

func TestDetailAccessFailureReturnsNoSuccess(t *testing.T) {
	_, db, now := readingFixture(t)
	seedReading(t, db, now, "a")
	store := accessFailureStore{Store: db.Observations(), ScopedReadStore: db.Observations().(memory.ScopedReadStore)}
	svc := memory.NewService(memory.Config{Store: store, Clock: func() time.Time { return now }})
	out, err := svc.GetMany(context.Background(), memory.GetManyInput{IDs: []string{"a"}, Project: "project"})
	if err == nil || out != nil || detailAccess(t, db, "a") != 0 {
		t.Fatal("bookkeeping failure advertised successful details")
	}
}

func TestNeighborhoodOversizedLegacyIdentifierFailsBound(t *testing.T) {
	svc, db, now := readingFixture(t)
	if err := db.Sessions().Insert(context.Background(), &session.Session{ID: "s", AgentID: "default", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	seedReading(t, db, now, "anchor", strings.Repeat("z", 4000))
	if _, err := db.SQL().Exec("UPDATE observations SET session_id='s'"); err != nil {
		t.Fatal(err)
	}
	out, err := svc.Neighborhood(context.Background(), memory.NeighborhoodInput{ID: "anchor", Project: "project", MaxBytes: 1})
	if err == nil || out != nil {
		t.Fatal("unbounded outer identifier leaked beyond JSON-object budget")
	}
}

func TestBatchOmitsEmbeddingsFromDetails(t *testing.T) {
	svc, db, now := readingFixture(t)
	seedReading(t, db, now, "vector")
	if err := db.Observations().UpdateEmbedding(context.Background(), "vector", "large-model", make([]float32, 10000)); err != nil {
		t.Fatal(err)
	}
	out, err := svc.GetMany(context.Background(), memory.GetManyInput{IDs: []string{"vector"}, Project: "project", MaxBytes: 2048})
	if err != nil {
		t.Fatal(err)
	}
	if out.Items[0].Status != memory.ReadOK || len(out.Items[0].Observation.Embedding) > 0 || out.Items[0].Observation.EmbeddingModel != "" {
		t.Fatal("embedding payload affected progressive read")
	}
}

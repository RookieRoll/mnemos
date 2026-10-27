package memory_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// retireStore is the test adapter for memory.RetirementStore. It applies a
// batch the way the production SQLite adapter is contracted to: one
// transaction, every selected id updated inside it, and rows that stopped
// being live skipped. Keeping the SQL here lets the service tests exercise
// real bi-temporal state without editing the storage package, which this lane
// does not own.
type retireStore struct {
	memory.Store
	memory.EvolutionStore

	sql   *sql.DB
	calls int
	ids   []string
	err   error
	// beforeGuarded runs once, between the service's selection read and the
	// batch write, so a test can change a row inside the select-then-write window
	// the guarded capability closes.
	beforeGuarded func()
}

// RetireObservations implements the narrow memory.RetirementStore for the tests.
func (r *retireStore) RetireObservations(ctx context.Context, ids []string, validUntil time.Time) (int64, error) {
	r.calls++
	r.ids = append([]string(nil), ids...)
	if r.err != nil {
		return 0, r.err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := r.sql.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `UPDATE observations
		   SET valid_until = ?
		 WHERE id = ? AND invalidated_at IS NULL
		   AND (valid_until IS NULL OR valid_until > ?)`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = stmt.Close() }()
	var retired int64
	for _, id := range ids {
		res, err := stmt.ExecContext(ctx, validUntil.UTC(), id, validUntil.UTC())
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		retired += n
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return retired, nil
}

// RetireObservationsGuarded implements memory.GuardedRetirementStore by
// delegating to the real storage adapter, so the service tests exercise the same
// selector re-check and id reporting production uses.
func (r *retireStore) RetireObservationsGuarded(ctx context.Context, ids []string, in memory.RetireInput, validUntil time.Time) ([]string, error) {
	r.calls++
	r.ids = append([]string(nil), ids...)
	if r.beforeGuarded != nil {
		r.beforeGuarded()
		r.beforeGuarded = nil
	}
	if r.err != nil {
		return nil, r.err
	}
	guarded, ok := r.Store.(memory.GuardedRetirementStore)
	if !ok {
		return nil, errors.New("embedded store does not offer guarded retirement")
	}
	return guarded.RetireObservationsGuarded(ctx, ids, in, validUntil)
}

// plainRetireStore exposes only the memory.Store surface, so the service sees
// a store without the bulk-retirement capability.
type plainRetireStore struct {
	memory.Store
}

type retireFixture struct {
	svc   *memory.Service
	store *retireStore
	now   time.Time
}

func newRetireFixture(t *testing.T) *retireFixture {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "retire.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Date(2026, 5, 20, 9, 30, 0, 0, time.UTC)
	obs := db.Observations()
	store := &retireStore{
		Store:          obs,
		EvolutionStore: obs.(memory.EvolutionStore),
		sql:            db.SQL(),
	}
	return &retireFixture{
		svc:   memory.NewService(memory.Config{Store: store, Clock: func() time.Time { return now }}),
		store: store,
		now:   now,
	}
}

type retireRow struct {
	key         string
	project     string
	importance  int
	access      int
	validFrom   *time.Time
	validUntil  *time.Time
	invalidated bool
}

// seed inserts one observation through the service and then nudges the fields
// SaveInput cannot express (access count, system invalidation).
func (f *retireFixture) seed(t *testing.T, row retireRow) string {
	t.Helper()
	ctx := context.Background()
	saved, err := f.svc.Save(ctx, memory.SaveInput{
		Title:      "retire " + row.key,
		Content:    "retire fixture " + row.key,
		Type:       memory.TypePattern,
		Project:    row.project,
		Importance: row.importance,
		ValidFrom:  row.validFrom,
		ValidUntil: row.validUntil,
	})
	if err != nil {
		t.Fatalf("seed %q: %v", row.key, err)
	}
	id := saved.Observation.ID
	for i := 0; i < row.access; i++ {
		if err := f.store.BumpAccess(ctx, id); err != nil {
			t.Fatalf("bump access for %q: %v", row.key, err)
		}
	}
	if row.invalidated {
		if err := f.store.Invalidate(ctx, id, f.now.Add(time.Hour)); err != nil {
			t.Fatalf("invalidate %q: %v", row.key, err)
		}
	}
	return id
}

type retireState struct {
	validUntil  *time.Time
	invalidated *time.Time
	access      int
	hash        string
	title       string
	content     string
}

func snapshotRetireRows(t *testing.T, store memory.Store) map[string]retireState {
	t.Helper()
	rows, err := store.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]retireState, len(rows))
	for _, o := range rows {
		out[o.ID] = retireState{
			validUntil:  o.ValidUntil,
			invalidated: o.InvalidatedAt,
			access:      o.AccessCount,
			hash:        o.ContentHash,
			title:       o.Title,
			content:     o.Content,
		}
	}
	return out
}

func sameRetireTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func assertRetireRowsUnchanged(t *testing.T, before, after map[string]retireState) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("row count changed: %d -> %d", len(before), len(after))
	}
	for id, want := range before {
		got, ok := after[id]
		if !ok {
			t.Fatalf("observation %s disappeared", id)
		}
		if !sameRetireTime(want.validUntil, got.validUntil) {
			t.Errorf("%s: valid_until changed (%v -> %v)", id, want.validUntil, got.validUntil)
		}
		if !sameRetireTime(want.invalidated, got.invalidated) {
			t.Errorf("%s: invalidated_at changed (%v -> %v)", id, want.invalidated, got.invalidated)
		}
		if want.access != got.access {
			t.Errorf("%s: access_count changed (%d -> %d)", id, want.access, got.access)
		}
		if want.hash != got.hash || want.title != got.title || want.content != got.content {
			t.Errorf("%s: indexed text changed", id)
		}
	}
}

func retireSampleIDs(sample []memory.RetireSample) []string {
	ids := make([]string, len(sample))
	for i := range sample {
		ids[i] = sample[i].ID
	}
	return ids
}

func sameRetireStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameRetireSamples(a, b []memory.RetireSample) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSelectRetirePredicates pins the shared selection predicate: importance
// at the threshold, the access-count boundary, already-retired rows, and the
// optional project filter. Every case also asserts the preview path wrote
// nothing.
func TestSelectRetirePredicates(t *testing.T) {
	cases := []struct {
		name    string
		in      memory.RetireInput
		want    []string
		wantErr bool
	}{
		{
			name: "project filter keeps only that project",
			in:   memory.RetireInput{Project: "alpha"},
			want: []string{"alpha-low", "alpha-high", "alpha-accessed", "alpha-future"},
		},
		{
			name: "importance exactly at the threshold is selected, above is not",
			in:   memory.RetireInput{ImportanceMax: 3},
			want: []string{"alpha-low", "alpha-accessed", "alpha-future", "beta-low"},
		},
		{
			name: "no access selects access_count zero only",
			in:   memory.RetireInput{NoAccess: true},
			want: []string{"alpha-low", "alpha-high", "alpha-future", "beta-low", "global-mid"},
		},
		{
			name: "selectors combine",
			in:   memory.RetireInput{Project: "alpha", ImportanceMax: 3, NoAccess: true},
			want: []string{"alpha-low", "alpha-future"},
		},
		{
			name: "project without matches selects nothing",
			in:   memory.RetireInput{Project: "gamma"},
			want: nil,
		},
		{
			name:    "no selector is refused",
			in:      memory.RetireInput{},
			wantErr: true,
		},
		{
			name:    "non-positive importance alone is not a selector",
			in:      memory.RetireInput{ImportanceMax: -1},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRetireFixture(t)
			past := f.now.Add(-2 * time.Hour)
			nowExactly := f.now
			future := f.now.Add(2 * time.Hour)
			rows := []retireRow{
				{key: "alpha-low", project: "alpha", importance: 3},
				{key: "alpha-high", project: "alpha", importance: 4},
				{key: "alpha-accessed", project: "alpha", importance: 2, access: 1},
				{key: "alpha-future", project: "alpha", importance: 2, validUntil: &future},
				{key: "alpha-retired", project: "alpha", importance: 1, validUntil: &past},
				{key: "alpha-now", project: "alpha", importance: 2, validUntil: &nowExactly},
				{key: "alpha-invalidated", project: "alpha", importance: 1, invalidated: true},
				{key: "beta-low", project: "beta", importance: 1},
				{key: "global-mid", importance: 5},
			}
			ids := make(map[string]string, len(rows))
			for _, row := range rows {
				ids[row.key] = f.seed(t, row)
			}
			before := snapshotRetireRows(t, f.store)

			report, err := f.svc.SelectRetire(context.Background(), tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected a selector validation error")
				}
				if report.Count != 0 || len(report.Sample) != 0 {
					t.Errorf("refused selection must report nothing, got %+v", report)
				}
			} else {
				if err != nil {
					t.Fatalf("SelectRetire: %v", err)
				}
				wantIDs := make([]string, 0, len(tc.want))
				for _, key := range tc.want {
					wantIDs = append(wantIDs, ids[key])
				}
				sort.Strings(wantIDs)
				gotIDs := retireSampleIDs(report.Sample)
				sort.Strings(gotIDs)
				if report.Count != len(wantIDs) {
					t.Errorf("count = %d, want %d", report.Count, len(wantIDs))
				}
				if !sameRetireStrings(gotIDs, wantIDs) {
					t.Errorf("sample ids = %v, want %v", gotIDs, wantIDs)
				}
			}
			if f.store.calls != 0 {
				t.Errorf("selection must never write, store saw %d batch calls", f.store.calls)
			}
			assertRetireRowsUnchanged(t, before, snapshotRetireRows(t, f.store))
		})
	}
}

// TestRetireReportCountsAndSamples pins the count/sample contract and that a
// dry-run preview and the real run print the identical report.
func TestRetireReportCountsAndSamples(t *testing.T) {
	cases := []struct {
		name       string
		matches    int
		wantCount  int
		wantSample int
	}{
		{name: "no matches", matches: 0, wantCount: 0, wantSample: 0},
		{name: "all matches fit the sample", matches: 3, wantCount: 3, wantSample: 3},
		{name: "sample is bounded at five", matches: 7, wantCount: 7, wantSample: 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRetireFixture(t)
			ctx := context.Background()
			titles := make(map[string]string, tc.matches)
			var ids []string
			for i := 0; i < tc.matches; i++ {
				row := retireRow{key: fmt.Sprintf("match-%d", i), project: "alpha", importance: 4}
				id := f.seed(t, row)
				ids = append(ids, id)
				titles[id] = "retire " + row.key
			}
			f.seed(t, retireRow{key: "other", project: "beta", importance: 9})
			in := memory.RetireInput{Project: "alpha"}

			preview, err := f.svc.SelectRetire(ctx, in)
			if err != nil {
				t.Fatalf("SelectRetire: %v", err)
			}
			if f.store.calls != 0 {
				t.Fatalf("preview wrote: %d batch calls", f.store.calls)
			}
			result, err := f.svc.Retire(ctx, in)
			if err != nil {
				t.Fatalf("Retire: %v", err)
			}
			wantIDs := append([]string(nil), ids...)
			sort.Strings(wantIDs)
			for name, report := range map[string]memory.RetireReport{"preview": preview, "run": result} {
				if report.Count != tc.wantCount {
					t.Errorf("%s count = %d, want %d", name, report.Count, tc.wantCount)
				}
				if len(report.Sample) != tc.wantSample {
					t.Errorf("%s sample length = %d, want %d", name, len(report.Sample), tc.wantSample)
				}
				for i, sample := range report.Sample {
					if sample.ID != wantIDs[i] {
						t.Errorf("%s sample[%d] id = %s, want %s", name, i, sample.ID, wantIDs[i])
					}
					if sample.Title != titles[sample.ID] {
						t.Errorf("%s sample[%d] title = %q, want %q", name, i, sample.Title, titles[sample.ID])
					}
				}
			}
			if preview.Count != result.Count || !sameRetireSamples(preview.Sample, result.Sample) {
				t.Errorf("preview and run reports diverge: preview=%+v run=%+v", preview, result)
			}
			wantCalls := 1
			if tc.matches == 0 {
				wantCalls = 0
			}
			if f.store.calls != wantCalls {
				t.Errorf("batch calls = %d, want %d", f.store.calls, wantCalls)
			}
		})
	}
}

// TestRetirePreservesBitemporalState is the retirement contract: only
// valid_until moves, the row survives, invalidated_at and the indexed text are
// untouched, no supersedes link appears, a historical AsOf query still sees
// the row, and a second run cannot retire it again.
func TestRetirePreservesBitemporalState(t *testing.T) {
	f := newRetireFixture(t)
	ctx := context.Background()
	early := f.now.Add(-24 * time.Hour)
	titles := map[string]string{}
	var matchIDs []string
	for i := 0; i < 3; i++ {
		row := retireRow{key: fmt.Sprintf("match-%d", i), project: "alpha", importance: 2}
		if i == 0 {
			row.validFrom = &early
		}
		id := f.seed(t, row)
		matchIDs = append(matchIDs, id)
		titles[id] = "retire " + row.key
	}
	otherIDs := []string{
		f.seed(t, retireRow{key: "high", project: "alpha", importance: 9}),
		f.seed(t, retireRow{key: "accessed", project: "alpha", importance: 1, access: 2}),
		f.seed(t, retireRow{key: "beta", project: "beta", importance: 1}),
	}
	before := snapshotRetireRows(t, f.store)
	in := memory.RetireInput{Project: "alpha", ImportanceMax: 2, NoAccess: true}

	preview, err := f.svc.SelectRetire(ctx, in)
	if err != nil {
		t.Fatalf("SelectRetire: %v", err)
	}
	if preview.Count != len(matchIDs) {
		t.Fatalf("preview count = %d, want %d", preview.Count, len(matchIDs))
	}
	assertRetireRowsUnchanged(t, before, snapshotRetireRows(t, f.store))

	result, err := f.svc.Retire(ctx, in)
	if err != nil {
		t.Fatalf("Retire: %v", err)
	}
	if result.Count != len(matchIDs) {
		t.Errorf("retired count = %d, want %d", result.Count, len(matchIDs))
	}
	if !sameRetireSamples(preview.Sample, result.Sample) {
		t.Errorf("preview sample %+v differs from run sample %+v", preview.Sample, result.Sample)
	}
	if f.store.calls != 1 {
		t.Errorf("retirement must be one batch write, got %d calls", f.store.calls)
	}
	selectedIDs := append([]string(nil), f.store.ids...)
	sort.Strings(selectedIDs)
	wantIDs := append([]string(nil), matchIDs...)
	sort.Strings(wantIDs)
	if !sameRetireStrings(selectedIDs, wantIDs) {
		t.Errorf("batch ids = %v, want %v", selectedIDs, wantIDs)
	}

	rows, err := f.store.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]memory.Observation, len(rows))
	for _, o := range rows {
		byID[o.ID] = o
	}
	for _, id := range matchIDs {
		o := byID[id]
		if o.ValidUntil == nil || !o.ValidUntil.Equal(f.now) {
			t.Errorf("%s: valid_until = %v, want retirement instant %v", id, o.ValidUntil, f.now)
		}
		if o.InvalidatedAt != nil {
			t.Errorf("%s: invalidated_at must stay nil, got %v", id, o.InvalidatedAt)
		}
		if o.ContentHash != before[id].hash {
			t.Errorf("%s: content_hash changed", id)
		}
		if o.Title != before[id].title || o.Content != before[id].content {
			t.Errorf("%s: indexed text changed", id)
		}
		if o.Live(f.now) {
			t.Errorf("%s: retired observation must not be live at the retirement instant", id)
		}
		if !o.Live(f.now.Add(-time.Minute)) {
			t.Errorf("%s: retired observation must still be live before the retirement instant", id)
		}
	}
	for _, id := range otherIDs {
		if !sameRetireTime(byID[id].ValidUntil, before[id].validUntil) {
			t.Errorf("%s: non-matching row changed", id)
		}
	}

	again, err := f.svc.Retire(ctx, in)
	if err != nil {
		t.Fatalf("second Retire: %v", err)
	}
	if again.Count != 0 || len(again.Sample) != 0 {
		t.Errorf("already retired rows must not match again, got %+v", again)
	}
	if f.store.calls != 1 {
		t.Errorf("second run must not write, batch calls = %d", f.store.calls)
	}

	links, err := f.store.ListLinks(ctx, memory.LinkSupersedes, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Errorf("retirement must not write supersedes links, got %d", len(links))
	}

	retiredID := matchIDs[0]
	hitsBefore, err := f.svc.Search(ctx, memory.SearchInput{Query: "fixture", AsOf: f.now.Add(-time.Hour)})
	if err != nil {
		t.Fatalf("AsOf before: %v", err)
	}
	foundBefore := false
	for _, hit := range hitsBefore {
		foundBefore = foundBefore || hit.Observation.ID == retiredID
	}
	if !foundBefore {
		t.Error("historical query before the retirement instant must return the retired observation")
	}
	hitsAfter, err := f.svc.Search(ctx, memory.SearchInput{Query: "fixture", AsOf: f.now.Add(time.Hour)})
	if err != nil {
		t.Fatalf("AsOf after: %v", err)
	}
	for _, hit := range hitsAfter {
		if hit.Observation.ID == retiredID {
			t.Error("query after the retirement instant must not return the retired observation")
		}
	}
}

// TestRetireRefusesUnsafeOrUnsupportedWrites covers the two error paths that
// must never leave a partial write: no selector, and a failing or missing
// batch capability.
func TestRetireRefusesUnsafeOrUnsupportedWrites(t *testing.T) {
	f := newRetireFixture(t)
	ctx := context.Background()
	f.seed(t, retireRow{key: "match", project: "alpha", importance: 2})
	before := snapshotRetireRows(t, f.store)

	if _, err := f.svc.Retire(ctx, memory.RetireInput{}); err == nil {
		t.Fatal("retire without selectors must be refused")
	}
	if _, err := f.svc.SelectRetire(ctx, memory.RetireInput{ImportanceMax: 0}); err == nil {
		t.Fatal("select without selectors must be refused")
	}
	if f.store.calls != 0 {
		t.Fatalf("refused calls wrote: %d batch calls", f.store.calls)
	}
	assertRetireRowsUnchanged(t, before, snapshotRetireRows(t, f.store))

	boom := errors.New("batch write failed")
	f.store.err = boom
	if _, err := f.svc.Retire(ctx, memory.RetireInput{Project: "alpha"}); err == nil || !errors.Is(err, boom) {
		t.Fatalf("want wrapped batch error, got %v", err)
	}
	assertRetireRowsUnchanged(t, before, snapshotRetireRows(t, f.store))

	f.store.err = nil
	plain := &plainRetireStore{Store: f.store}
	svc := memory.NewService(memory.Config{Store: plain, Clock: func() time.Time { return f.now }})
	if _, err := svc.Retire(ctx, memory.RetireInput{Project: "alpha"}); err == nil {
		t.Fatal("a store without RetirementStore must be refused")
	}
	assertRetireRowsUnchanged(t, before, snapshotRetireRows(t, f.store))
}

// backdate moves an observation's created_at into the past. Save stamps
// created_at from the service clock, so an age fixture can only come from the
// store itself; every other column stays as saved.
func (f *retireFixture) backdate(t *testing.T, id string, createdAt time.Time) {
	t.Helper()
	if _, err := f.store.sql.Exec(
		`UPDATE observations SET created_at = ? WHERE id = ?`,
		createdAt.UTC(), id,
	); err != nil {
		t.Fatalf("backdate %s: %v", id, err)
	}
}

// TestSelectRetireOlderThan pins the created_at selector: a row created
// strictly before the cutoff is selected, a row created exactly at it is not,
// the zero value disables the predicate (and is not a selector on its own), and
// the cutoff ANDs with the other selectors. Every case also asserts the preview
// wrote nothing.
func TestSelectRetireOlderThan(t *testing.T) {
	cases := []struct {
		name    string
		build   func(cutoff time.Time) memory.RetireInput
		want    []string
		wantErr bool
	}{
		{
			name:  "row created exactly at the cutoff is excluded",
			build: func(cutoff time.Time) memory.RetireInput { return memory.RetireInput{OlderThan: cutoff} },
			want:  []string{"older-alpha", "older-beta"},
		},
		{
			name: "cutoff further back selects nothing older",
			build: func(cutoff time.Time) memory.RetireInput {
				return memory.RetireInput{OlderThan: cutoff.Add(-6 * time.Hour)}
			},
			want: nil,
		},
		{
			name: "cutoff past every row selects them all",
			build: func(cutoff time.Time) memory.RetireInput {
				return memory.RetireInput{OlderThan: cutoff.Add(100 * time.Hour)}
			},
			want: []string{"older-alpha", "boundary-alpha", "newer-alpha", "older-beta"},
		},
		{
			name: "cutoff ANDs with project",
			build: func(cutoff time.Time) memory.RetireInput {
				return memory.RetireInput{OlderThan: cutoff, Project: "alpha"}
			},
			want: []string{"older-alpha"},
		},
		{
			name: "cutoff ANDs with importance",
			build: func(cutoff time.Time) memory.RetireInput {
				return memory.RetireInput{OlderThan: cutoff, ImportanceMax: 1}
			},
			want: []string{"older-beta"},
		},
		{
			name: "cutoff ANDs with no-access",
			build: func(cutoff time.Time) memory.RetireInput {
				return memory.RetireInput{OlderThan: cutoff, NoAccess: true}
			},
			want: []string{"older-alpha", "older-beta"},
		},
		{
			name:  "zero cutoff disables the predicate",
			build: func(time.Time) memory.RetireInput { return memory.RetireInput{Project: "alpha"} },
			want:  []string{"older-alpha", "boundary-alpha", "newer-alpha"},
		},
		{
			name:    "zero cutoff alone is not a selector",
			build:   func(time.Time) memory.RetireInput { return memory.RetireInput{OlderThan: time.Time{}} },
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRetireFixture(t)
			cutoff := f.now.Add(-24 * time.Hour)
			rows := []struct {
				key        string
				project    string
				importance int
				access     int
				created    time.Time
			}{
				{key: "older-alpha", project: "alpha", importance: 2, created: cutoff.Add(-time.Hour)},
				{key: "boundary-alpha", project: "alpha", importance: 2, created: cutoff},
				{key: "newer-alpha", project: "alpha", importance: 2, created: cutoff.Add(time.Hour)},
				{key: "older-beta", project: "beta", importance: 1, created: cutoff.Add(-90 * time.Minute)},
			}
			ids := make(map[string]string, len(rows))
			for _, row := range rows {
				ids[row.key] = f.seed(t, retireRow{
					key: row.key, project: row.project, importance: row.importance, access: row.access,
				})
				f.backdate(t, ids[row.key], row.created)
			}
			before := snapshotRetireRows(t, f.store)
			in := tc.build(cutoff)

			report, err := f.svc.SelectRetire(context.Background(), in)
			if tc.wantErr {
				if err == nil {
					t.Fatal("an input without a real selector must be refused")
				}
				if report.Count != 0 || len(report.Sample) != 0 {
					t.Errorf("refused selection must report nothing, got %+v", report)
				}
			} else {
				if err != nil {
					t.Fatalf("SelectRetire: %v", err)
				}
				wantIDs := make([]string, 0, len(tc.want))
				for _, key := range tc.want {
					wantIDs = append(wantIDs, ids[key])
				}
				sort.Strings(wantIDs)
				gotIDs := retireSampleIDs(report.Sample)
				sort.Strings(gotIDs)
				if report.Count != len(wantIDs) {
					t.Errorf("count = %d, want %d", report.Count, len(wantIDs))
				}
				if !sameRetireStrings(gotIDs, wantIDs) {
					t.Errorf("sample ids = %v, want %v", gotIDs, wantIDs)
				}
			}
			if f.store.calls != 0 {
				t.Errorf("selection must never write, store saw %d batch calls", f.store.calls)
			}
			assertRetireRowsUnchanged(t, before, snapshotRetireRows(t, f.store))
		})
	}
}

// TestRetireReportReflectsTheActualWrite pins the count-and-sample contract
// against the select-then-write window: a row that gains an access after the
// selection read stops matching --no-access, the guarded UPDATE skips it, and it
// must vanish from both the count and the sample. A report built from the
// pre-write selection would still name it.
func TestRetireReportReflectsTheActualWrite(t *testing.T) {
	f := newRetireFixture(t)
	ctx := context.Background()
	var ids []string
	for i := 0; i < 3; i++ {
		ids = append(ids, f.seed(t, retireRow{
			key: fmt.Sprintf("race-%d", i), project: "alpha", importance: 2,
		}))
	}
	skipped := ids[0]
	f.store.beforeGuarded = func() {
		if err := f.store.BumpAccess(ctx, skipped); err != nil {
			t.Errorf("bump access inside the select-then-write window: %v", err)
		}
	}

	report, err := f.svc.Retire(ctx, memory.RetireInput{Project: "alpha", NoAccess: true})
	if err != nil {
		t.Fatalf("Retire: %v", err)
	}
	if report.Count != 2 {
		t.Fatalf("count = %d, want the two rows the write actually retired", report.Count)
	}
	if len(report.Sample) != 2 {
		t.Fatalf("sample length = %d, want 2", len(report.Sample))
	}
	var retired int
	if err := f.store.sql.QueryRow(
		`SELECT COUNT(*) FROM observations WHERE project = 'alpha' AND valid_until IS NOT NULL`).Scan(&retired); err != nil {
		t.Fatal(err)
	}
	if retired != report.Count {
		t.Fatalf("report count = %d, but %d rows carry valid_until", report.Count, retired)
	}
	for _, sample := range report.Sample {
		if sample.ID == skipped {
			t.Fatalf("sample names %s, a row the write skipped", skipped)
		}
	}
}

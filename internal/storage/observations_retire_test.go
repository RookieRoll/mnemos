package storage_test

import (
	"context"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// retirementStore asserts the observation store still provides the bulk
// fact-time retirement capability and hands it back typed. The assertion is
// explicit because memory.Service.Retire refuses a store without it, so a
// missing adapter would surface as a user-facing CLI error rather than a
// compile failure.
func retirementStore(t *testing.T, db *storage.DB) memory.RetirementStore {
	t.Helper()
	rs, ok := db.Observations().(memory.RetirementStore)
	if !ok {
		t.Fatal("storage observations store must implement memory.RetirementStore")
	}
	return rs
}

// retireSeed is one observation to insert before a retirement attempt: how it
// looked when the retirement started. project and importance default to projX
// and 3; validFrom defaults to the row's created_at, 24h before the retirement
// instant.
type retireSeed struct {
	id          string
	project     string
	importance  int
	access      int
	validFrom   *time.Time
	validUntil  *time.Time
	invalidated bool
}

// retireObsState is the subset of a row retirement is allowed to leave alone.
type retireObsState struct {
	validUntil  *time.Time
	invalidated *time.Time
	created     time.Time
	access      int
	title       string
	content     string
	hash        string
}

func seedRetireObservations(t *testing.T, store memory.Store, at time.Time, seeds []retireSeed) {
	t.Helper()
	ctx := context.Background()
	for _, seed := range seeds {
		created := at.Add(-24 * time.Hour)
		validFrom := created
		if seed.validFrom != nil {
			validFrom = *seed.validFrom
		}
		project := seed.project
		if project == "" {
			project = "projX"
		}
		importance := seed.importance
		if importance == 0 {
			importance = 3
		}
		o := &memory.Observation{
			ID:            seed.id,
			AgentID:       "agentX",
			Project:       project,
			Title:         "retire " + seed.id,
			Content:       "retirement fixture " + seed.id,
			Type:          memory.TypePattern,
			Importance:    importance,
			AccessCount:   seed.access,
			CreatedAt:     created,
			ValidFrom:     validFrom,
			ValidUntil:    seed.validUntil,
			ContentHash:   "hash-" + seed.id,
			InvalidatedAt: nilIfNot(seed.invalidated, at.Add(-time.Hour)),
		}
		if err := store.Insert(ctx, o); err != nil {
			t.Fatalf("seed %s: %v", seed.id, err)
		}
	}
}

func snapshotRetireObservations(t *testing.T, store memory.Store) map[string]retireObsState {
	t.Helper()
	rows, err := store.Export(context.Background())
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	out := make(map[string]retireObsState, len(rows))
	for _, o := range rows {
		out[o.ID] = retireObsState{
			validUntil:  o.ValidUntil,
			invalidated: o.InvalidatedAt,
			created:     o.CreatedAt,
			access:      o.AccessCount,
			title:       o.Title,
			content:     o.Content,
			hash:        o.ContentHash,
		}
	}
	return out
}

func nilIfNot(set bool, v time.Time) *time.Time {
	if !set {
		return nil
	}
	return &v
}

func sameRetireInstant(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// TestRetireObservations pins the storage adapter contract: one write sets
// valid_until on exactly the listed live rows, skips rows that stopped being
// live (system-invalidated, already retired at or before the retirement instant,
// or not yet effective), counts only rows it actually wrote, treats an empty id
// list as a no-op, and leaves every other column alone.
func TestRetireObservations(t *testing.T) {
	at := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	earlier := at.Add(-2 * time.Hour)
	later := at.Add(2 * time.Hour)

	cases := []struct {
		name    string
		seeds   []retireSeed
		ids     []string
		want    int64
		retired []string
	}{
		{
			name:    "live batch is retired in full",
			seeds:   []retireSeed{{id: "a"}, {id: "b"}, {id: "c"}},
			ids:     []string{"a", "b", "c"},
			want:    3,
			retired: []string{"a", "b", "c"},
		},
		{
			name:    "already retired rows are skipped",
			seeds:   []retireSeed{{id: "live"}, {id: "retired", validUntil: &earlier}},
			ids:     []string{"live", "retired"},
			want:    1,
			retired: []string{"live"},
		},
		{
			name:    "row retired at the same instant is skipped",
			seeds:   []retireSeed{{id: "live"}, {id: "boundary", validUntil: &at}},
			ids:     []string{"live", "boundary"},
			want:    1,
			retired: []string{"live"},
		},
		{
			name:    "still-live future boundary is retired",
			seeds:   []retireSeed{{id: "future", validUntil: &later}},
			ids:     []string{"future"},
			want:    1,
			retired: []string{"future"},
		},
		{
			name:    "row not yet effective is skipped",
			seeds:   []retireSeed{{id: "live"}, {id: "not-yet", validFrom: &later}},
			ids:     []string{"live", "not-yet"},
			want:    1,
			retired: []string{"live"},
		},
		{
			name:    "row effective at the retirement instant is retired",
			seeds:   []retireSeed{{id: "boundary", validFrom: &at}},
			ids:     []string{"boundary"},
			want:    1,
			retired: []string{"boundary"},
		},
		{
			name:    "system-invalidated rows are skipped",
			seeds:   []retireSeed{{id: "live"}, {id: "invalidated", invalidated: true}},
			ids:     []string{"live", "invalidated"},
			want:    1,
			retired: []string{"live"},
		},
		{
			name:  "empty id list writes nothing",
			seeds: []retireSeed{{id: "untouched"}},
			ids:   nil,
			want:  0,
		},
		{
			name: "partial hit counts only the rows written",
			seeds: []retireSeed{
				{id: "live-1"}, {id: "live-2"}, {id: "live-3"},
				{id: "retired", validUntil: &earlier},
				{id: "invalidated", invalidated: true},
			},
			ids:     []string{"live-1", "live-2", "live-3", "retired", "invalidated", "absent"},
			want:    3,
			retired: []string{"live-1", "live-2", "live-3"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			store := db.Observations()
			seedRetireObservations(t, store, at, tc.seeds)
			before := snapshotRetireObservations(t, store)

			got, err := retirementStore(t, db).RetireObservations(context.Background(), tc.ids, at)
			if err != nil {
				t.Fatalf("RetireObservations: %v", err)
			}
			if got != tc.want {
				t.Errorf("retired rows = %d, want %d", got, tc.want)
			}

			after := snapshotRetireObservations(t, store)
			if len(after) != len(before) {
				t.Fatalf("row count changed: %d -> %d (retirement must never delete)", len(before), len(after))
			}
			retired := make(map[string]bool, len(tc.retired))
			for _, id := range tc.retired {
				retired[id] = true
			}
			for id, was := range before {
				now := after[id]
				wantValidUntil := was.validUntil
				if retired[id] {
					wantValidUntil = &at
				}
				if !sameRetireInstant(wantValidUntil, now.validUntil) {
					t.Errorf("%s: valid_until = %v, want %v", id, now.validUntil, wantValidUntil)
				}
				if !sameRetireInstant(was.invalidated, now.invalidated) {
					t.Errorf("%s: invalidated_at changed (%v -> %v)", id, was.invalidated, now.invalidated)
				}
				if !was.created.Equal(now.created) {
					t.Errorf("%s: created_at changed (%v -> %v)", id, was.created, now.created)
				}
				if was.access != now.access {
					t.Errorf("%s: access_count changed (%d -> %d)", id, was.access, now.access)
				}
				if was.title != now.title || was.content != now.content || was.hash != now.hash {
					t.Errorf("%s: indexed text or content hash changed", id)
				}
			}
		})
	}
}

// TestRetireObservationsIsBulkWrite pins that one call retires a whole batch:
// a single statement, so either every listed live row is stamped or, when the
// write fails, none is. The failing case uses a cancelled context to keep the
// transaction from committing.
func TestRetireObservationsIsBulkWrite(t *testing.T) {
	db := openTestDB(t)
	store := db.Observations()
	at := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	seeds := []retireSeed{{id: "a"}, {id: "b"}}
	seedRetireObservations(t, store, at, seeds)
	before := snapshotRetireObservations(t, store)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := retirementStore(t, db).RetireObservations(ctx, []string{"a", "b"}, at); err == nil {
		t.Fatal("a cancelled context must fail the retirement write")
	}
	after := snapshotRetireObservations(t, store)
	for id, was := range before {
		if !sameRetireInstant(was.validUntil, after[id].validUntil) {
			t.Errorf("%s: failed write left a partial retirement (%v)", id, after[id].validUntil)
		}
	}
}

// TestRetireObservationsLargeBatch retires more ids than one SQL statement can
// bind comfortably, so the batch is split into several statements inside one
// transaction. Every listed live row must still be retired exactly once and
// counted exactly once.
func TestRetireObservationsLargeBatch(t *testing.T) {
	db := openTestDB(t)
	at := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	// One row more than a single statement's worth even on the most
	// conservative host-parameter limit.
	const rows = 901

	// One raw statement seeds the fixture; the recursive generator keeps the
	// host-parameter count at two.
	if _, err := db.SQL().Exec(`
		INSERT INTO observations
		       (id, agent_id, project, title, content, obs_type, importance, created_at, valid_from)
		WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM seq WHERE n < ?)
		SELECT 'bulk-' || n, 'agentX', 'projX', 'bulk ' || n, 'body ' || n, 'pattern', 3, ?, ?
		  FROM seq`, rows, at, at); err != nil {
		t.Fatalf("seed bulk rows: %v", err)
	}
	ids := make([]string, 0, rows)
	for i := 1; i <= rows; i++ {
		ids = append(ids, "bulk-"+strconv.Itoa(i))
	}

	retired, err := retirementStore(t, db).RetireObservations(context.Background(), ids, at)
	if err != nil {
		t.Fatalf("RetireObservations: %v", err)
	}
	if retired != rows {
		t.Errorf("retired rows = %d, want %d", retired, rows)
	}
	var live int
	if err := db.SQL().QueryRow(
		`SELECT COUNT(*) FROM observations
		  WHERE id LIKE 'bulk-%' AND (valid_until IS NULL OR valid_until > ?)`, at,
	).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 0 {
		t.Errorf("%d bulk rows are still live after retirement", live)
	}
}

// TestRetireObservationsGuardedRechecksSelectors pins the stronger write the
// service prefers: every selector is re-applied inside the retirement UPDATE,
// and the returned ids are the rows the statement actually stamped. A row that
// stopped matching after the selection read is skipped instead of retired, and a
// skipped row never appears in the returned list.
func TestRetireObservationsGuardedRechecksSelectors(t *testing.T) {
	at := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		seeds []retireSeed
		in    memory.RetireInput
		ids   []string
		want  []string
	}{
		{
			name:  "matching rows are retired and returned",
			seeds: []retireSeed{{id: "a"}, {id: "b"}},
			in:    memory.RetireInput{Project: "projX", ImportanceMax: 3, NoAccess: true},
			ids:   []string{"a", "b"},
			want:  []string{"a", "b"},
		},
		{
			name:  "row above the importance threshold is skipped",
			seeds: []retireSeed{{id: "low", importance: 1}, {id: "high", importance: 4}},
			in:    memory.RetireInput{ImportanceMax: 2},
			ids:   []string{"low", "high"},
			want:  []string{"low"},
		},
		{
			name:  "accessed row is skipped by no-access",
			seeds: []retireSeed{{id: "fresh"}, {id: "touched", access: 1}},
			in:    memory.RetireInput{NoAccess: true},
			ids:   []string{"fresh", "touched"},
			want:  []string{"fresh"},
		},
		{
			name:  "row outside the project is skipped",
			seeds: []retireSeed{{id: "here"}, {id: "there", project: "other"}},
			in:    memory.RetireInput{Project: "projX"},
			ids:   []string{"here", "there"},
			want:  []string{"here"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			store := db.Observations()
			seedRetireObservations(t, store, at, tc.seeds)
			before := snapshotRetireObservations(t, store)

			guarded, ok := retirementStore(t, db).(memory.GuardedRetirementStore)
			if !ok {
				t.Fatal("the storage retirement adapter must offer the guarded write")
			}
			got, err := guarded.RetireObservationsGuarded(context.Background(), tc.ids, tc.in, at)
			if err != nil {
				t.Fatalf("RetireObservationsGuarded: %v", err)
			}
			want := append([]string(nil), tc.want...)
			sort.Strings(got)
			sort.Strings(want)
			if !sameStringList(got, want) {
				t.Fatalf("retired ids = %v, want %v", got, want)
			}

			after := snapshotRetireObservations(t, store)
			stamped := make(map[string]bool, len(got))
			for _, id := range got {
				stamped[id] = true
			}
			for id, was := range before {
				if stamped[id] {
					if !sameRetireInstant(&at, after[id].validUntil) {
						t.Errorf("%s: valid_until = %v, want the retirement instant %v", id, after[id].validUntil, at)
					}
					continue
				}
				if !sameRetireInstant(was.validUntil, after[id].validUntil) {
					t.Errorf("%s: a skipped row was stamped (%v)", id, after[id].validUntil)
				}
			}
		})
	}
}

func sameStringList(a, b []string) bool {
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

package storage_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func openEmbedDB(t *testing.T) (*storage.DB, *memory.Service) {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "embed.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, memory.NewService(memory.Config{Store: db.Observations()})
}

// seedEmbedded saves an observation and attaches a vector when vec is
// non-nil, returning the row ID.
func seedEmbedded(t *testing.T, svc *memory.Service, store memory.Store, in memory.SaveInput, vec []float32) string {
	t.Helper()
	ctx := context.Background()
	res, err := svc.Save(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if vec != nil {
		if err := store.UpdateEmbedding(ctx, res.Observation.ID, "test", vec); err != nil {
			t.Fatal(err)
		}
	}
	return res.Observation.ID
}

func TestListEmbeddingsScopeAndFilters(t *testing.T) {
	db, svc := openEmbedDB(t)
	store := db.Observations()
	ctx := context.Background()
	v := []float32{1, 0, 0, 0}

	idA := seedEmbedded(t, svc, store, memory.SaveInput{
		Title: "a", Content: "alpha", Type: memory.TypePattern, Project: "p1", AgentID: "alpha", Importance: 5}, v)
	idB := seedEmbedded(t, svc, store, memory.SaveInput{
		Title: "b", Content: "bravo", Type: memory.TypeCorrection, Project: "p1", AgentID: "alpha", Importance: 8}, v)
	idC := seedEmbedded(t, svc, store, memory.SaveInput{
		Title: "c", Content: "charlie", Type: memory.TypePattern, Project: "p2", AgentID: "alpha"}, v)
	seedEmbedded(t, svc, store, memory.SaveInput{
		Title: "d", Content: "delta no vector", Type: memory.TypePattern, Project: "p1", AgentID: "alpha"}, nil)
	idE := seedEmbedded(t, svc, store, memory.SaveInput{
		Title: "e", Content: "echo stale", Type: memory.TypePattern, Project: "p1", AgentID: "alpha"}, v)
	idF := seedEmbedded(t, svc, store, memory.SaveInput{
		Title: "f", Content: "foxtrot raw", Type: memory.TypePattern, Project: "p1", AgentID: "alpha",
		SourceKind: memory.SourceTool}, v)
	idG := seedEmbedded(t, svc, store, memory.SaveInput{
		Title: "g", Content: "golf tagged", Type: memory.TypePattern, Project: "p1", AgentID: "alpha",
		Importance: 3, Tags: []string{"db"}}, v)

	if err := svc.Invalidate(ctx, idE); err != nil {
		t.Fatal(err)
	}

	ids := func(t *testing.T, in memory.SearchInput) map[string]bool {
		t.Helper()
		rows, err := store.ListEmbeddings(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, r := range rows {
			out[r.ID] = true
		}
		return out
	}
	expect := func(t *testing.T, got map[string]bool, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("got %d rows %v, want %d rows %v", len(got), got, len(want), want)
		}
		for _, id := range want {
			if !got[id] {
				t.Errorf("missing row %s in %v", id, got)
			}
		}
	}

	cases := []struct {
		name string
		in   memory.SearchInput
		want []string
	}{
		{"live embedded only", memory.SearchInput{Project: "p1", AgentID: "alpha"}, []string{idA, idB, idG}},
		{"project filter", memory.SearchInput{Project: "p2"}, []string{idC}},
		{"agent filter misses", memory.SearchInput{Project: "p1", AgentID: "beta"}, nil},
		{"type filter", memory.SearchInput{Project: "p1", AgentID: "alpha", Type: memory.TypeCorrection}, []string{idB}},
		{"min importance", memory.SearchInput{Project: "p1", AgentID: "alpha", MinImportance: 5}, []string{idA, idB}},
		{"tag filter", memory.SearchInput{Project: "p1", AgentID: "alpha", Tags: []string{"db"}}, []string{idG}},
		{"include stale", memory.SearchInput{Project: "p1", AgentID: "alpha", IncludeStale: true}, []string{idA, idB, idE, idG}},
		{"include raw", memory.SearchInput{Project: "p1", AgentID: "alpha", IncludeRaw: true}, []string{idA, idB, idF, idG}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expect(t, ids(t, tc.in), tc.want...)
		})
	}

	t.Run("deterministic order", func(t *testing.T) {
		first, err := store.ListEmbeddings(ctx, memory.SearchInput{Project: "p1", AgentID: "alpha"})
		if err != nil {
			t.Fatal(err)
		}
		second, err := store.ListEmbeddings(ctx, memory.SearchInput{Project: "p1", AgentID: "alpha"})
		if err != nil {
			t.Fatal(err)
		}
		if len(first) != len(second) {
			t.Fatalf("length drift between calls: %d vs %d", len(first), len(second))
		}
		for i := range first {
			if first[i].ID != second[i].ID {
				t.Fatalf("order drift at %d: %s vs %s", i, first[i].ID, second[i].ID)
			}
		}
	})
}

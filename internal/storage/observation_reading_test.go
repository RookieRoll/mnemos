package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

func TestBulkDetailAccessIsAtomic(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"a", "b"} {
		if err := db.Observations().Insert(ctx, &memory.Observation{ID: id, AgentID: "default", Project: "project", Title: id, Content: "detail", Type: memory.TypePattern, Importance: 5, CreatedAt: now, ValidFrom: now}); err != nil {
			t.Fatal(err)
		}
	}
	store := db.Observations().(memory.ScopedReadStore)
	count := func(id string) int {
		t.Helper()
		var n int
		if err := db.SQL().QueryRow("SELECT access_count FROM observations WHERE id=?", id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if _, err := db.SQL().Exec(`CREATE TRIGGER abort_detail_access BEFORE UPDATE OF access_count ON observations WHEN NEW.id='b' BEGIN SELECT RAISE(ABORT, 'test access failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.IncrementAccessMany(ctx, []string{"a", "b"}, now); err == nil {
		t.Fatal("expected atomic update failure")
	}
	if count("a") != 0 || count("b") != 0 {
		t.Fatal("partial access changes survived SQL failure")
	}
	if _, err := db.SQL().Exec("DROP TRIGGER abort_detail_access"); err != nil {
		t.Fatal(err)
	}
	if err := store.IncrementAccessMany(ctx, []string{"a", "missing"}, now); err == nil {
		t.Fatal("missing detail did not abort aggregate update")
	}
	if count("a") != 0 {
		t.Fatal("missing row left partial increments")
	}
	if err := store.IncrementAccessMany(ctx, []string{"a", "a", "b"}, now); err != nil {
		t.Fatal(err)
	}
	if count("a") != 1 || count("b") != 1 {
		t.Fatal("unique returned details not counted once")
	}
	rows, err := store.ReadScoped(ctx, memory.ScopedReadInput{IDs: []string{"a", "b"}, Project: "project", AgentID: "default", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || count("a") != 1 || count("b") != 1 {
		t.Fatal("scoped read mutated counters")
	}
}

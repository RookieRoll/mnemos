package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

func TestEmbeddingBackfillKeysetHighWaterAndEmptyVectors(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "backfill.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := db.Observations().(*obsStore)
	now := time.Now().UTC()
	for _, id := range []string{"a", "b", "c"} {
		o := memory.Observation{ID: id, AgentID: "default", Title: id, Content: id, Type: memory.TypeSemantic, Importance: 5, CreatedAt: now, ValidFrom: now}
		if err := store.Insert(ctx, &o); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.sql.ExecContext(ctx, `UPDATE observations SET embedding=x'' WHERE id='b'`); err != nil {
		t.Fatal(err)
	}
	upper, err := store.EmbeddingBackfillUpperBound(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if upper != "c" {
		t.Fatalf("upper=%q", upper)
	}
	page, err := store.ListEmbeddingBackfill(ctx, memory.EmbeddingBackfillInput{ThroughID: upper, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].ID != "a" {
		t.Fatalf("first page=%v", page)
	}
	if err := store.Insert(ctx, &memory.Observation{ID: "z", AgentID: "default", Title: "late", Content: "late", Type: memory.TypeSemantic, Importance: 5, CreatedAt: now, ValidFrom: now}); err != nil {
		t.Fatal(err)
	}
	// Neither a failed a nor a concurrent higher-ID insertion changes the
	// remaining page. Empty blobs are missing and therefore eligible.
	page, err = store.ListEmbeddingBackfill(ctx, memory.EmbeddingBackfillInput{AfterID: "a", ThroughID: upper, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].ID != "b" || page[1].ID != "c" {
		t.Fatalf("cursor/horizon leaked or repeated rows: %v", page)
	}
	for _, input := range []memory.EmbeddingBackfillInput{{ThroughID: upper, Limit: 1001}, {ThroughID: upper, Limit: 1, RebuildIncompatible: true}} {
		if _, err := store.ListEmbeddingBackfill(ctx, input); err == nil {
			t.Fatalf("invalid input accepted: %+v", input)
		}
	}
}

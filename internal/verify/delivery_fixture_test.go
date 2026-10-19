package verify_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
	"github.com/polyxmedia/mnemos/internal/verify"
)

func TestVersionedLabelledFixtureOnIsolatedStore(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "labelled.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := db.Observations()
	svc := memory.NewService(memory.Config{Store: store, Clock: func() time.Time { return now }})
	stale := now.Add(-time.Hour)
	for _, o := range []memory.Observation{
		{ID: "rule", Title: "transaction rule", Content: "Every multiple write operation needs a transaction."},
		{ID: "exception", Title: "transaction exception", Content: "Read-only operations require no transaction."},
		{ID: "raw", Title: "transaction raw note", Content: "Unreviewed transaction advice", TrustTier: memory.TrustRaw},
		{ID: "stale", Title: "transaction stale advice", Content: "Old transaction advice", ValidUntil: &stale},
		{ID: "chinese", Title: "Unicode 中文约定", Content: "Unicode 文本必须完整保留，不能按字节截断。"},
	} {
		o.Project = "fixture"
		o.Type = memory.TypeConvention
		o.Importance = 8
		o.CreatedAt = now.Add(-24 * time.Hour)
		o.ValidFrom = o.CreatedAt
		if o.TrustTier == "" {
			o.TrustTier = memory.TrustCurated
		}
		if err := store.Insert(ctx, &o); err != nil {
			t.Fatal(err)
		}
	}
	fix, err := verify.LoadRetrievalFixture("../../verify/context-retrieval.yaml")
	if err != nil {
		t.Fatal(err)
	}
	first, err := verify.RunRetrieval(ctx, svc, fix)
	if err != nil {
		t.Fatal(err)
	}
	second, err := verify.RunRetrieval(ctx, svc, fix)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("fixed-clock reports differ")
	}
	if first.Passed != first.Total {
		t.Fatalf("fixture failed: %+v", first.Cases)
	}
	rows, err := store.ListByProject(ctx, "", "fixture", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("live corpus changed: %d", len(rows))
	}
	for _, o := range rows {
		if o.AccessCount != 0 {
			t.Fatal("evaluation changed access counters")
		}
	}
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

type backfillTestEmbedder struct {
	calls  map[string]int
	fail   func(string) bool
	onCall func()
}

type backfillFixtureStore interface {
	memory.Store
	embeddingBackfillStore
}

func (e *backfillTestEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	id := strings.SplitN(text, "\n", 2)[0]
	e.calls[id]++
	if e.onCall != nil {
		e.onCall()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.fail != nil && e.fail(id) {
		return nil, errors.New("synthetic provider failure")
	}
	return []float32{1, 0}, nil
}
func (e *backfillTestEmbedder) Dimension() int { return 2 }
func (e *backfillTestEmbedder) Model() string  { return "fixture/current" }

func TestEmbeddingBackfillVisitsEveryIDOnce(t *testing.T) {
	for _, tc := range []struct {
		name       string
		count      int
		fail       func(string) bool
		wantFailed int
	}{
		{"three pages", 205, nil, 0},
		{"permanent full batch", 100, func(string) bool { return true }, 100},
		{"partial failures cross pages", 205, func(id string) bool { return strings.HasSuffix(id, "0") }, 21},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "backfill.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			store := db.Observations().(backfillFixtureStore)
			now := time.Now().UTC()
			for i := 0; i < tc.count; i++ {
				id := fmt.Sprintf("item-%03d", i)
				if err := store.Insert(context.Background(), &memory.Observation{ID: id, AgentID: "default", Title: id, Content: "synthetic", Type: memory.TypeSemantic, Importance: 5, CreatedAt: now, ValidFrom: now}); err != nil {
					t.Fatal(err)
				}
			}
			e := &backfillTestEmbedder{calls: map[string]int{}, fail: tc.fail}
			report, err := runEmbeddingBackfill(context.Background(), store, e, embeddingBackfillOptions{})
			if (err != nil) != (tc.wantFailed > 0) {
				t.Fatalf("report=%+v err=%v", report, err)
			}
			if report.Attempted != tc.count || report.Failed != tc.wantFailed || report.Succeeded != tc.count-tc.wantFailed {
				t.Fatalf("unexpected counts: %+v", report)
			}
			if len(e.calls) != tc.count {
				t.Fatalf("only visited %d/%d", len(e.calls), tc.count)
			}
			for id, n := range e.calls {
				if n != 1 {
					t.Fatalf("%s attempted %d times", id, n)
				}
			}
		})
	}
}

func TestEmbeddingBackfillRebuildIsExplicitAndPreservesMetadata(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "backfill.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := db.Observations().(backfillFixtureStore)
	now := time.Now().UTC()
	for _, o := range []memory.Observation{
		{ID: "a", Title: "missing"},
		{ID: "b", Title: "old model", EmbeddingModel: "fixture/old", Embedding: []float32{1, 0}},
		{ID: "c", Title: "wrong dimension", EmbeddingModel: "fixture/current", Embedding: []float32{1, 0, 0}},
		{ID: "d", Title: "compatible", EmbeddingModel: "fixture/current", Embedding: []float32{0, 1}},
	} {
		o.AgentID = "default"
		o.Content = "synthetic"
		o.Type = memory.TypeSemantic
		o.Importance = 7
		o.CreatedAt = now
		o.ValidFrom = now
		o.Pinned = true
		o.TopicKey = "topic"
		o.Tags = []string{"retained"}
		if err := store.Insert(context.Background(), &o); err != nil {
			t.Fatal(err)
		}
	}
	e := &backfillTestEmbedder{calls: map[string]int{}}
	report, err := runEmbeddingBackfill(context.Background(), store, e, embeddingBackfillOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Attempted != 1 || e.calls["missing"] != 1 {
		t.Fatalf("default changed existing vectors: %+v %v", report, e.calls)
	}
	before, err := store.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e.calls = map[string]int{}
	report, err = runEmbeddingBackfill(context.Background(), store, e, embeddingBackfillOptions{RebuildIncompatible: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Attempted != 2 || e.calls["old model"] != 1 || e.calls["wrong dimension"] != 1 {
		t.Fatalf("rebuild eligibility wrong: %+v %v", report, e.calls)
	}
	after, err := store.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := range before {
		before[i].Embedding = nil
		before[i].EmbeddingModel = ""
		after[i].Embedding = nil
		after[i].EmbeddingModel = ""
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("maintenance changed observation metadata")
	}
}

func TestEmbeddingBackfillCancellationStopsImmediately(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "backfill.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := db.Observations().(backfillFixtureStore)
	now := time.Now().UTC()
	for i := 0; i < 105; i++ {
		id := fmt.Sprintf("item-%03d", i)
		if err := store.Insert(context.Background(), &memory.Observation{ID: id, AgentID: "default", Title: id, Content: "synthetic", Type: memory.TypeSemantic, Importance: 5, CreatedAt: now, ValidFrom: now}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &backfillTestEmbedder{calls: map[string]int{}}
	e.onCall = func() {
		if len(e.calls) == 2 {
			cancel()
		}
	}
	report, err := runEmbeddingBackfill(ctx, store, e, embeddingBackfillOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want cancellation, got %v", err)
	}
	if len(e.calls) != 2 {
		t.Fatalf("continued after cancellation: %d calls", len(e.calls))
	}
	if report.Attempted != report.Succeeded+report.Failed || report.Failed != 1 {
		t.Fatalf("in-flight cancelled attempt not counted: %+v", report)
	}
}

func TestRunEmbedBackfillRebuildOptionWithFakeProvider(t *testing.T) {
	requests := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{1, 0}}})
	}))
	defer provider.Close()
	homeWithConfig(t, fmt.Sprintf("[embedding]\nprovider=\"ollama\"\nmodel=\"fixture\"\ndimension=2\nbase_url=%q\n", provider.URL))
	d, err := loadDeps(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	o := memory.Observation{ID: "old", AgentID: "default", Title: "synthetic", Content: "synthetic", Type: memory.TypeSemantic, Importance: 5, CreatedAt: now, ValidFrom: now, EmbeddingModel: "old", Embedding: []float32{0, 1}}
	if err := d.db.Observations().Insert(context.Background(), &o); err != nil {
		t.Fatal(err)
	}
	d.close()
	var commandErr error
	out := captureStdout(t, func() { commandErr = runEmbed(context.Background(), []string{"backfill", "--rebuild-incompatible"}) })
	if commandErr != nil {
		t.Fatal(commandErr)
	}
	if requests != 1 || !strings.Contains(out, "attempted=1 succeeded=1 failed=0") {
		t.Fatalf("real CLI option did not execute provider: requests=%d output=%q", requests, out)
	}
	d, err = loadDeps(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	got, err := d.db.Observations().Get(context.Background(), o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.EmbeddingModel != "ollama/fixture" || !reflect.DeepEqual(got.Embedding, []float32{1, 0}) || got.Content != o.Content || got.ID != o.ID {
		t.Fatalf("unexpected rebuild result: %+v", got)
	}
}

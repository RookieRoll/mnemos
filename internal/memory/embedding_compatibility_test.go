package memory_test

import (
	"context"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

func TestIncompatibleEmbeddingPreservesFTSScoreAndMode(t *testing.T) {
	for _, tc := range []struct {
		name, model   string
		stored, query []float32
	}{
		{"other model", "old", []float32{1, 0}, []float32{1, 0}},
		{"missing model", "", []float32{1, 0}, []float32{1, 0}},
		{"stored dimension", "fixed", []float32{1, 0, 0}, []float32{1, 0}},
		{"query dimension", "fixed", []float32{1, 0}, []float32{1, 0, 0}},
		{"zero query", "fixed", []float32{1, 0}, []float32{0, 0}},
		{"invalid stored signal", "fixed", []float32{float32(math.NaN()), 0}, []float32{1, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, store := newContractStore(t)
			id := insertObs(t, store, &memory.Observation{Title: "sqlite tuning", Content: "sqlite tuning", EmbeddingModel: tc.model, Embedding: tc.stored})
			insertObs(t, store, &memory.Observation{Title: "unrelated", Content: "unrelated", EmbeddingModel: tc.model, Embedding: tc.stored})
			seedContrast(t, store, 10)
			now := time.Now().UTC()
			clock := func() time.Time { return now }
			base := memory.NewService(memory.Config{Store: store, Clock: clock})
			cfg := memory.Config{Store: store, Embedder: fixedEmbedder{dim: 2, vec: tc.query}, Clock: clock}
			svc := memory.NewService(cfg)
			ctx := context.Background()
			before, err := store.Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			want, err := base.Search(ctx, memory.SearchInput{Query: "sqlite"})
			if err != nil {
				t.Fatal(err)
			}
			got, mode, err := svc.SearchWithMode(ctx, memory.SearchInput{Query: "sqlite"})
			if err != nil {
				t.Fatal(err)
			}
			if mode != memory.RetrievalFTS {
				t.Errorf("mode=%s, want fts", mode)
			}
			if len(got) != len(want) {
				t.Fatalf("incompatible semantic recall added hits: got %d want %d", len(got), len(want))
			}
			for i := range got {
				if got[i].Observation.ID != want[i].Observation.ID || math.Abs(got[i].Score-want[i].Score) > 1e-9 {
					t.Errorf("incompatible signal changed keyword score: got=%+v want=%+v", got[i], want[i])
				}
			}
			after, err := store.Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			// Compare encoded float bits too: NaN is intentionally not equal to itself.
			if before.EmbeddingModel != after.EmbeddingModel || len(before.Embedding) != len(after.Embedding) {
				t.Fatal("retrieval changed stored embedding metadata")
			}
			for i := range before.Embedding {
				if math.Float32bits(before.Embedding[i]) != math.Float32bits(after.Embedding[i]) {
					t.Fatal("retrieval changed stored vector")
				}
			}
		})
	}
}

func TestCompatibleSemanticRecallRequiresModelMetadata(t *testing.T) {
	_, store := newContractStore(t)
	id := insertObs(t, store, &memory.Observation{Title: "automobile", Content: "automobile", EmbeddingModel: "fixed", Embedding: []float32{1, 0}})
	svc := memory.NewService(memory.Config{Store: store, Embedder: fixedEmbedder{dim: 2, vec: []float32{1, 0}}})
	hits, mode, err := svc.SearchWithMode(context.Background(), memory.SearchInput{Query: "vehicle"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, hit := range hits {
		ids = append(ids, hit.Observation.ID)
	}
	if mode != memory.RetrievalHybrid || !reflect.DeepEqual(ids, []string{id}) {
		t.Fatalf("compatible semantic recall lost: ids=%v mode=%v", ids, mode)
	}
}

func TestEmbeddingEndpointWeightsAndFallback(t *testing.T) {
	for _, tc := range []struct {
		name     string
		alpha    float64
		embedder memory.Embedder
		want     memory.RetrievalMode
	}{
		{"zero no provider", 0, nil, memory.RetrievalFTS},
		{"zero provider error", 0, fixedEmbedder{dim: 2, err: errFake}, memory.RetrievalFTS},
		{"zero vector and error", 0, partialErrorEmbedder{}, memory.RetrievalFTS},
		{"zero invalid dimension", 0, fixedEmbedder{dim: 2, vec: []float32{1, 0, 0}}, memory.RetrievalFTS},
		{"zero zero signal", 0, fixedEmbedder{dim: 2, vec: []float32{0, 0}}, memory.RetrievalFTS},
		{"zero valid vector", 0, fixedEmbedder{dim: 2, vec: []float32{1, 0}}, memory.RetrievalHybrid},
		{"one valid vector", 1, fixedEmbedder{dim: 2, vec: []float32{1, 0}}, memory.RetrievalFTS},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, store := newContractStore(t)
			insertObs(t, store, &memory.Observation{Title: "sqlite", Content: "sqlite", EmbeddingModel: "fixed", Embedding: []float32{1, 0}})
			seedContrast(t, store, 10)
			h := memory.DefaultHybridParams()
			h.Alpha = tc.alpha
			now := time.Now().UTC()
			clock := func() time.Time { return now }
			svc := memory.NewService(memory.Config{Store: store, Embedder: tc.embedder, Hybrid: h, Clock: clock})
			hits, mode, err := svc.SearchWithMode(context.Background(), memory.SearchInput{Query: "sqlite"})
			if err != nil {
				t.Fatal(err)
			}
			if mode != tc.want || len(hits) != 1 {
				t.Fatalf("mode=%s hits=%d, want %s with one hit", mode, len(hits), tc.want)
			}
			if tc.want == memory.RetrievalFTS {
				base := memory.NewService(memory.Config{Store: store, Clock: clock})
				want, err := base.Search(context.Background(), memory.SearchInput{Query: "sqlite"})
				if err != nil {
					t.Fatal(err)
				}
				if hits[0].Score != want[0].Score {
					t.Fatalf("endpoint fallback score=%v, want %v", hits[0].Score, want[0].Score)
				}
			}
		})
	}
}

type partialErrorEmbedder struct{}

func (partialErrorEmbedder) Embed(context.Context, string) ([]float32, error) {
	return []float32{1, 0}, errFake
}
func (partialErrorEmbedder) Dimension() int { return 2 }
func (partialErrorEmbedder) Model() string  { return "fixed" }

func TestPureVectorValidQueryExcludesIncompatibleKeywordCandidates(t *testing.T) {
	for _, tc := range []struct {
		name, model string
		vec         []float32
	}{
		{"other model", "other", []float32{1, 0}},
		{"missing model", "", []float32{1, 0}},
		{"missing vector", "fixed", nil},
		{"zero signal", "fixed", []float32{0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, store := newContractStore(t)
			insertObs(t, store, &memory.Observation{Title: "sqlite", Content: "sqlite", EmbeddingModel: tc.model, Embedding: tc.vec})
			seedContrast(t, store, 10)
			h := memory.DefaultHybridParams()
			h.Alpha = 0
			svc := memory.NewService(memory.Config{Store: store, Hybrid: h, Embedder: fixedEmbedder{dim: 2, vec: []float32{1, 0}}})
			hits, mode, err := svc.SearchWithMode(context.Background(), memory.SearchInput{Query: "sqlite"})
			if err != nil {
				t.Fatal(err)
			}
			if len(hits) != 0 || mode != memory.RetrievalFTS {
				t.Fatalf("pure-vector endpoint silently enabled keyword weighting: hits=%v mode=%v", hits, mode)
			}
		})
	}
}

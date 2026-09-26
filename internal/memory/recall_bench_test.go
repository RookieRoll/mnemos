package memory_test

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// BenchmarkVectorRecallScan measures the brute-force vector recall path:
// the scoped ListEmbeddings read plus cosine over every returned row.
// 10k rows at dim 64 stands in for a long-lived local store; the numbers
// are the ceiling the design note quotes for "no ANN, just scan".
func BenchmarkVectorRecallScan(b *testing.B) {
	const rows = 10000
	const dim = 64

	db, err := storage.Open(context.Background(), filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	store := db.Observations()
	svc := memory.NewService(memory.Config{
		Store:    store,
		Embedder: fixedEmbedder{dim: dim, vec: make([]float32, dim)},
	})
	ctx := context.Background()

	rng := rand.New(rand.NewSource(42))
	now := time.Now().UTC()
	for i := 0; i < rows; i++ {
		vec := make([]float32, dim)
		for j := range vec {
			vec[j] = rng.Float32()
		}
		o := &memory.Observation{
			ID:        ulid.Make().String(),
			AgentID:   "bench",
			Project:   "bench",
			Title:     fmt.Sprintf("row %d", i),
			Content:   fmt.Sprintf("benchmarked content %d", i),
			Type:      memory.TypeEpisodic,
			Importance: 5,
			CreatedAt: now,
			ValidFrom: now,
			Embedding: vec,
		}
		if err := store.Insert(ctx, o); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := svc.SearchWithMode(ctx, memory.SearchInput{
			Query:   "no-such-token-zzz",
			Project: "bench",
			Limit:   10,
		}); err != nil {
			b.Fatal(err)
		}
	}
}

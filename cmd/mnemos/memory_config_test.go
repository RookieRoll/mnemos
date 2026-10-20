package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/config"
	"github.com/polyxmedia/mnemos/internal/memory"
)

func TestSharedMemoryServiceConfigKeepsExplicitWeights(t *testing.T) {
	for _, alpha := range []float64{0, .2, 1} {
		t.Run(fmt.Sprint(alpha), func(t *testing.T) {
			search := config.Default().Search
			search.DecayRate = .25
			search.HybridAlpha = alpha
			got := memoryServiceConfig(search, nil, nil, nil)
			if got.Hybrid.Alpha != alpha || got.Hybrid.BM25K != 8 || got.Hybrid.CosHigh != .8 || got.RankParams.DecayRate != .25 || got.RankParams.ImportanceWeight != .5 || got.RankParams.AccessBoost != .1 {
				t.Fatalf("lost resolved configuration: %+v", got)
			}
		})
	}
}

func TestLoadDepsUsesConfiguredRanking(t *testing.T) {
	for _, alpha := range []float64{0, .2, 1} {
		t.Run(fmt.Sprint(alpha), func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{1, 0}}})
			}))
			defer provider.Close()
			homeWithConfig(t, fmt.Sprintf("[search]\ndecay_rate=0.25\nhybrid_alpha=%v\n[embedding]\nprovider=\"ollama\"\nmodel=\"fixture\"\ndimension=2\nbase_url=%q\n", alpha, provider.URL))
			d, err := loadDeps(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer d.close()
			now := time.Now().UTC()
			old := now.Add(-365 * 24 * time.Hour)
			o := memory.Observation{ID: "target", AgentID: "default", Title: "sqlite", Content: "sqlite", Type: memory.TypeSemantic, Importance: 5, CreatedAt: old, ValidFrom: old, EmbeddingModel: "ollama/fixture", Embedding: []float32{1, 0}}
			if err := d.db.Observations().Insert(context.Background(), &o); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 10; i++ {
				noise := o
				noise.ID = fmt.Sprint("noise-", i)
				noise.Title = "unrelated"
				noise.Content = "weather zebra"
				noise.Embedding = nil
				noise.EmbeddingModel = ""
				if err := d.db.Observations().Insert(context.Background(), &noise); err != nil {
					t.Fatal(err)
				}
			}
			hits, mode, err := d.mem.SearchWithMode(context.Background(), memory.SearchInput{Query: "sqlite"})
			if err != nil {
				t.Fatal(err)
			}
			if len(hits) != 1 {
				t.Fatalf("got %d hits", len(hits))
			}
			wantRel := alpha*(hits[0].BM25/(hits[0].BM25+8)) + (1 - alpha)
			if math.Abs(hits[0].Relevance-wantRel) > 1e-9 {
				t.Fatalf("configured alpha lost: relevance=%v want=%v", hits[0].Relevance, wantRel)
			}
			rank := memory.DefaultRankParams()
			rank.DecayRate = .25
			wantPolicy := memory.NewRanker(rank).PolicyFactor(o, now)
			if math.Abs(hits[0].PolicyFactor-wantPolicy) > 1e-6 {
				t.Fatalf("configured decay lost: policy=%v want=%v", hits[0].PolicyFactor, wantPolicy)
			}
			if alpha == 1 && mode != memory.RetrievalFTS {
				t.Fatalf("zero weight vector reported as %s", mode)
			}
		})
	}
}

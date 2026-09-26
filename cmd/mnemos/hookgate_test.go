package main

import (
	"context"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/memory"
)

// gateEmbedder gives the query and matching docs one vector and filler docs
// an orthogonal one, so cosine carries a real signal in the hook path.
type gateEmbedder struct {
	qvec, other []float32
}

func (g gateEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if strings.Contains(text, "frobnicate") {
		return g.qvec, nil
	}
	return g.other, nil
}
func (g gateEmbedder) Dimension() int { return 4 }
func (g gateEmbedder) Model() string  { return "gate-test" }

// TestHybridModePromptGateKeepsOnTopicMemory is the regression for the
// score-contract bug: with embeddings active, the prompt hook's gate used
// to read a BM25-calibrated floor (1.5) against RRF-scale scores (<= 0.016),
// so hybrid mode silently dropped EVERY memory. On the contract scale the
// same floor keeps working in both modes.
func TestHybridModePromptGateKeepsOnTopicMemory(t *testing.T) {
	withHome(t)
	ctx := context.Background()

	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	defer d.close()

	// Re-wire the memory service with an active embedder: this is the
	// hybrid-capable configuration whose scores used to sink below the gate.
	emb := gateEmbedder{qvec: []float32{1, 0, 0, 0}, other: []float32{0, 1, 0, 0}}
	d.embedder = emb
	d.mem = memory.NewService(memory.Config{
		Store:      d.db.Observations(),
		Embedder:   emb,
		Injections: injection.NewLogger(d.db.Injections(), nil),
	})

	// Corpus contrast so BM25 magnitudes are meaningful (same idiom as the
	// other hook tests).
	for i, title := range []string{"react hooks", "docker compose", "css grid", "git rebase", "tls certs"} {
		if _, err := d.mem.Save(ctx, memory.SaveInput{
			Title: title, Content: "unrelated filler content " + strings.Repeat("noise ", i+1),
			Type: memory.TypeContext, Project: "p",
		}); err != nil {
			t.Fatalf("save filler: %v", err)
		}
	}
	target, err := d.mem.Save(ctx, memory.SaveInput{
		Title: "frobnicate the widget safely", Content: "always frobnicate the widget before shipping",
		Type: memory.TypeConvention, Project: "p",
	})
	if err != nil {
		t.Fatalf("save target: %v", err)
	}

	pm := collectPromptMemory(ctx, d, "how do I frobnicate the widget safely", "agent", "p", "sess-1")
	if len(pm.Hits) == 0 {
		t.Fatal("hybrid-active on-topic memory must clear the gate (pre-change this returned nothing)")
	}
	if pm.Hits[0].Observation.ID != target.Observation.ID {
		t.Errorf("want target %s first, got %s", target.Observation.ID, pm.Hits[0].Observation.ID)
	}
	if pm.Hits[0].Relevance <= 0 || pm.Hits[0].PolicyFactor <= 0 {
		t.Errorf("breakdown must ride along on surfaced hits: %+v", pm.Hits[0])
	}

	// Weak batch: the only candidates are cross-project, so the affinity
	// penalty must keep them under the floor — nothing injected.
	if _, err := d.mem.Save(ctx, memory.SaveInput{
		Title: "frobnicate the widget elsewhere", Content: "always frobnicate the widget before shipping",
		Type: memory.TypeConvention, Project: "other",
	}); err != nil {
		t.Fatalf("save cross-project: %v", err)
	}
	// Second project, so the first project's suppression window cannot
	// mask a gate failure.
	weak := collectPromptMemory(ctx, d, "how do I frobnicate the widget safely", "agent", "p2", "sess-2")
	if len(weak.Hits) != 0 {
		t.Errorf("cross-project weak batch must be gated, got %d hits: %+v", len(weak.Hits), weak.Hits)
	}
}

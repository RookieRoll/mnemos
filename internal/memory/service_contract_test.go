package memory_test

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// mapEmbedder returns a fixed vector per text, zero vector for anything
// unseen (zero vector = no signal, same as a missing embedding).
type mapEmbedder struct {
	dim  int
	vecs map[string][]float32
}

func (m mapEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if v, ok := m.vecs[text]; ok {
		return v, nil
	}
	return make([]float32, m.dim), nil
}
func (m mapEmbedder) Dimension() int { return m.dim }
func (m mapEmbedder) Model() string  { return "map" }

func newContractStore(t *testing.T) (*storage.DB, memory.Store) {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, db.Observations()
}

func insertObs(t *testing.T, store memory.Store, o *memory.Observation) string {
	t.Helper()
	if o.ID == "" {
		o.ID = ulid.Make().String()
	}
	if o.AgentID == "" {
		o.AgentID = "default"
	}
	if o.Type == "" {
		o.Type = memory.TypeSemantic
	}
	if o.Importance == 0 {
		o.Importance = 5
	}
	now := time.Now().UTC()
	if o.CreatedAt.IsZero() {
		o.CreatedAt = now
	}
	if o.ValidFrom.IsZero() {
		o.ValidFrom = now
	}
	if err := store.Insert(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	return o.ID
}

// seedContrast adds unrelated rows so BM25's IDF is meaningful: with a
// corpus of only matching docs every term has near-zero IDF and the BM25
// magnitude collapses — the same degeneracy the hook tests dodge with
// their "corpus contrast" fixtures.
func seedContrast(t *testing.T, store memory.Store, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		insertObs(t, store, &memory.Observation{
			Title: fmt.Sprintf("noise %d", i), Content: fmt.Sprintf("lorem ipsum zebra %d weather report", i),
		})
	}
}

// Spec scenario "Paraphrase-only memory is retrieved": the target shares no
// keyword with the query and only the vector source can find it.
func TestVectorRecallSurfacesParaphraseOnlyMemory(t *testing.T) {
	q := "how do I frobnicate the widget"
	_, store := newContractStore(t)
	svc := memory.NewService(memory.Config{Store: store, Embedder: mapEmbedder{
		dim: 4, vecs: map[string][]float32{q: {1, 0, 0, 0}},
	}})
	id := insertObs(t, store, &memory.Observation{
		Title: "t", Content: "zanzibar quux wibble", Embedding: []float32{1, 0, 0, 0},
	})
	seedContrast(t, store, 6)

	res, mode, err := svc.SearchWithMode(context.Background(), memory.SearchInput{Query: q})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range res {
		if r.Observation.ID == id {
			found = true
			if r.Score <= 0 {
				t.Errorf("recalled hit must carry score > 0, got %v", r.Score)
			}
		}
	}
	if !found {
		t.Errorf("paraphrase-only memory must be retrievable via vector recall; got %d results", len(res))
	}
	if mode != memory.RetrievalHybrid {
		t.Errorf("vector recall contributed, mode must be %q, got %q", memory.RetrievalHybrid, mode)
	}
}

// Spec scenario "Missing embedding is not penalized": identical keyword
// relevance, one row with no embedding (and one with a dead zero vector —
// the compatibility guard), one with a useless orthogonal vector.
func TestMissingEmbeddingIsNotPenalized(t *testing.T) {
	q := "sqlite tuning"
	_, store := newContractStore(t)
	svc := memory.NewService(memory.Config{Store: store, Embedder: mapEmbedder{
		dim: 4, vecs: map[string][]float32{q: {1, 0, 0, 0}},
	}})
	noVec := insertObs(t, store, &memory.Observation{Title: "same", Content: "sqlite tuning notes"})
	zeroVec := insertObs(t, store, &memory.Observation{
		Title: "same", Content: "sqlite tuning notes", Embedding: []float32{0, 0, 0, 0},
	})
	orthVec := insertObs(t, store, &memory.Observation{
		Title: "same", Content: "sqlite tuning notes", Embedding: []float32{0, 1, 0, 0},
	})
	seedContrast(t, store, 6)

	res, _, err := svc.SearchWithMode(context.Background(), memory.SearchInput{Query: q})
	if err != nil {
		t.Fatal(err)
	}
	score := map[string]float64{}
	for _, r := range res {
		score[r.Observation.ID] = r.Score
	}
	if score[noVec] == 0 {
		t.Fatal("keyword hit without embedding must score")
	}
	if math.Abs(score[noVec]-score[zeroVec]) > 1e-9 {
		t.Errorf("zero-vector row must score identically to a missing embedding: %v vs %v",
			score[zeroVec], score[noVec])
	}
	if score[orthVec] >= score[noVec] {
		t.Errorf("a useless vector signal must not outrank keyword-only: %v vs %v",
			score[orthVec], score[noVec])
	}
}

// Spec scenario "Irrelevant but popular memory stays out": policy factors
// alone must not lift a zero-relevance memory into results.
func TestIrrelevantButPopularStaysOut(t *testing.T) {
	q := "database migration plan"
	_, store := newContractStore(t)
	svc := memory.NewService(memory.Config{Store: store, Embedder: mapEmbedder{
		dim: 4, vecs: map[string][]float32{q: {1, 0, 0, 0}},
	}})
	insertObs(t, store, &memory.Observation{
		Title: "hot noise", Content: "lorem ipsum dolor", Importance: 10,
		Embedding: []float32{0, 1, 0, 0},
	})
	hot := insertObs(t, store, &memory.Observation{
		Title: "hot relevant", Content: "database migration plan steps", Importance: 10,
		Embedding: []float32{1, 0, 0, 0},
	})
	seedContrast(t, store, 6)

	res, _, err := svc.SearchWithMode(context.Background(), memory.SearchInput{Query: q})
	if err != nil {
		t.Fatal(err)
	}
	sawHot := false
	for _, r := range res {
		if r.Score <= 0 || r.Score > memory.ScoreCeiling {
			t.Errorf("score %v violates the contract bound (0, %v]", r.Score, memory.ScoreCeiling)
		}
		if r.Observation.Title == "hot noise" {
			t.Error("irrelevant memory must not be lifted into results by policy factors")
		}
		if r.Observation.ID == hot {
			sawHot = true
		}
	}
	if !sawHot {
		t.Error("relevant memory must be returned")
	}
}

// The ceiling is a real clamp, not a hope: with pure semantic relevance 1
// and a policy factor past the ceiling, the score lands exactly on it.
func TestScoreCeilingClamps(t *testing.T) {
	q := "database migration plan"
	_, store := newContractStore(t)
	svc := memory.NewService(memory.Config{
		Store:    store,
		Embedder: mapEmbedder{dim: 4, vecs: map[string][]float32{q: {1, 0, 0, 0}}},
		// Pure semantic: relevance = cosine term. BM25K must be spelled out
		// because an all-zero HybridParams means "unset" and gets the defaults.
		Hybrid: memory.HybridParams{Alpha: 0, BM25K: 8},
	})
	hot := insertObs(t, store, &memory.Observation{
		Title: "hot", Content: "database migration plan steps", Importance: 10,
		Embedding: []float32{1, 0, 0, 0},
	})
	seedContrast(t, store, 6)
	// access_factor = 1 + 0.1*ln(1+n) > 1.2 once n > 2.
	for i := 0; i < 50; i++ {
		if err := store.BumpAccess(context.Background(), hot); err != nil {
			t.Fatal(err)
		}
	}

	res, _, err := svc.SearchWithMode(context.Background(), memory.SearchInput{Query: q})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Observation.ID == hot && r.Score != memory.ScoreCeiling {
			t.Errorf("policy past the ceiling must clamp the score to %v, got %v", memory.ScoreCeiling, r.Score)
		}
	}
}

// Spec scenario "Scores are comparable across modes": one gate floor works
// for keyword-mode and hybrid-mode results alike.
func TestScoresComparableAcrossModes(t *testing.T) {
	q := "database migration plan"
	_, store := newContractStore(t)
	insertObs(t, store, &memory.Observation{
		Title: "migration", Content: "database migration plan steps",
	})
	seedContrast(t, store, 6)

	const gate = 0.10 // the calibrated prompt-hook floor
	withVec := memory.NewService(memory.Config{Store: store, Embedder: mapEmbedder{
		dim: 4, vecs: map[string][]float32{q: {1, 0, 0, 0}},
	}})
	keywordOnly := memory.NewService(memory.Config{Store: store})

	for name, svc := range map[string]*memory.Service{"hybrid-capable": withVec, "keyword-only": keywordOnly} {
		res, _, err := svc.SearchWithMode(context.Background(), memory.SearchInput{Query: q})
		if err != nil {
			t.Fatal(err)
		}
		if len(res) == 0 {
			t.Fatalf("%s: expected the on-topic memory", name)
		}
		top := res[0]
		if top.Score <= 0 || top.Score > memory.ScoreCeiling {
			t.Errorf("%s: score %v outside the contract bound", name, top.Score)
		}
		if top.Score < gate {
			t.Errorf("%s: on-topic score %v must clear the single gate floor %v", name, top.Score, gate)
		}
	}
}

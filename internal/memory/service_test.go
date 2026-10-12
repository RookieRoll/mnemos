package memory_test

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func newService(t *testing.T, clock func() time.Time) *memory.Service {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return memory.NewService(memory.Config{
		Store: db.Observations(),
		Clock: clock,
	})
}

func TestSaveValidation(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()

	cases := []memory.SaveInput{
		{Title: "", Content: "x", Type: memory.TypePattern},
		{Title: "t", Content: "", Type: memory.TypePattern},
		{Title: "t", Content: "c", Type: "garbage"},
		{Title: "t", Content: "c", Type: memory.TypePattern, Importance: 11},
	}
	for i, in := range cases {
		if _, err := svc.Save(ctx, in); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
}

func TestSaveGetRoundTrip(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()
	res, err := svc.Save(ctx, memory.SaveInput{
		Title:      "Use WAL",
		Content:    "Enable WAL for concurrent readers.",
		Type:       memory.TypePattern,
		Tags:       []string{"sqlite"},
		Importance: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Deduped {
		t.Fatal("first save should not dedup")
	}
	got, err := svc.Get(ctx, res.Observation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != res.Observation.Content {
		t.Errorf("round trip content mismatch")
	}
}

func TestSaveDedupsIdenticalContent(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()

	first, err := svc.Save(ctx, memory.SaveInput{
		Title:   "Same",
		Content: "identical body",
		Type:    memory.TypePattern,
		Project: "proj",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Save(ctx, memory.SaveInput{
		Title:   "Same",
		Content: "identical body",
		Type:    memory.TypePattern,
		Project: "proj",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Deduped {
		t.Error("second identical save should dedup")
	}
	if second.Observation.ID != first.Observation.ID {
		t.Error("dedup must return the existing ID")
	}
	got, _ := svc.Get(ctx, first.Observation.ID)
	if got.AccessCount < 1 {
		t.Error("dedup should bump access count")
	}
}

func TestSupersedeInvalidatesAndLinks(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()

	oldRes, _ := svc.Save(ctx, memory.SaveInput{
		Title: "Use X", Content: "we use X", Type: memory.TypeDecision, Importance: 5,
	})
	newRes, _ := svc.Save(ctx, memory.SaveInput{
		Title: "Use Y", Content: "we use Y now", Type: memory.TypeDecision, Importance: 7,
	})
	if err := svc.Supersede(ctx, newRes.Observation.ID, oldRes.Observation.ID); err != nil {
		t.Fatal(err)
	}

	results, err := svc.Search(ctx, memory.SearchInput{Query: "use"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Observation.ID == oldRes.Observation.ID {
			t.Error("old superseded observation should be excluded from default search")
		}
	}
}

func TestRankingPrefersImportantAndRecent(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()

	// Same text, different importance/recency.
	old := time.Now().Add(-60 * 24 * time.Hour).UTC()
	// Trick: we can't backdate via SaveInput, but the ranker uses CreatedAt
	// pulled from the store. For this test we only vary importance.
	lowRes, _ := svc.Save(ctx, memory.SaveInput{
		Title: "Pattern A", Content: "pattern about sqlite indexing",
		Type: memory.TypePattern, Importance: 3,
	})
	highRes, _ := svc.Save(ctx, memory.SaveInput{
		Title: "Pattern B", Content: "pattern about sqlite indexing",
		Type: memory.TypePattern, Importance: 10,
	})
	_ = old
	_ = lowRes

	results, err := svc.Search(ctx, memory.SearchInput{Query: "sqlite indexing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Fatalf("want 2+ results, got %d", len(results))
	}
	if results[0].Observation.ID != highRes.Observation.ID {
		t.Errorf("expected high-importance observation first, got %s", results[0].Observation.Title)
	}
}

func TestContextRespectsTokenBudget(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()

	for i := 0; i < 50; i++ {
		_, err := svc.Save(ctx, memory.SaveInput{
			Title:      fmt.Sprintf("Entry %d", i),
			Content:    fmt.Sprintf("content %d about sqlite indexing and FTS5 search ranking", i),
			Type:       memory.TypePattern,
			Importance: 5,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	block, err := svc.Context(ctx, memory.ContextInput{
		Query:     "sqlite",
		MaxTokens: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	if block.TokenEstimate > 200 {
		t.Errorf("budget exceeded: %d > 200", block.TokenEstimate)
	}
	if len(block.Observations) == 0 {
		t.Error("expected at least one observation in context block")
	}
	if block.Text == "" {
		t.Error("context text should not be empty")
	}
}

func TestSearchPreferProjectDownranksCrossProject(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()

	// Identical relevance and importance; only the project differs.
	other, _ := svc.Save(ctx, memory.SaveInput{
		Title: "Other project", Content: "pattern about sqlite indexing",
		Type: memory.TypePattern, Importance: 5, Project: "wayframer",
	})
	same, _ := svc.Save(ctx, memory.SaveInput{
		Title: "Same project", Content: "pattern about sqlite indexing",
		Type: memory.TypePattern, Importance: 5, Project: "taken",
	})
	global, _ := svc.Save(ctx, memory.SaveInput{
		Title: "Global", Content: "pattern about sqlite indexing",
		Type: memory.TypePattern, Importance: 5,
	})

	results, err := svc.Search(ctx, memory.SearchInput{
		Query:         "sqlite indexing",
		PreferProject: "taken",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	if results[2].Observation.ID != other.Observation.ID {
		t.Errorf("cross-project hit should rank last, got %s", results[2].Observation.Title)
	}
	scores := map[string]float64{}
	for _, r := range results {
		scores[r.Observation.ID] = r.Score
	}
	if scores[other.Observation.ID] >= scores[same.Observation.ID] {
		t.Error("cross-project score should be penalized below same-project")
	}
	if scores[global.Observation.ID] < scores[same.Observation.ID] {
		t.Error("project-less (global) hit must not be penalized")
	}

	// Without PreferProject the same query applies no penalty.
	unbiased, err := svc.Search(ctx, memory.SearchInput{Query: "sqlite indexing"})
	if err != nil {
		t.Fatal(err)
	}
	uscores := map[string]float64{}
	for _, r := range unbiased {
		uscores[r.Observation.ID] = r.Score
	}
	// Timestamps differ by milliseconds so scores are not exactly equal;
	// assert no penalty-sized gap rather than float equality.
	if uscores[other.Observation.ID] < uscores[same.Observation.ID]*0.9 {
		t.Error("without PreferProject, no cross-project penalty should apply")
	}
}

func TestDecayParamsShapeScore(t *testing.T) {
	r := memory.NewRanker(memory.DefaultRankParams())
	now := time.Now().UTC()
	fresh := memory.Observation{Importance: 5, CreatedAt: now}
	old := memory.Observation{Importance: 5, CreatedAt: now.AddDate(0, -6, 0)}
	freshScore := r.Score(fresh, 1.0, now)
	oldScore := r.Score(old, 1.0, now)
	if freshScore <= oldScore {
		t.Errorf("fresh should outrank old: fresh=%f old=%f", freshScore, oldScore)
	}
}

func TestService_SaveAndPinSemantics(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()

	// 1. Save with Pinned=true and TTLDays: pinning wins, ExpiresAt is nil.
	resPinned, err := svc.Save(ctx, memory.SaveInput{
		Title:   "pinned rule",
		Content: "permanent rule content",
		Type:    memory.TypeConvention,
		Pinned:  true,
		TTLDays: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resPinned.Observation.Pinned {
		t.Error("observation should be pinned")
	}
	if resPinned.Observation.ExpiresAt != nil {
		t.Errorf("pinning must clear expiry: got %v", resPinned.Observation.ExpiresAt)
	}

	// 2. Save with Pinned=false and TTLDays: expiry is set.
	resUnpinned, err := svc.Save(ctx, memory.SaveInput{
		Title:   "ephemeral note",
		Content: "temporary note content",
		Type:    memory.TypeContext,
		Pinned:  false,
		TTLDays: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resUnpinned.Observation.Pinned {
		t.Error("observation should not be pinned")
	}
	if resUnpinned.Observation.ExpiresAt == nil {
		t.Error("unpinned observation with TTLDays should have ExpiresAt set")
	}

	// 3. Pinning an existing observation with TTL clears expiry.
	pinnedNow, err := svc.Pin(ctx, resUnpinned.Observation.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !pinnedNow.Pinned {
		t.Error("observation should now be pinned")
	}
	if pinnedNow.ExpiresAt != nil {
		t.Errorf("pinning existing observation must clear expiry: got %v", pinnedNow.ExpiresAt)
	}

	// 4. Unpinning leaves ExpiresAt unset.
	unpinnedNow, err := svc.Pin(ctx, resUnpinned.Observation.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if unpinnedNow.Pinned {
		t.Error("observation should now be unpinned")
	}
	if unpinnedNow.ExpiresAt != nil {
		t.Errorf("unpinning should leave expiry unset: got %v", unpinnedNow.ExpiresAt)
	}

	// 5. Pinning nonexistent observation returns error.
	if _, err := svc.Pin(ctx, "nonexistent", true); err == nil {
		t.Error("pinning nonexistent observation should error")
	}
}

func TestSearch_GraphExpansion(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()

	// Seed background contrast so IDF is realistic and BM25 score reaches > 0.45.
	for i := 0; i < 20; i++ {
		_, _ = svc.Save(ctx, memory.SaveInput{
			Title:   fmt.Sprintf("unrelated topic %d", i),
			Content: fmt.Sprintf("content zebra giraffe elephant %d", i),
			Type:    memory.TypeContext,
		})
	}

	// Parent observation that will match query strongly (Importance 10, fresh)
	parent, err := svc.Save(ctx, memory.SaveInput{
		Title:      "postgres database tuning guide",
		Content:    "comprehensive database tuning and performance guide for high throughput",
		Type:       memory.TypeArchitecture,
		Importance: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 3 neighbours linked via refines that do NOT contain the query keywords
	// (so initial FTS recall does not find them, exercising graph expansion)
	n1, err := svc.Save(ctx, memory.SaveInput{
		Title:      "partition pruning strategies",
		Content:    "split heavy historical tables into weekly chunks for faster range scans",
		Type:       memory.TypeSemantic,
		Importance: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	n2, err := svc.Save(ctx, memory.SaveInput{
		Title:      "wal buffer write stalls",
		Content:    "increase write ahead log capacity to eliminate disk latency stalls",
		Type:       memory.TypeSemantic,
		Importance: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	n3, err := svc.Save(ctx, memory.SaveInput{
		Title:      "aggressive vacuum thresholds",
		Content:    "schedule automated cleaning cycles during off peak traffic hours",
		Type:       memory.TypeSemantic,
		Importance: 8,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Link parent to all three
	if err := svc.Link(ctx, parent.Observation.ID, n1.Observation.ID, memory.LinkRefines); err != nil {
		t.Fatal(err)
	}
	if err := svc.Link(ctx, parent.Observation.ID, n2.Observation.ID, memory.LinkRefines); err != nil {
		t.Fatal(err)
	}
	if err := svc.Link(ctx, parent.Observation.ID, n3.Observation.ID, memory.LinkRefines); err != nil {
		t.Fatal(err)
	}

	// Search matching parent's title/content
	res, err := svc.Search(ctx, memory.SearchInput{Query: "postgres database tuning"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		t.Logf("hit: id=%s title=%q score=%v expandedFrom=%q", r.Observation.ID, r.Observation.Title, r.Score, r.ExpandedFrom)
	}

	var expanded []memory.SearchResult
	for _, r := range res {
		if r.ExpandedFrom == parent.Observation.ID {
			expanded = append(expanded, r)
		}
	}

	// Per-parent cap is 2
	if len(expanded) != 2 {
		t.Fatalf("expected 2 expanded neighbours per parent cap, got %d", len(expanded))
	}
	for _, exp := range expanded {
		if math.Abs(exp.GraphFactor-0.70) > 1e-6 {
			t.Errorf("expanded hit must have GraphFactor=0.70, got %v", exp.GraphFactor)
		}
		expectedScore := exp.Relevance * exp.PolicyFactor * exp.GraphFactor * exp.ContradictionFactor
		if math.Abs(exp.Score-expectedScore) > 1e-6 {
			t.Errorf("expanded hit score mismatch: got %v, want %v", exp.Score, expectedScore)
		}
	}

	// Case: score <= 0.45 should NOT expand neighbours.
	// Low importance (1) + older created_at leads to low score.
	lowParent, err := svc.Save(ctx, memory.SaveInput{
		Title:      "obscure esoteric note",
		Content:    "random obscure details",
		Type:       memory.TypeContext,
		Importance: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	lowChild, err := svc.Save(ctx, memory.SaveInput{
		Title:      "obscure child",
		Content:    "esoteric sub-details",
		Type:       memory.TypeContext,
		Importance: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Link(ctx, lowParent.Observation.ID, lowChild.Observation.ID, memory.LinkRefines); err != nil {
		t.Fatal(err)
	}

	lowRes, err := svc.Search(ctx, memory.SearchInput{Query: "obscure random"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range lowRes {
		if r.ExpandedFrom == lowParent.Observation.ID {
			t.Errorf("low-scoring parent (score <= 0.45) must not expand neighbours: got %+v", r)
		}
	}
}

func TestSearch_ContradictionScoring(t *testing.T) {
	svc := newService(t, nil)
	ctx := context.Background()

	// 1. Curated contradiction: 0.50 penalty + warning
	cand1, err := svc.Save(ctx, memory.SaveInput{
		Title:      "use blocking queue for workers",
		Content:    "blocking queue ensures simple worker threads",
		Type:       memory.TypeArchitecture,
		Importance: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	refuter1, err := svc.Save(ctx, memory.SaveInput{
		Title:      "blocking queue causes thread exhaustion",
		Content:    "under load blocking queue starves pool",
		Type:       memory.TypeCorrection,
		TrustTier:  memory.TrustCurated,
		Importance: 9,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Link(ctx, refuter1.Observation.ID, cand1.Observation.ID, memory.LinkContradicts); err != nil {
		t.Fatal(err)
	}

	// 2. Raw-only contradiction: 0.75 penalty + unverified warning
	cand2, err := svc.Save(ctx, memory.SaveInput{
		Title:      "redis cluster caching strategy",
		Content:    "cache all metadata in redis cluster",
		Type:       memory.TypeArchitecture,
		Importance: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	refuter2, err := svc.Save(ctx, memory.SaveInput{
		Title:      "raw automated observation about redis",
		Content:    "benchmark indicates cluster latency spike",
		Type:       memory.TypeContext,
		SourceKind: memory.SourceTool, // will be clamped to TrustRaw
		Importance: 6,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Link(ctx, refuter2.Observation.ID, cand2.Observation.ID, memory.LinkContradicts); err != nil {
		t.Fatal(err)
	}

	// 3. Clean candidate: 1.0 factor, no warning
	clean, err := svc.Save(ctx, memory.SaveInput{
		Title:      "clean undisputed design pattern",
		Content:    "clean pattern with no contradictions",
		Type:       memory.TypePattern,
		Importance: 8,
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := svc.Search(ctx, memory.SearchInput{Query: "queue redis clean"})
	if err != nil {
		t.Fatal(err)
	}

	byID := map[string]memory.SearchResult{}
	for _, r := range res {
		byID[r.Observation.ID] = r
	}

	// Verify cand1: 0.50 factor + curated warning
	c1, ok := byID[cand1.Observation.ID]
	if !ok {
		t.Fatalf("cand1 missing from results")
	}
	if math.Abs(c1.ContradictionFactor-0.50) > 1e-6 {
		t.Errorf("cand1 ContradictionFactor = %v, want 0.50", c1.ContradictionFactor)
	}
	if len(c1.ContradictedBy) != 1 || c1.ContradictedBy[0] != refuter1.Observation.ID {
		t.Errorf("cand1 ContradictedBy = %v, want [%s]", c1.ContradictedBy, refuter1.Observation.ID)
	}
	if !strings.Contains(c1.Snippet, "[WARNING: Contradicted by") || strings.Contains(c1.Snippet, "unverified source") {
		t.Errorf("cand1 snippet must have curated warning: %s", c1.Snippet)
	}

	// Verify cand2: 0.75 factor + unverified warning
	c2, ok := byID[cand2.Observation.ID]
	if !ok {
		t.Fatalf("cand2 missing from results")
	}
	if math.Abs(c2.ContradictionFactor-0.75) > 1e-6 {
		t.Errorf("cand2 ContradictionFactor = %v, want 0.75", c2.ContradictionFactor)
	}
	if !strings.Contains(c2.Snippet, "unverified source") {
		t.Errorf("cand2 snippet must have unverified source warning: %s", c2.Snippet)
	}

	// Verify clean: 1.0 factor + no warning
	cl, ok := byID[clean.Observation.ID]
	if !ok {
		t.Fatalf("clean missing from results")
	}
	if math.Abs(cl.ContradictionFactor-1.0) > 1e-6 {
		t.Errorf("clean ContradictionFactor = %v, want 1.0", cl.ContradictionFactor)
	}
	if len(cl.ContradictedBy) != 0 {
		t.Errorf("clean should have empty ContradictedBy, got %v", cl.ContradictedBy)
	}
	if strings.Contains(cl.Snippet, "WARNING") {
		t.Errorf("clean snippet should not contain warning: %s", cl.Snippet)
	}
}

// TestSearch_GraphExpansionHonoursVisibility is the review fix guard: a graph
// neighbour must not become a back door past the query's visibility contract.
// Raw-tier neighbours stay quarantined unless the query opts into raw results,
// and the query's type and project constraints apply to expanded neighbours
// unchanged. Neighbour content deliberately shares no keywords with the query
// so the neighbour can only surface through graph expansion.
func TestSearch_GraphExpansionHonoursVisibility(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name      string
		parent    memory.SaveInput
		neighbour memory.SaveInput
		search    memory.SearchInput
		wantShown bool
	}{
		{
			name: "raw neighbour quarantined by default",
			parent: memory.SaveInput{
				Title: "postgres database tuning guide", Content: "comprehensive database tuning and performance guide for high throughput",
				Type: memory.TypeArchitecture, Project: "mnemos", Importance: 10,
			},
			neighbour: memory.SaveInput{
				Title: "internal tool dump", Content: "verbose process output log capture",
				Type: memory.TypeSemantic, Importance: 8, SourceKind: memory.SourceTool,
			},
			search:    memory.SearchInput{Query: "postgres database tuning"},
			wantShown: false,
		},
		{
			name: "raw neighbour shown when query opts into raw",
			parent: memory.SaveInput{
				Title: "postgres database tuning guide", Content: "comprehensive database tuning and performance guide for high throughput",
				Type: memory.TypeArchitecture, Project: "mnemos", Importance: 10,
			},
			neighbour: memory.SaveInput{
				Title: "internal tool dump", Content: "verbose process output log capture",
				Type: memory.TypeSemantic, Importance: 8, SourceKind: memory.SourceTool,
			},
			search:    memory.SearchInput{Query: "postgres database tuning", IncludeRaw: true},
			wantShown: true,
		},
		{
			name: "type filter applies to neighbours",
			parent: memory.SaveInput{
				Title: "postgres database tuning guide", Content: "comprehensive database tuning and performance guide for high throughput",
				Type: memory.TypeArchitecture, Project: "mnemos", Importance: 10,
			},
			neighbour: memory.SaveInput{
				Title: "launch party memory", Content: "casual recollection of the celebration evening",
				Type: memory.TypeEpisodic, Importance: 8,
			},
			search:    memory.SearchInput{Query: "postgres database tuning", Type: memory.TypeArchitecture},
			wantShown: false,
		},
		{
			name: "project filter applies to neighbours",
			parent: memory.SaveInput{
				Title: "postgres database tuning guide", Content: "comprehensive database tuning and performance guide for high throughput",
				Type: memory.TypeArchitecture, Project: "mnemos", Importance: 10,
			},
			neighbour: memory.SaveInput{
				Title: "foreign team note", Content: "notes for another team entirely elsewhere",
				Type: memory.TypeArchitecture, Importance: 8, Project: "other-proj",
			},
			search:    memory.SearchInput{Query: "postgres database tuning", Project: "mnemos"},
			wantShown: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newService(t, nil)
			for i := 0; i < 20; i++ {
				_, _ = svc.Save(ctx, memory.SaveInput{
					Title:   fmt.Sprintf("unrelated topic %d", i),
					Content: fmt.Sprintf("content zebra giraffe elephant %d", i),
					Type:    memory.TypeContext,
				})
			}
			parent, err := svc.Save(ctx, tc.parent)
			if err != nil {
				t.Fatal(err)
			}
			nb, err := svc.Save(ctx, tc.neighbour)
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.Link(ctx, parent.Observation.ID, nb.Observation.ID, memory.LinkRefines); err != nil {
				t.Fatal(err)
			}

			res, err := svc.Search(ctx, tc.search)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, r := range res {
				if r.Observation.ID != nb.Observation.ID {
					continue
				}
				found = true
				if r.ExpandedFrom != parent.Observation.ID {
					t.Errorf("ExpandedFrom = %q, want %q", r.ExpandedFrom, parent.Observation.ID)
				}
			}
			if found != tc.wantShown {
				t.Errorf("neighbour surfaced = %v, want %v", found, tc.wantShown)
			}
		})
	}
}

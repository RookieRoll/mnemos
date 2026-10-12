package memory_test

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// TestScenario_LifecycleAndGraphRetrieval exercises task 7.3:
// save a pinned convention -> confirm Tier-0 presence in session_start prewarm
// -> search a query and confirm expanded_from graph annotation
// -> search a refuted memory and confirm contradiction warning and lower ranking.
func TestScenario_LifecycleAndGraphRetrieval(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(dir, "e2e_scenario.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	mem := memory.NewService(memory.Config{Store: db.Observations()})
	pw := prewarm.NewService(prewarm.Config{
		Observations: db.Observations(),
		Sessions:     db.Sessions(),
		Skills:       db.Skills(),
		Touches:      db.Touches(),
	})

	// 1. Save a pinned convention
	pinnedConv, err := mem.Save(ctx, memory.SaveInput{
		Title:      "zero cgo binary requirement",
		Content:    "all release binaries must be compiled with CGO_ENABLED=0 pure go",
		Type:       memory.TypeConvention,
		Project:    "mnemos",
		Importance: 10,
		Pinned:     true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. Confirm Tier-0 presence in session_start prewarm block
	block, err := pw.Build(ctx, prewarm.Request{
		Mode:    prewarm.ModeSessionStart,
		Project: "mnemos",
		Goal:    "bootstrap new feature",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(block.Sections) == 0 || block.Sections[0].Title != "core invariants (pinned)" {
		t.Fatalf("first section must be 'core invariants (pinned)', got %+v", block.Sections)
	}
	if !strings.Contains(block.Sections[0].Body, pinnedConv.Observation.Title) {
		t.Fatalf("Tier-0 block missing pinned convention: %s", block.Sections[0].Body)
	}

	// 3. Graph expansion: parent + linked refinement
	// Seed background contrast docs so BM25 score exceeds 0.45 threshold
	for i := 0; i < 20; i++ {
		_, _ = mem.Save(ctx, memory.SaveInput{
			Title:   fmt.Sprintf("unrelated background fact %d", i),
			Content: fmt.Sprintf("unrelated content zebra elephant giraffe %d", i),
			Type:    memory.TypeContext,
		})
	}

	parentArch, err := mem.Save(ctx, memory.SaveInput{
		Title:      "database sharding strategy",
		Content:    "database sharding strategy guidelines for high throughput relational database sharding cluster",
		Type:       memory.TypeArchitecture,
		Importance: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	childRefine, err := mem.Save(ctx, memory.SaveInput{
		Title:      "consistent hashing ring",
		Content:    "virtual vnodes distribution algorithm for key partition placement",
		Type:       memory.TypeSemantic,
		Importance: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.Link(ctx, parentArch.Observation.ID, childRefine.Observation.ID, memory.LinkRefines); err != nil {
		t.Fatal(err)
	}

	// Search for parent topic
	res1, err := mem.Search(ctx, memory.SearchInput{Query: "database sharding strategy"})
	if err != nil {
		t.Fatal(err)
	}

	var foundExpanded *memory.SearchResult
	for _, r := range res1 {
		if r.Observation.ID == childRefine.Observation.ID {
			rCopy := r
			foundExpanded = &rCopy
			break
		}
	}
	if foundExpanded == nil {
		t.Fatal("expected childRefine to appear in results via graph expansion")
	}
	if foundExpanded.ExpandedFrom != parentArch.Observation.ID {
		t.Errorf("expected ExpandedFrom = %q, got %q", parentArch.Observation.ID, foundExpanded.ExpandedFrom)
	}
	if foundExpanded.GraphFactor != 0.70 {
		t.Errorf("expected GraphFactor = 0.70, got %v", foundExpanded.GraphFactor)
	}

	// 4. Contradiction penalty and warning
	refutedProposal, err := mem.Save(ctx, memory.SaveInput{
		Title:      "single global mutex lock",
		Content:    "use single mutex lock to synchronize all state transitions",
		Type:       memory.TypeArchitecture,
		Importance: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	correctiveFact, err := mem.Save(ctx, memory.SaveInput{
		Title:      "fine grained lock striping for state transitions",
		Content:    "single mutex lock causes fatal contention synchronize with fine grained striped locks",
		Type:       memory.TypeCorrection,
		TrustTier:  memory.TrustCurated,
		Importance: 9,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.Link(ctx, correctiveFact.Observation.ID, refutedProposal.Observation.ID, memory.LinkContradicts); err != nil {
		t.Fatal(err)
	}

	res2, err := mem.Search(ctx, memory.SearchInput{Query: "mutex lock synchronize contention"})
	if err != nil {
		t.Fatal(err)
	}

	var refutedHit, correctiveHit *memory.SearchResult
	for _, r := range res2 {
		if r.Observation.ID == refutedProposal.Observation.ID {
			rCopy := r
			refutedHit = &rCopy
		}
		if r.Observation.ID == correctiveFact.Observation.ID {
			rCopy := r
			correctiveHit = &rCopy
		}
	}
	if refutedHit == nil || correctiveHit == nil {
		t.Fatalf("expected both hits in search results, got refuted=%v, corrective=%v", refutedHit, correctiveHit)
	}

	if math.Abs(refutedHit.ContradictionFactor-0.50) > 1e-6 {
		t.Errorf("refuted candidate must receive 0.50 ContradictionFactor, got %v", refutedHit.ContradictionFactor)
	}
	if !strings.Contains(refutedHit.Snippet, "[WARNING: Contradicted by") {
		t.Errorf("refuted candidate snippet must carry warning: %s", refutedHit.Snippet)
	}
	// Contradiction penalty halves the final score relative to its uncontradicted product
	unpenalizedScore := min(memory.ScoreCeiling, refutedHit.Relevance*refutedHit.PolicyFactor*refutedHit.GraphFactor)
	expectedScore := min(memory.ScoreCeiling, unpenalizedScore*0.50)
	if math.Abs(refutedHit.Score-expectedScore) > 1e-6 {
		t.Errorf("refuted candidate score must be exactly halved by 0.50 penalty: score=%v, expected=%v",
			refutedHit.Score, expectedScore)
	}
}

package memory_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func hasTier(tiers []string, want string) bool {
	for _, t := range tiers {
		if t == want {
			return true
		}
	}
	return false
}

func TestCapabilitiesAlwaysReportsCoreAndProvenance(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	t.Cleanup(func() { _ = db.Close() })

	svc := memory.NewService(memory.Config{Store: db.Observations()}) // no embedder
	c := svc.Capabilities()

	if c.Spec != memory.SpecVersion {
		t.Errorf("spec = %q, want %q", c.Spec, memory.SpecVersion)
	}
	if !c.Bitemporal || !c.Provenance || !c.TrustTiers || !c.TypedLinks || !c.DeterministicDedup || !c.PinnedMemory || !c.GraphRetrieval {
		t.Errorf("schema-level capabilities must all be true, got %+v", c)
	}
	if !hasTier(c.Tiers, "memory-core") || !hasTier(c.Tiers, "provenance") {
		t.Errorf("tiers must include memory-core and provenance, got %v", c.Tiers)
	}
}

// TestCapabilitiesHybridTierIsBehaviorBound is the anti-lie guard: the
// embeddings tier must track whether hybrid retrieval can actually run, not a
// static config or constant.
func TestCapabilitiesHybridTierIsBehaviorBound(t *testing.T) {
	dir := t.TempDir()
	db, _ := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	t.Cleanup(func() { _ = db.Close() })

	// No embedder: hybrid is not live, so the embeddings tier must be absent.
	off := memory.NewService(memory.Config{Store: db.Observations()})
	if c := off.Capabilities(); c.HybridRetrieval || hasTier(c.Tiers, "embeddings") {
		t.Errorf("without embedder the embeddings tier must be absent, got tiers=%v hybrid=%v", c.Tiers, c.HybridRetrieval)
	}

	// Embedder configured: hybrid is live, so the embeddings tier must appear.
	on := memory.NewService(memory.Config{
		Store:    db.Observations(),
		Embedder: fixedEmbedder{dim: 4, vec: []float32{1, 0, 0, 0}},
	})
	c := on.Capabilities()
	if !c.HybridRetrieval || !hasTier(c.Tiers, "embeddings") {
		t.Errorf("with embedder the embeddings tier must be present, got tiers=%v hybrid=%v", c.Tiers, c.HybridRetrieval)
	}

	// Fidelity-preserving export/import ships, so the portable tier is always
	// present regardless of embedder state.
	if !c.PortableExport || !hasTier(c.Tiers, "portable") {
		t.Errorf("portable tier must be present, got tiers=%v portable=%v", c.Tiers, c.PortableExport)
	}
}

// TestCapabilitiesPinnedMemorySemantics holds the D9 boundary in place: the
// flag stays true because pinning is live in the default core profile through
// mnemos_save's pinned parameter, and the sentence next to it must name the
// maintenance-only tool that changes pin state after the fact. A bare true
// flag reads as "post-hoc pinning is available here", which is wrong on core.
func TestCapabilitiesPinnedMemorySemantics(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	c := memory.NewService(memory.Config{Store: db.Observations()}).Capabilities()
	if !c.PinnedMemory {
		t.Fatal("pinned_memory must stay true: pinning is live in core through the mnemos_save pinned parameter")
	}

	tests := []struct {
		name string
		want string
	}{
		{"names the save surface that can pin", "mnemos_save"},
		{"names the save-time parameter", "pinned parameter"},
		{"names the default profile", "core"},
		{"names the post-hoc tool", "mnemos_pin"},
		{"names the profile that registers it", "maintenance"},
		{"scopes pin state on core to save time", "only at save time"},
	}
	for _, tt := range tests {
		if !strings.Contains(c.PinnedMemorySemantics, tt.want) {
			t.Errorf("%s: pinned_memory_semantics must mention %q, got %q", tt.name, tt.want, c.PinnedMemorySemantics)
		}
	}

	// The mnemos://capabilities resource marshals this struct verbatim
	// (internal/mcp readCapabilities), so the sentence has to survive JSON
	// encoding under its documented key and the flag has to stay true there.
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal capabilities: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal capabilities: %v", err)
	}
	if doc["pinned_memory"] != true {
		t.Errorf("pinned_memory in the capability JSON = %v, want true", doc["pinned_memory"])
	}
	if got, _ := doc["pinned_memory_semantics"].(string); got != c.PinnedMemorySemantics {
		t.Errorf("pinned_memory_semantics in the capability JSON = %v, want %q", doc["pinned_memory_semantics"], c.PinnedMemorySemantics)
	}
}

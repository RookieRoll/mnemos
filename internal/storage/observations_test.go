package storage_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// TestObservationPinnedRoundTrip verifies the pinned flag survives every
// row-scanning path (get / search / list) at both values.
func TestObservationPinnedRoundTrip(t *testing.T) {
	db := openTestDB(t)
	store := db.Observations()
	ctx := context.Background()
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	pinned := &memory.Observation{
		ID: "obs_pinned", AgentID: "agentX", Project: "projX",
		Title: "zero cgo invariant", Content: "the binary must stay cgo free",
		Type: memory.TypeConvention, Importance: 9, Pinned: true,
		CreatedAt: now, ValidFrom: now,
	}
	plain := &memory.Observation{
		ID: "obs_plain", AgentID: "agentX", Project: "projX",
		Title: "a passing note", Content: "something transient about cgo tooling",
		Type: memory.TypeContext, Importance: 5,
		CreatedAt: now, ValidFrom: now,
	}
	for _, o := range []*memory.Observation{pinned, plain} {
		if err := store.Insert(ctx, o); err != nil {
			t.Fatalf("insert %s: %v", o.ID, err)
		}
	}

	got, err := store.Get(ctx, pinned.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Pinned {
		t.Error("get: pinned row came back unpinned")
	}

	hits, err := store.Search(ctx, memory.SearchInput{Query: "cgo", AgentID: "agentX", Project: "projX"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, h := range hits {
		seen[h.Observation.ID] = h.Observation.Pinned
	}
	if p, ok := seen[pinned.ID]; !ok || !p {
		t.Errorf("search: pinned row missing or flag lost (seen=%v)", seen)
	}
	if p, ok := seen[plain.ID]; ok && p {
		t.Errorf("search: plain row came back pinned (seen=%v)", seen)
	}

	list, err := store.ListByProject(ctx, "agentX", "projX", memory.TypeConvention, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !list[0].Pinned {
		t.Errorf("list: pinned flag lost: %+v", list)
	}
}

// TestObservationPinnedCheckRejectsInvalid pins the CHECK constraint: only
// 0 and 1 may reach the column.
func TestObservationPinnedCheckRejectsInvalid(t *testing.T) {
	db := openTestDB(t)
	_, err := db.SQL().Exec(`INSERT INTO observations (id, title, content, obs_type, pinned)
		VALUES ('bad', 't', 'c', 'semantic', 2)`)
	if err == nil {
		t.Fatal("pinned=2 must violate CHECK (pinned IN (0, 1))")
	}
	if !strings.Contains(err.Error(), "CHECK") {
		t.Errorf("expected CHECK constraint error, got: %v", err)
	}
}

// TestDecayImportanceSkipsPinned verifies the dream pass's idle decay
// leaves pinned rows untouched while unpinned stale rows decay.
func TestDecayImportanceSkipsPinned(t *testing.T) {
	db := openTestDB(t)
	store := db.Observations()
	ctx := context.Background()
	old := time.Now().UTC().AddDate(0, 0, -60)

	pinned := &memory.Observation{
		ID: "dec_pinned", AgentID: "agentX", Title: "pinned old", Content: "core memory content",
		Type: memory.TypeArchitecture, Importance: 8, Pinned: true,
		CreatedAt: old, ValidFrom: old,
	}
	plain := &memory.Observation{
		ID: "dec_plain", AgentID: "agentX", Title: "plain old", Content: "transient content",
		Type: memory.TypeBugfix, Importance: 8,
		CreatedAt: old, ValidFrom: old,
	}
	for _, o := range []*memory.Observation{pinned, plain} {
		if err := store.Insert(ctx, o); err != nil {
			t.Fatalf("insert %s: %v", o.ID, err)
		}
	}

	n, err := store.DecayImportance(ctx, 30, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("decayed rows = %d, want 1 (pinned exempt)", n)
	}

	gp, err := store.Get(ctx, pinned.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gp.Importance != 8 {
		t.Errorf("pinned importance = %d, want 8", gp.Importance)
	}
	gu, err := store.Get(ctx, plain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gu.Importance != 7 {
		t.Errorf("plain importance = %d, want 7", gu.Importance)
	}
}

// TestListNeighborsFor covers both edge directions on both edge kinds
// (refines links and derived_from references), dead-endpoint exclusion,
// wrong-edge-kind filtering, and the deterministic per-parent cap.
func TestListNeighborsFor(t *testing.T) {
	db := openTestDB(t)
	store := db.Observations()
	ctx := context.Background()
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	mk := func(id string, typ memory.ObsType, derivedFrom []string) *memory.Observation {
		o := &memory.Observation{
			ID: id, AgentID: "agentX", Project: "projX",
			Title: id, Content: "neighbour body " + id, Type: typ,
			Importance: 5, DerivedFrom: derivedFrom, CreatedAt: now, ValidFrom: now,
		}
		if err := store.Insert(ctx, o); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
		return o
	}
	mk("nb_par", memory.TypeSemantic, nil) // np derives from nb_par
	mk("np", memory.TypeContext, []string{"nb_par"})
	mk("nb_a", memory.TypeSemantic, nil)
	mk("nb_b", memory.TypeSemantic, []string{"np"}) // derived from np
	mk("nb_c", memory.TypeSemantic, nil)
	mk("nb_dead", memory.TypeSemantic, nil)
	mk("nb_rev", memory.TypeSemantic, nil)
	mk("nb_unrelated", memory.TypeSemantic, nil)

	link := func(from, to string, lt memory.LinkType) {
		if err := store.Link(ctx, from, to, lt); err != nil {
			t.Fatalf("link %s->%s: %v", from, to, err)
		}
	}
	link("np", "nb_a", memory.LinkRefines)
	link("np", "nb_c", memory.LinkRefines)
	link("np", "nb_dead", memory.LinkRefines)
	link("nb_rev", "np", memory.LinkRefines)       // parent as target
	link("np", "nb_unrelated", memory.LinkRelated) // wrong edge kind
	if err := store.Invalidate(ctx, "nb_dead", now); err != nil {
		t.Fatal(err)
	}

	got, err := store.ListNeighborsFor(ctx, []string{"np"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, n := range got {
		if n.ParentID != "np" {
			t.Errorf("neighbour parent = %q, want np", n.ParentID)
		}
		ids = append(ids, n.Observation.ID)
	}
	// Cap 2, deterministic order (neighbour created_at ties break on id):
	// nb_a and nb_b win; nb_c/nb_par/nb_rev are capped out; nb_dead is
	// dead; nb_unrelated is the wrong edge kind.
	if len(ids) != 2 || ids[0] != "nb_a" || ids[1] != "nb_b" {
		t.Errorf("capped expansion = %v, want [nb_a nb_b]", ids)
	}

	// No cap: both directions of both edge kinds, dead and wrong-kind
	// still excluded.
	got, err = store.ListNeighborsFor(ctx, []string{"np"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	ids = ids[:0]
	for _, n := range got {
		ids = append(ids, n.Observation.ID)
	}
	if len(ids) != 5 {
		t.Errorf("uncapped expansion = %v, want 5 entries (a,b,c,par,rev)", ids)
	}
	joined := strings.Join(ids, ",")
	for _, want := range []string{"nb_a", "nb_b", "nb_c", "nb_par", "nb_rev"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing neighbour %s in %v", want, ids)
		}
	}
}

// TestListContradictors covers both edge directions, trust-tier carriage,
// and dead-endpoint exclusion.
func TestListContradictors(t *testing.T) {
	db := openTestDB(t)
	store := db.Observations()
	ctx := context.Background()
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	mk := func(id string, tier memory.TrustTier) {
		o := &memory.Observation{
			ID: id, AgentID: "agentX", Project: "projX",
			Title: id, Content: "body " + id, Type: memory.TypeCorrection,
			Importance: 5, TrustTier: tier, CreatedAt: now, ValidFrom: now,
		}
		if err := store.Insert(ctx, o); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	mk("cand", memory.TrustCurated)
	mk("ref_c", memory.TrustCurated)
	mk("ref_r", memory.TrustRaw)
	mk("ref_dead", memory.TrustCurated)

	link := func(from, to string) {
		if err := store.Link(ctx, from, to, memory.LinkContradicts); err != nil {
			t.Fatalf("link %s->%s: %v", from, to, err)
		}
	}
	link("ref_c", "cand")    // candidate as target
	link("cand", "ref_r")    // candidate as source
	link("ref_dead", "cand") // refuter dies
	if err := store.Invalidate(ctx, "ref_dead", now); err != nil {
		t.Fatal(err)
	}

	got, err := store.ListContradictors(ctx, []string{"cand"})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]memory.TrustTier{}
	for _, c := range got {
		if c.CandidateID != "cand" {
			t.Errorf("candidate = %q, want cand", c.CandidateID)
		}
		byID[c.Observation.ID] = c.Observation.TrustTier
	}
	if len(byID) != 2 {
		t.Fatalf("contradictors = %v, want ref_c + ref_r (dead excluded)", byID)
	}
	if byID["ref_c"] != memory.TrustCurated || byID["ref_r"] != memory.TrustRaw {
		t.Errorf("trust tiers not carried: %v", byID)
	}
}

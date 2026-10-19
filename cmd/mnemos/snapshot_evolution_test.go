package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
)

func writeEvolutionSnapshot(t *testing.T, snap *snapshot) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "snapshot.json")
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEvolutionSnapshotRoundTripAndRepeat(t *testing.T) {
	ctx := context.Background()
	homeWithConfig(t, "")
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old, err := d.mem.Save(ctx, memory.SaveInput{Project: "p", Title: "old transaction rule", Content: "transaction requires lock", Type: memory.TypeConvention, Pinned: true, TopicKey: "database/transactions", Evidence: []memory.EvidenceReference{{Locator: "https://example.invalid/no-fetch", Relation: "supports", Fingerprint: strings.Repeat("a", 64)}}})
	if err != nil {
		t.Fatal(err)
	}
	ref := old.Observation.Evidence[0]
	if _, err := d.mem.CheckEvidence(ctx, memory.EvidenceCheckInput{ObservationID: old.Observation.ID, EvidenceKey: ref.Key, Status: "changed"}); err != nil {
		t.Fatal(err)
	}
	next, err := d.mem.Save(ctx, memory.SaveInput{Project: "p", Title: "new transaction rule", Content: "transaction uses optimistic updates", Type: memory.TypeConvention, ReplacesID: old.Observation.ID, Evidence: []memory.EvidenceReference{{Locator: "repo/decision.md", Relation: "supports", Revision: "abc", Selector: "section2"}}})
	if err != nil {
		t.Fatal(err)
	}
	// Ordinary graph edges retain their existing narrower portability contract;
	// they must not poison the dedicated supersession export section.
	if err := d.mem.Link(ctx, next.Observation.ID, old.Observation.ID, memory.LinkRelated); err != nil {
		t.Fatal(err)
	}
	before, err := d.db.Observations().Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := dumpAll(ctx, d.db)
	if err != nil {
		t.Fatal(err)
	}
	after, err := d.db.Observations().Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("export changed access or owner data")
	}
	if snap.SchemaVersion != 2 || len(snap.Links) != 1 || len(snap.Evidence) != 2 {
		t.Fatalf("incomplete snapshot: %+v", snap)
	}
	if snap.Links[0].SourceID != next.Observation.ID || snap.Links[0].TargetID != old.Observation.ID || snap.Links[0].LinkedAt.IsZero() || snap.Links[0].ReplacementHash == "" {
		t.Fatal("historical link or retry identity omitted")
	}
	file := writeEvolutionSnapshot(t, snap)
	d.close()
	homeWithConfig(t, "")
	if err := runImport(ctx, []string{file}); err != nil {
		t.Fatal(err)
	}
	restored, err := loadDeps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := dumpAll(ctx, restored.db)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snap, fresh) {
		a, _ := json.Marshal(snap)
		b, _ := json.Marshal(fresh)
		t.Fatalf("fresh round-trip differs\n%s\n%s", a, b)
	}
	if _, err := restored.mem.CheckEvidence(ctx, memory.EvidenceCheckInput{ObservationID: old.Observation.ID, EvidenceKey: ref.Key, Status: "unavailable"}); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.mem.Pin(ctx, next.Observation.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.mem.Get(ctx, next.Observation.ID); err != nil {
		t.Fatal(err)
	}
	localBefore, err := dumpAll(ctx, restored.db)
	if err != nil {
		t.Fatal(err)
	}
	restored.close()
	if err := runImport(ctx, []string{file}); err != nil {
		t.Fatalf("matching repeated import: %v", err)
	}
	repeated, err := loadDeps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer repeated.close()
	localAfter, err := dumpAll(ctx, repeated.db)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(localBefore, localAfter) {
		t.Fatal("reimport overwrote local latest check/pin/access metadata")
	}
	data, _ := json.Marshal(snap)
	var mismatched snapshot
	_ = json.Unmarshal(data, &mismatched)
	mismatched.Observations[0].Title = "unrelated owner identity"
	if err := runImport(ctx, []string{writeEvolutionSnapshot(t, &mismatched)}); err == nil {
		t.Fatal("existing same-ID different owner identity accepted")
	}
	unchanged, err := dumpAll(ctx, repeated.db)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(localAfter, unchanged) {
		t.Fatal("failed identity preflight mutated metadata")
	}
}

func TestEvolutionSnapshotPreflight(t *testing.T) {
	for _, tc := range []struct {
		name    string
		snap    snapshot
		wantErr bool
	}{
		{"legacy", snapshot{}, false},
		{"future", snapshot{SchemaVersion: 3}, true},
		{"orphan evidence", snapshot{SchemaVersion: 2, Evidence: []snapshotEvidence{{ObservationID: "missing", Evidence: memory.EvidenceReference{Key: "key"}}}}, true},
		{"orphan link", snapshot{SchemaVersion: 2, Links: []memory.LinkEdge{{LinkType: memory.LinkSupersedes, SourceID: "new", TargetID: "old"}}}, true},
		{"duplicate owners", snapshot{Observations: []memory.Observation{{ID: "x"}, {ID: "x"}}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := prepareSnapshot(&tc.snap)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

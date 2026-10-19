package main

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
	"github.com/polyxmedia/mnemos/internal/verify"
)

func TestLabelledDeliveryUsesProductionGateWithoutWrites(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "evaluation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	store := db.Observations()
	svc := memory.NewService(memory.Config{Store: store, Clock: func() time.Time { return now }})
	d := &deps{db: db, mem: svc}
	for i, title := range []string{"frobnicate the widget safely", "react hooks", "docker compose", "css grid", "git rebase", "tls certs"} {
		o := &memory.Observation{ID: title, Title: title, Content: title + " workflow guidance", Type: memory.TypeConvention, Project: "p", Importance: 9, TrustTier: memory.TrustCurated, CreatedAt: now, ValidFrom: now}
		if i == 0 {
			o.Content = "always frobnicate the widget before shipping"
		}
		if err := store.Insert(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	yes := true
	fix := &verify.RetrievalFixture{Cases: []verify.RetrievalCase{
		{Name: "weak cross-project", Project: "other", Queries: []string{"frobnicate widget safely"}, ExpectNoInjection: &yes},
		{Name: "unexpected injection", Project: "p", Queries: []string{"frobnicate widget safely"}, ExpectNoInjection: &yes},
	}}
	deliver := func(ctx context.Context, c verify.RetrievalCase, q string) ([]injection.Ref, error) {
		pm := collectPromptMemoryAt(ctx, d, q, "", c.Project, "", now, false)
		var refs []injection.Ref
		for _, h := range pm.Hits {
			refs = append(refs, injection.Ref{Kind: injection.KindObservation, ID: h.Observation.ID})
		}
		return refs, nil
	}
	candidates, err := svc.Search(ctx, memory.SearchInput{Query: "frobnicate widget safely", PreferProject: "other", Limit: 3})
	if err != nil || len(candidates) == 0 {
		t.Fatalf("weak test needs actual candidates: %v %v", candidates, err)
	}
	first, err := verify.RunRetrievalWithDelivery(ctx, svc, fix, deliver)
	if err != nil {
		t.Fatal(err)
	}
	second, err := verify.RunRetrievalWithDelivery(ctx, svc, fix, deliver)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("fixed-clock reports differ")
	}
	if !first.Cases[0].Pass || first.Cases[1].Pass || len(first.Cases[1].Queries[0].Selected) == 0 {
		t.Fatalf("actual gate verdicts=%+v", first.Cases)
	}
	rows, err := store.ListByProject(ctx, "", "p", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 6 {
		t.Fatalf("evaluation inserted memories: %d", len(rows))
	}
	for _, o := range rows {
		if o.AccessCount != 0 {
			t.Fatalf("evaluation changed access count for %s", o.ID)
		}
		events, err := db.Injections().ListByRef(ctx, injection.KindObservation, o.ID, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 0 {
			t.Fatal("evaluation recorded injections")
		}
	}
}

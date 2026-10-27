package main

import (
	"context"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

func TestConfiguredNearDuplicateDisable(t *testing.T) {
	homeWithConfig(t, "[search]\nnear_dup_window = \"0s\"\n[embedding]\nprovider = \"none\"\n")
	d, err := loadDeps(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	a, err := d.mem.Save(context.Background(), memory.SaveInput{Title: "first", Content: "Use WAL.", Type: memory.TypeContext})
	if err != nil {
		t.Fatal(err)
	}
	b, err := d.mem.Save(context.Background(), memory.SaveInput{Title: "second", Content: "Use WAL!", Type: memory.TypeContext})
	if err != nil {
		t.Fatal(err)
	}
	if a.Observation.ID == b.Observation.ID {
		t.Fatal("near_dup_window=0s ignored: production composition still folds saves")
	}
}

func TestConfiguredRawPromotionNever(t *testing.T) {
	homeWithConfig(t, "[rumination]\nenabled = true\nraw_promote = \"never\"\nstale_raw_days = 1\n[embedding]\nprovider = \"none\"\n")
	ctx := context.Background()
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	saved, err := d.mem.Save(ctx, memory.SaveInput{Title: "old inference", Content: "old inference content", Type: memory.TypeContext, SourceKind: memory.SourceAgentInference})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-48 * time.Hour)
	if _, err := d.db.SQL().Exec(`UPDATE observations SET created_at=?,valid_from=? WHERE id=?`, old, old, saved.Observation.ID); err != nil {
		t.Fatal(err)
	}
	got, err := d.rum.Detect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("raw_promote=%q ignored: detected %d candidates", d.cfg.Rumination.RawPromote, len(got))
	}
}

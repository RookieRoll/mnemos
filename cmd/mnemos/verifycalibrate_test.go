package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// TestVerifyCalibrateAndRetrievalOnSeededStore runs the two cheap verify
// modes end to end against a seeded store: calibration must emit its
// parameter report on the contract scale, and the retrieval probes must
// pass (precision@K on the seeded corpus). The fixture is written from the
// seeded IDs so the whole path — fixture load, search, labels, sweep,
// report — is exercised for real.
func TestVerifyCalibrateAndRetrievalOnSeededStore(t *testing.T) {
	withHome(t)
	ctx := context.Background()

	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	// Corpus contrast so BM25 magnitudes are meaningful.
	for i, title := range []string{"react hooks", "docker compose", "css grid", "git rebase", "tls certs"} {
		if _, err := d.mem.Save(ctx, memory.SaveInput{
			Title: title, Content: "unrelated filler content " + strings.Repeat("noise ", i+1),
			Type: memory.TypeContext, Project: "api",
		}); err != nil {
			t.Fatalf("save filler: %v", err)
		}
	}
	saved, err := d.mem.Save(ctx, memory.SaveInput{
		Title:   "prepared statements for every query",
		Content: "sql injection is prevented by parameter binding; never concatenate strings into queries",
		Type:    memory.TypeConvention, Project: "api",
	})
	if err != nil {
		t.Fatalf("save target: %v", err)
	}
	d.close()

	fixture := fmt.Sprintf("probes:\n  - id: %q\n    queries: [\"sql injection prevention\"]\n    expect_in_top: 5\n    project: \"api\"\n", saved.Observation.ID)
	path := filepath.Join(t.TempDir(), "retrieval.yaml")
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}

	var out string
	out = captureStdout(t, func() {
		if err := runVerifyCalibrate(ctx, []string{path}); err != nil {
			t.Fatalf("verify calibrate: %v", err)
		}
	})
	if !strings.Contains(out, "Gate calibration") || !strings.Contains(out, "floor:") {
		t.Errorf("calibration must emit its parameter report, got: %q", out)
	}
	if strings.Contains(out, "floor:         0.00") {
		t.Errorf("seeded on-topic probe must produce a non-zero floor, got: %q", out)
	}

	// precision@K: the probe target must surface inside expect_in_top,
	// which is what makes runVerifyRetrieval exit clean.
	if err := runVerifyRetrieval(ctx, []string{path}); err != nil {
		t.Errorf("retrieval probes on seeded store: %v", err)
	}
}

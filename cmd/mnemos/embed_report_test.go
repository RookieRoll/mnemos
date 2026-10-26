package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// embedTestObservation builds a live observation. Callers override the vector,
// model and validity fields they need.
func embedTestObservation(title, content, model string, vector []float32) memory.Observation {
	now := time.Now().UTC()
	return memory.Observation{
		ID:             title,
		AgentID:        "default",
		Title:          title,
		Content:        content,
		Type:           memory.TypeSemantic,
		Importance:     5,
		CreatedAt:      now,
		ValidFrom:      now,
		Embedding:      vector,
		EmbeddingModel: model,
	}
}

func TestEmbeddingCoverageReportsModelMix(t *testing.T) {
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	usable := embedTestObservation("usable", "text", "ollama/fixture", []float32{1, 0})
	usable2 := embedTestObservation("usable2", "text", "ollama/fixture", []float32{0, 1})
	missing := embedTestObservation("missing", "text", "", nil)
	other := embedTestObservation("other", "text", "old/model", []float32{1, 0, 0})
	expired := embedTestObservation("expired", "text", "", nil)
	expired.ExpiresAt = &past
	retired := embedTestObservation("retired", "text", "", nil)
	retired.ValidUntil = &past
	invalidated := embedTestObservation("invalidated", "text", "", nil)
	invalidated.InvalidatedAt = &past
	blank := embedTestObservation("blank", "", "", nil)
	blank.Title = "" // blank text is what makes the row ineligible

	observations := []memory.Observation{usable, usable2, missing, other, expired, retired, invalidated, blank}

	coverage := measureEmbeddingCoverage(observations, "ollama/fixture", 2, now)
	if coverage.Eligible != 4 {
		t.Errorf("eligible = %d, want 4 (usable, usable2, missing, other)", coverage.Eligible)
	}
	if coverage.Embedded != 2 {
		t.Errorf("embedded = %d, want 2", coverage.Embedded)
	}
	if len(coverage.Models) != 2 {
		t.Fatalf("model distribution = %+v, want two rows", coverage.Models)
	}
	if coverage.Models[0].Model != "old/model" || coverage.Models[0].Count != 1 || !coverage.Models[0].Mismatch {
		t.Errorf("old/model row = %+v, want one row flagged as a dimension mismatch", coverage.Models[0])
	}
	if coverage.Models[1].Model != "ollama/fixture" || coverage.Models[1].Count != 2 || coverage.Models[1].Mismatch {
		t.Errorf("active model row = %+v, want two rows without a mismatch", coverage.Models[1])
	}

	skipped, remaining := embeddingBackfillGap(observations, "ollama/fixture", 2, false, now)
	want := embeddingSkipCounts{Superseded: 3, IncompatibleModel: 1, EmptyContent: 1}
	if skipped != want {
		t.Errorf("skipped = %+v, want %+v", skipped, want)
	}
	if remaining != 1 {
		t.Errorf("remaining = %d, want 1 (missing; the incompatible row is skipped)", remaining)
	}
	// Rebuilding is asked to touch the incompatible row, so it stops being a
	// skip and joins the remainder.
	rebuildSkipped, rebuildRemaining := embeddingBackfillGap(observations, "ollama/fixture", 2, true, now)
	if rebuildSkipped.IncompatibleModel != 0 {
		t.Errorf("rebuild skip count = %d, want 0", rebuildSkipped.IncompatibleModel)
	}
	if rebuildRemaining != 2 {
		t.Errorf("rebuild remaining = %d, want 2 (missing, other)", rebuildRemaining)
	}
}

func TestEmbedStatusReportsCoverageAndModels(t *testing.T) {
	homeWithConfig(t, `
[embedding]
provider = "ollama"
model = "fixture"
dimension = 2
base_url = "http://127.0.0.1:1"
`)
	ctx := context.Background()
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	rows := []memory.Observation{
		embedTestObservation("usable", "text", "ollama/fixture", []float32{1, 0}),
		embedTestObservation("usable2", "text", "ollama/fixture", []float32{0, 1}),
		embedTestObservation("missing", "text", "", nil),
		embedTestObservation("other", "text", "old/model", []float32{1, 0, 0}),
		embedTestObservation("expired", "text", "", nil),
		embedTestObservation("blank", "", "", nil),
	}
	rows[4].ExpiresAt = &past
	rows[5].Title = "" // blank text is not embeddable
	for i := range rows {
		if err := d.db.Observations().Insert(ctx, &rows[i]); err != nil {
			t.Fatalf("insert %s: %v", rows[i].ID, err)
		}
	}
	d.close()

	out := captureStdout(t, func() {
		if err := runEmbedStatus(ctx); err != nil {
			t.Fatalf("embed status: %v", err)
		}
	})
	for _, want := range []string{"embedded 2/4", "ollama/fixture", "old/model", "dimension 3 != active 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("embed status missing %q: %s", want, out)
		}
	}
}

func TestEmbedBackfillReportsSkippedAndRemaining(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{1, 0}}})
	}))
	defer provider.Close()
	homeWithConfig(t, fmt.Sprintf(`
[embedding]
provider = "ollama"
model = "fixture"
dimension = 2
base_url = %q
`, provider.URL))

	ctx := context.Background()
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	rows := []memory.Observation{
		embedTestObservation("missing", "text", "", nil),
		embedTestObservation("other", "text", "old/model", []float32{0, 1}),
		embedTestObservation("gone", "text", "", nil),
		embedTestObservation("blank", "", "", nil),
	}
	rows[2].InvalidatedAt = &past
	rows[3].Title = "" // blank text is not embeddable
	for i := range rows {
		if err := d.db.Observations().Insert(ctx, &rows[i]); err != nil {
			t.Fatalf("insert %s: %v", rows[i].ID, err)
		}
	}
	d.close()

	run := func() string {
		return captureStdout(t, func() {
			if err := runEmbed(ctx, []string{"backfill"}); err != nil {
				t.Fatalf("embed backfill: %v", err)
			}
		})
	}
	first := run()
	for _, want := range []string{
		"attempted=1 succeeded=1 failed=0",
		"skipped=superseded:1 incompatible_model:1 empty_content:1",
		"remaining=0",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("first backfill missing %q: %s", want, first)
		}
	}
	second := run()
	for _, want := range []string{
		"attempted=0 succeeded=0 failed=0",
		"skipped=superseded:1 incompatible_model:1 empty_content:1",
		"remaining=0",
	} {
		if !strings.Contains(second, want) {
			t.Errorf("second backfill missing %q: %s", want, second)
		}
	}
}

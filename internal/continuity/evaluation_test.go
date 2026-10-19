package continuity_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/continuity"
	"github.com/polyxmedia/mnemos/internal/session"
)

// This is a deterministic information-availability fixture, not an agent speed
// benchmark. Fixed scripted reads expose the progress absent from an older summary.
func TestContinuitySummaryBaselineAndCheckpointReadFixture(t *testing.T) {
	h := newHarness(t)
	old := h.open("default", "old-completed-session")
	if err := h.db.Sessions().Close(t.Context(), session.CloseInput{ID: old.SessionID, Summary: "Prepared schema design; implementation begins next."}); err != nil {
		t.Fatal(err)
	}
	working := h.open("default", "working-session")
	write := checkpoint(working.SessionID, "progress", 0)
	write.Payload.Summary = "Schema implementation and targeted test are complete."
	write.Payload.Blockers = []string{"Previous-binary compatibility has not been verified."}
	h.save(write)
	receiver := h.open("default", "receiving-session")
	type metrics struct{ reads, repeated, absentBlockers, verification int }
	// A fixed deterministic reader uses evidence fixtures when progress does not
	// report the completed work. These counts describe this script only.
	evidence := map[string]string{"implementation": "Schema implementation is complete.", "targeted-test": "The targeted test is complete.", "current-workspace": "Fixture files still match; compatibility remains unverified."}
	fixtureRoot := t.TempDir()
	for name, text := range evidence {
		if err := os.WriteFile(filepath.Join(fixtureRoot, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	readFile := func(name string) string {
		raw, err := os.ReadFile(filepath.Join(fixtureRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	expectedBlockers := []string{"Previous-binary compatibility has not been verified."}
	run := func(sourceID string) metrics {
		m := metrics{}
		readSource := func() *continuity.Source { m.reads++; return h.read(receiver.SessionID, sourceID, "default").Source }
		readEvidence := func(name string) string { m.reads++; m.repeated++; return readFile(name) }
		source := readSource()
		if source == nil {
			t.Fatal("fixture source missing")
		}
		if source.Payload == nil {
			if !strings.Contains(readEvidence("implementation"), "complete") || !strings.Contains(readEvidence("targeted-test"), "complete") {
				t.Fatal("fixture evidence missing")
			}
		} else if len(source.Payload.Blockers) != 1 || !strings.Contains(source.Payload.Summary, "complete") {
			t.Fatal("checkpoint progress fixture changed")
		}
		sourceText := source.Summary
		if source.Payload != nil {
			sourceText += source.Payload.Summary + strings.Join(source.Payload.Blockers, "\n")
		}
		for _, blocker := range expectedBlockers {
			if !strings.Contains(sourceText, blocker) {
				m.absentBlockers++
			}
		}
		// Both arms independently check the same current-workspace fixture before
		// reaching the scripted next-action boundary. A checkpoint is not proof.
		readCurrent := func() string { m.reads++; m.verification++; return readFile("current-workspace") }
		if !strings.Contains(readCurrent(), "compatibility remains unverified") {
			t.Fatal("current-workspace verification changed")
		}
		return m
	}
	baseline, progress := run(old.SessionID), run(working.SessionID)
	if baseline.reads != 4 || progress.reads != 2 || baseline.repeated != 2 || progress.repeated != 0 || baseline.absentBlockers != 1 || progress.absentBlockers != 0 || baseline.verification != 1 || progress.verification != 1 {
		t.Fatalf("scripted availability changed: %+v %+v", baseline, progress)
	}
	t.Logf("scripted fixture n=1/arm: summary reads=%d repeated-inspections=%d blockers-absent-from-source=%d; checkpoint reads=%d repeated-inspections=%d blockers-absent-from-source=%d; both arms performed one current-workspace verification read", baseline.reads, baseline.repeated, baseline.absentBlockers, progress.reads, progress.repeated, progress.absentBlockers)
}

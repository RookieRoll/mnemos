package verify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplicitFixtureValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantErr    bool
	}{
		{"defaults", "cases:\n- name: useful\n  queries: [sql]\n  relevant_ids: [a, b]\n", false},
		{"overlap", "cases:\n- name: overlap\n  queries: [sql]\n  relevant_ids: [a]\n  forbidden_ids: [a]\n", true},
		{"mode", "cases:\n- name: mode\n  queries: [sql]\n  forbidden_ids: [a]\n  query_mode: sometimes\n", true},
		{"unlabelled", "cases:\n- name: empty\n  queries: [sql]\n", true},
		{"empty expectation", "cases:\n- name: empty\n  queries: [sql]\n  expect_no_injection: true\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "fixture.yaml")
			if err := os.WriteFile(p, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			f, err := LoadRetrievalFixture(p)
			if (err != nil) != tc.wantErr {
				t.Fatalf("fixture=%+v err=%v", f, err)
			}
			if err == nil && (f.Cases[0].ExpectInTop != 5 || f.Cases[0].QueryMode != "all") {
				t.Fatal("defaults missing")
			}
		})
	}
}

func TestExplicitQueryAggregationAndCalibration(t *testing.T) {
	s := fakeSearcher{byQuery: map[string][]string{"good": {"a", "a", "b", "unknown"}, "bad": {"a", "forbidden"}}}
	for _, mode := range []string{"any", "all"} {
		fix := &RetrievalFixture{Probes: []RetrievalProbe{{ID: "b", Queries: []string{"good", "bad"}, ExpectInTop: 5}}, Cases: []RetrievalCase{{Name: "labels", Queries: []string{"good", "bad"}, RelevantIDs: []string{"a", "b"}, ForbiddenIDs: []string{"forbidden"}, QueryMode: mode}}}
		rep, err := RunRetrieval(context.Background(), s, fix)
		if err != nil {
			t.Fatal(err)
		}
		if !rep.Probes[0].Pass || rep.Cases[0].Pass != (mode == "any") {
			t.Fatalf("report=%+v", rep)
		}
		if rep.Cases[0].Queries[0].Recall != 1 || len(rep.Cases[0].Queries[0].UnlabelledIDs) != 1 {
			t.Fatal("incorrect labels")
		}
		cal, err := CalibrateLabelledGate(context.Background(), s, fix)
		if err != nil {
			t.Fatal(err)
		}
		if cal.Coverage.Positive != 3 || cal.Coverage.Negative != 1 || cal.Coverage.Unlabelled != 1 || !cal.Coverage.SeparationMeasured {
			t.Fatalf("coverage=%+v", cal.Coverage)
		}
	}
	for _, ids := range [][]string{nil, {"unknown"}, {"a"}} {
		fix := &RetrievalFixture{Cases: []RetrievalCase{{Name: "coverage", Queries: []string{"q"}, RelevantIDs: []string{"a"}}}}
		rep, err := CalibrateLabelledGate(context.Background(), fakeSearcher{byQuery: map[string][]string{"q": ids}}, fix)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Coverage.SeparationMeasured {
			t.Fatal("single class presented as separation")
		}
	}
}

func TestREADMEFixtureExamplesLoad(t *testing.T) {
	data, err := os.ReadFile("../../verify/README.md")
	if err != nil {
		t.Fatal(err)
	}
	blocks := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "```yaml\n")
	if len(blocks) < 2 {
		t.Fatal("no README YAML examples found")
	}
	t.Logf("validated %d README YAML examples", len(blocks)-1)
	for i, b := range blocks[1:] {
		text := strings.SplitN(b, "```", 2)[0]
		if strings.HasPrefix(text, "- id:") {
			text = "probes:\n" + text
		}
		p := filepath.Join(t.TempDir(), "example.yaml")
		if err := os.WriteFile(p, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadRetrievalFixture(p); err != nil {
			t.Fatalf("example %d: %v", i, err)
		}
	}
}

package workspacediagnostics

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type fixedReader struct {
	snapshot Snapshot
	err      error
}

func (r fixedReader) Snapshot(context.Context, int) (Snapshot, error) { return r.snapshot, r.err }

type fixedProbe map[string]PathState

func (p fixedProbe) Inspect(_ context.Context, root string) PathState { return p[root] }

type timeoutProbe struct{}

func (timeoutProbe) Inspect(ctx context.Context, _ string) PathState {
	<-ctx.Done()
	return PathState{Status: "unknown", Git: "unknown", Reason: "probe_budget_exhausted"}
}

func TestDiagnoseClassifiesEvidenceWithoutCertifyingMerge(t *testing.T) {
	cases := []struct {
		name  string
		want  string
		setup func(string) (Metadata, Metadata, fixedProbe)
	}{
		{
			name: "physical aliases",
			want: "verified_path_equivalence",
			setup: func(base string) (Metadata, Metadata, fixedProbe) {
				a, b := filepath.Join(base, "repo"), filepath.Join(base, "alias")
				return Metadata{Identity: Identity{Root: a, Key: "k1"}}, Metadata{Identity: Identity{Root: b, Key: "k2"}}, fixedProbe{a: {Status: "live", Git: "git", Canonical: a}, b: {Status: "live", Git: "git", Canonical: a}}
			},
		},
		{
			name: "non git parent and child",
			want: "possible_boundary_split",
			setup: func(base string) (Metadata, Metadata, fixedProbe) {
				a, b := filepath.Join(base, "work"), filepath.Join(base, "work", "app")
				return Metadata{Identity: Identity{Root: a, Key: "k1"}}, Metadata{Identity: Identity{Root: b, Key: "k2"}}, fixedProbe{a: {Status: "live", Git: "non_git", Canonical: a}, b: {Status: "live", Git: "non_git", Canonical: b}}
			},
		},
		{
			name: "missing old path with similar live path",
			want: "possible_migration",
			setup: func(base string) (Metadata, Metadata, fixedProbe) {
				a, b := filepath.Join(base, "old", "agent"), filepath.Join(base, "new", "agent")
				return Metadata{Identity: Identity{Root: a, Key: "k1"}}, Metadata{Identity: Identity{Root: b, Key: "k2"}}, fixedProbe{a: {Status: "missing", Git: "unknown"}, b: {Status: "live", Git: "non_git", Canonical: b}}
			},
		},
		{
			name: "independent git roots stay separate",
			want: "independent_git_roots",
			setup: func(base string) (Metadata, Metadata, fixedProbe) {
				a, b := filepath.Join(base, "work", "project"), filepath.Join(base, "work", "project-copy")
				return Metadata{Identity: Identity{Root: a, Key: "k1"}}, Metadata{Identity: Identity{Root: b, Key: "k2"}}, fixedProbe{a: {Status: "live", Git: "git", GitRoot: a}, b: {Status: "live", Git: "git", GitRoot: b}}
			},
		},
		{
			name: "weak similarity",
			want: "weak_similarity",
			setup: func(base string) (Metadata, Metadata, fixedProbe) {
				a, b := filepath.Join(base, "work", "alpha"), filepath.Join(base, "other", "alpha-copy")
				return Metadata{Identity: Identity{Root: a, Key: "k1"}}, Metadata{Identity: Identity{Root: b, Key: "k2"}}, fixedProbe{a: {Status: "live", Git: "non_git"}, b: {Status: "live", Git: "non_git"}}
			},
		},
		{
			name: "inaccessible evidence",
			want: "unknown_evidence",
			setup: func(base string) (Metadata, Metadata, fixedProbe) {
				a, b := filepath.Join(base, "work", "alpha"), filepath.Join(base, "other", "alpha-copy")
				return Metadata{Identity: Identity{Root: a, Key: "k1"}}, Metadata{Identity: Identity{Root: b, Key: "k2"}}, fixedProbe{a: {Status: "unknown", Git: "unknown"}, b: {Status: "live", Git: "non_git"}}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			left, right, paths := tc.setup(t.TempDir())
			service, err := NewService(Config{Reader: fixedReader{snapshot: Snapshot{Exists: true, TotalRoots: 2, Roots: []Metadata{left, right}}}, Probe: paths, MaxRoots: 2, MaxCandidates: 10, Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			report, err := service.Diagnose(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Candidates) != 1 || report.Candidates[0].Classification != tc.want {
				t.Fatalf("candidate classification = %+v, want %q", report.Candidates, tc.want)
			}
			if report.Candidates[0].MergeSafety != "not_established" {
				t.Fatalf("merge safety = %q", report.Candidates[0].MergeSafety)
			}
		})
	}
}

func TestDiagnoseBudgetsRetryOverlapAndSortsDeterministically(t *testing.T) {
	base := t.TempDir()
	a := Metadata{Identity: Identity{Root: filepath.Join(base, "work", "application"), Key: "a"}}
	b := Metadata{Identity: Identity{Root: filepath.Join(base, "work", "application", "child", "grandchild"), Key: "b"}, Counts: Counts{OwnerCheckpoints: 1}}
	c := Metadata{Identity: Identity{Root: filepath.Join(base, "work", "application", "child"), Key: "c"}}
	paths := fixedProbe{
		a.Root: {Status: "live", Git: "non_git"},
		b.Root: {Status: "live", Git: "non_git"},
		c.Root: {Status: "live", Git: "non_git"},
	}
	snapshot := Snapshot{Exists: true, TotalRoots: 3, Roots: []Metadata{c, b, a}, RetryOverlap: []RetryConflict{{Left: c.Identity, Right: a.Identity, Count: 2}}, RetryOverlapTruncated: true}
	service, err := NewService(Config{Reader: fixedReader{snapshot: snapshot}, Probe: paths, MaxRoots: 3, MaxCandidates: 2, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Diagnose(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !report.Partial || !contains(report.Reasons, "retry_overlap_budget") || !contains(report.Reasons, "max_candidates") {
		t.Fatalf("expected explicit budget truncation: %+v", report)
	}
	if report.ComparedPairs != 3 || len(report.Candidates) != 2 {
		t.Fatalf("comparison budget exceeded: %+v", report)
	}
	if !identityLess(report.Roots[0].Identity, report.Roots[1].Identity) || !identityLess(report.Roots[1].Identity, report.Roots[2].Identity) {
		t.Fatalf("root order is not deterministic: %+v", report.Roots)
	}
	for _, candidate := range report.Candidates {
		if candidate.Left.Root == a.Root && candidate.Right.Root == c.Root && candidate.RetryOverlap != 2 {
			t.Fatalf("retry overlap lost for pair: %+v", candidate)
		}
		if candidate.Left.Root == a.Root && candidate.Right.Root == b.Root && !contains(candidate.Blockers, "checkpoint_visibility_review_required") {
			t.Fatalf("visibility risk missing: %+v", candidate)
		}
	}
}

func TestDiagnoseFindsLateCandidateAfterMoreThanCandidateBudgetPairs(t *testing.T) {
	base := t.TempDir()
	roots := make([]Metadata, 0, 23)
	paths := fixedProbe{}
	for i := 0; i < 21; i++ {
		root := filepath.Join(base, "project", fmt.Sprintf("filler-%02d", i))
		roots = append(roots, Metadata{Identity: Identity{Root: root, Key: fmt.Sprintf("k-%02d", i)}})
		paths[root] = PathState{Status: "live", Git: "non_git"}
	}
	left := filepath.Join(base, "project", "zz-alias")
	right := filepath.Join(base, "project", "zz-alias-copy")
	roots = append(roots, Metadata{Identity: Identity{Root: left, Key: "k-left"}}, Metadata{Identity: Identity{Root: right, Key: "k-right"}})
	paths[left] = PathState{Status: "live", Git: "non_git"}
	paths[right] = PathState{Status: "live", Git: "non_git"}
	service, err := NewService(Config{Reader: fixedReader{snapshot: Snapshot{Exists: true, TotalRoots: len(roots), Roots: roots}}, Probe: paths, MaxRoots: len(roots), MaxCandidates: 1, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Diagnose(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.ComparedPairs != 253 || report.Partial || len(report.Candidates) != 1 || report.Candidates[0].Left.Root != left || report.Candidates[0].Right.Root != right {
		t.Fatalf("late candidate missed or scan was incorrectly capped: compared=%d partial=%v candidates=%+v", report.ComparedPairs, report.Partial, report.Candidates)
	}
}

func TestDiagnoseSingleProbeTimeoutIsPartial(t *testing.T) {
	service, err := NewService(Config{Reader: fixedReader{snapshot: Snapshot{Exists: true, TotalRoots: 1, Roots: []Metadata{{Identity: Identity{Root: `/slow`, Key: "key"}}}}}, Probe: timeoutProbe{}, MaxRoots: 1, MaxCandidates: 1, Timeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Diagnose(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !report.Partial || report.Status != "partial" || !contains(report.Reasons, "timeout_during_path_probes") {
		t.Fatalf("single-root timeout should be explicit: %+v", report)
	}
}

func TestDiagnoseReturnsReaderErrorsAndCallerCancellation(t *testing.T) {
	marker := errors.New("read failed")
	service, _ := NewService(Config{Reader: fixedReader{err: marker}, Probe: fixedProbe{}, MaxRoots: 1, MaxCandidates: 1, Timeout: time.Second})
	if _, err := service.Diagnose(context.Background()); !errors.Is(err, marker) {
		t.Fatalf("reader error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Diagnose(ctx); err == nil {
		t.Fatal("expected canceled context error")
	}
	probeService, err := NewService(Config{Reader: fixedReader{snapshot: Snapshot{Exists: true, TotalRoots: 1, Roots: []Metadata{{Identity: Identity{Root: `/slow`, Key: "key"}}}}}, Probe: timeoutProbe{}, MaxRoots: 1, MaxCandidates: 1, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	probeCtx, stop := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		stop()
	}()
	if _, err := probeService.Diagnose(probeCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation during a probe should remain an error, got %v", err)
	}
}

func TestPairKeyIsOrderIndependent(t *testing.T) {
	a, b := Identity{Root: `/a`, Key: "1"}, Identity{Root: `/b`, Key: "2"}
	if !reflect.DeepEqual(newPairKey(a, b), newPairKey(b, a)) {
		t.Fatal("pair key must not depend on input order")
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

package rumination

import (
	"context"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// fakeRawReader is the minimal RawObservationReader satisfying the
// StaleRawObservationMonitor's narrow interface. Declared at the test
// site (consumer-owned pattern) so we do not depend on the full
// memory.Reader just to exercise one method.
type fakeRawReader struct {
	items []memory.Observation
	err   error

	// calls counts ListByTrustTier invocations so a test can prove the monitor
	// does not even scan raw observations under raw_promote = never.
	calls int
}

func (f *fakeRawReader) ListByTrustTier(ctx context.Context, agentID string, tier memory.TrustTier, limit int) ([]memory.Observation, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := make([]memory.Observation, 0, len(f.items))
	for _, o := range f.items {
		if tier != "" && o.TrustTier != tier {
			continue
		}
		out = append(out, o)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func TestStaleRawMonitorFiresOnOldRaw(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	reader := &fakeRawReader{items: []memory.Observation{
		// 30 days old = 2.1× the default 14-day threshold → medium.
		{ID: "old-raw", TrustTier: memory.TrustRaw, SourceKind: memory.SourceTool,
			CreatedAt: now.AddDate(0, 0, -30)},
		// Below cutoff — should not fire.
		{ID: "fresh", TrustTier: memory.TrustRaw, SourceKind: memory.SourceTool,
			CreatedAt: now.AddDate(0, 0, -3)},
		// 45 days old → 3.2× threshold → high.
		{ID: "ancient", TrustTier: memory.TrustRaw, SourceKind: memory.SourceAgentInference,
			CreatedAt: now.AddDate(0, 0, -45)},
	}}
	m := &StaleRawObservationMonitor{
		Observations: reader,
		StaleDays:    14,
		now:          func() time.Time { return now },
	}

	got, err := m.Detect(context.Background())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 candidates (old-raw + ancient), got %d: %v", len(got), got)
	}

	sev := map[string]Severity{}
	for _, c := range got {
		sev[c.TargetID] = c.Severity
	}
	if sev["old-raw"] != SeverityMedium {
		t.Errorf("30-day raw should be medium, got %v", sev["old-raw"])
	}
	if sev["ancient"] != SeverityHigh {
		t.Errorf("45-day raw should be high, got %v", sev["ancient"])
	}
}

func TestStaleRawMonitorSkipsCurated(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	reader := &fakeRawReader{items: []memory.Observation{
		{ID: "curated", TrustTier: memory.TrustCurated, CreatedAt: now.AddDate(0, -6, 0)},
		{ID: "raw", TrustTier: memory.TrustRaw, CreatedAt: now.AddDate(0, -6, 0)},
	}}
	m := &StaleRawObservationMonitor{
		Observations: reader,
		now:          func() time.Time { return now },
	}

	got, err := m.Detect(context.Background())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(got) != 1 || got[0].TargetID != "raw" {
		t.Errorf("monitor should fire only on raw-tier rows, got %+v", got)
	}
}

func TestStaleRawSeverityRatios(t *testing.T) {
	cases := []struct {
		age, cutoff int
		want        Severity
	}{
		{15, 14, SeverityLow},
		{28, 14, SeverityMedium},
		{42, 14, SeverityHigh},
		{100, 14, SeverityHigh},
		{5, 0, SeverityLow},
	}
	for _, c := range cases {
		if got := staleRawSeverity(c.age, c.cutoff); got != c.want {
			t.Errorf("staleRawSeverity(%d, %d) = %v, want %v", c.age, c.cutoff, got, c.want)
		}
	}
}

func TestStaleRawMonitorName(t *testing.T) {
	m := &StaleRawObservationMonitor{}
	if m.Name() != "stale-raw" {
		t.Errorf("monitor name drift: got %q", m.Name())
	}
}

// fixedMonitor is a Monitor that always reports the same candidates. Used to
// prove one monitor's policy does not silence its peers.
type fixedMonitor struct {
	name       string
	candidates []Candidate
}

func (m fixedMonitor) Name() string { return m.name }

func (m fixedMonitor) Detect(context.Context) ([]Candidate, error) { return m.candidates, nil }

func TestStaleRawMonitorRawPromoteNever_EmitsNoCandidates(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	reader := &fakeRawReader{items: []memory.Observation{
		{ID: "old-raw", TrustTier: memory.TrustRaw, SourceKind: memory.SourceTool,
			CreatedAt: now.AddDate(0, 0, -30)},
		{ID: "ancient", TrustTier: memory.TrustRaw, SourceKind: memory.SourceAgentInference,
			CreatedAt: now.AddDate(0, 0, -45)},
	}}
	m := &StaleRawObservationMonitor{
		Observations: reader,
		RawPromote:   RawPromoteNever,
		now:          func() time.Time { return now },
	}

	got, err := m.Detect(context.Background())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("raw_promote=never must produce zero candidates, got %d: %+v", len(got), got)
	}
	if reader.calls != 0 {
		t.Fatalf("raw_promote=never must not scan raw observations, reader called %d time(s)", reader.calls)
	}
}

func TestStaleRawMonitorRawPromoteAuto_PreservesBehaviour(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	newReader := func() *fakeRawReader {
		return &fakeRawReader{items: []memory.Observation{
			{ID: "old-raw", TrustTier: memory.TrustRaw, SourceKind: memory.SourceTool,
				CreatedAt: now.AddDate(0, 0, -30)},
			{ID: "fresh", TrustTier: memory.TrustRaw, SourceKind: memory.SourceTool,
				CreatedAt: now.AddDate(0, 0, -3)},
			{ID: "ancient", TrustTier: memory.TrustRaw, SourceKind: memory.SourceAgentInference,
				CreatedAt: now.AddDate(0, 0, -45)},
		}}
	}

	// Reference run: the monitor exactly as it was before the switch, with the
	// policy field left unset.
	reference, err := (&StaleRawObservationMonitor{Observations: newReader(), now: clock}).Detect(context.Background())
	if err != nil {
		t.Fatalf("reference detect: %v", err)
	}
	if len(reference) != 2 {
		t.Fatalf("reference detection should fire on the two stale rows, got %d: %+v", len(reference), reference)
	}

	tests := []struct {
		name   string
		policy RawPromotePolicy
	}{
		{name: "unset field keeps historical behaviour", policy: ""},
		{name: "explicit auto", policy: RawPromoteAuto},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (&StaleRawObservationMonitor{
				Observations: newReader(),
				RawPromote:   tt.policy,
				now:          clock,
			}).Detect(context.Background())
			if err != nil {
				t.Fatalf("detect: %v", err)
			}
			if len(got) != len(reference) {
				t.Fatalf("candidates = %d, want %d (historical behaviour)", len(got), len(reference))
			}
			for i := range reference {
				if got[i].ID != reference[i].ID || got[i].Severity != reference[i].Severity || got[i].Reason != reference[i].Reason {
					t.Fatalf("candidate %d drifted: got %+v, want %+v", i, got[i], reference[i])
				}
			}
		})
	}
}

func TestStaleRawMonitorRawPromoteNever_LeavesOtherMonitorsUnaffected(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	reader := &fakeRawReader{items: []memory.Observation{
		{ID: "old-raw", TrustTier: memory.TrustRaw, CreatedAt: now.AddDate(0, 0, -30)},
	}}
	peer := fixedMonitor{name: "peer", candidates: []Candidate{{
		ID: "peer-1", MonitorName: "peer", Severity: SeverityHigh,
		TargetKind: TargetObservation, TargetID: "obs-1", DetectedAt: now,
	}}}
	svc := NewService(Config{Monitors: []Monitor{
		&StaleRawObservationMonitor{
			Observations: reader,
			RawPromote:   RawPromoteNever,
			now:          func() time.Time { return now },
		},
		peer,
	}})

	got, err := svc.Detect(context.Background())
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(got) != 1 || got[0].ID != "peer-1" {
		t.Fatalf("silencing stale-raw must not silence peers, got %+v", got)
	}
}

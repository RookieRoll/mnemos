package verify_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
	"github.com/polyxmedia/mnemos/internal/verify"
)

// seedCalibrationStore builds the seeded corpus the calibration sweeps
// over: a clear on-topic pair, a weak lexical neighbour, and unrelated
// noise, spread across two projects so the penalty sweep has cross-project
// material to separate.
func seedCalibrationStore(t *testing.T) (*memory.Service, memory.Store) {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "cal.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := db.Observations()
	svc := memory.NewService(memory.Config{Store: store})
	ctx := context.Background()
	now := time.Now().UTC()

	rows := []*memory.Observation{
		{
			ID: "mem-sql", Project: "api", Title: "Prepared statements for every query",
			Content:  "Every SQL query goes through prepared statements; string concatenation into SQL is banned after the injection incident. Parameter binding only.",
			Type:     memory.TypeConvention, Importance: 9, CreatedAt: now, ValidFrom: now,
		},
		{
			ID: "mem-tx", Project: "api", Title: "Transactions wrap multi-write operations",
			Content:  "Multi-write flows open a transaction and roll back on error; no partial writes.",
			Type:     memory.TypeConvention, Importance: 6, CreatedAt: now, ValidFrom: now,
		},
		{
			ID: "mem-notes", Project: "api", Title: "Meeting notes",
			Content:  "Discussed the SQL schema and the roadmap afterwards; no decisions recorded.",
			Type:     memory.TypeEpisodic, Importance: 2, CreatedAt: now, ValidFrom: now,
		},
		{
			ID: "mem-escape", Project: "web", Title: "Escape user input before HTML rendering",
			Content:  "Every value interpolated into HTML gets escaped; raw template insertion is banned to prevent SQL problems too.",
			Type:     memory.TypeConvention, Importance: 7, CreatedAt: now, ValidFrom: now,
		},
		{
			ID: "mem-style", Project: "api", Title: "Formatting",
			Content:  "gofmt decides; do not argue about spacing.",
			Type:     memory.TypeConvention, Importance: 3, CreatedAt: now, ValidFrom: now,
		},
	}
	for _, o := range rows {
		if err := store.Insert(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	return svc, store
}

func TestCalibrateGateEmitsReportFromSeededStore(t *testing.T) {
	svc, _ := seedCalibrationStore(t)
	fix := &verify.RetrievalFixture{Probes: []verify.RetrievalProbe{
		{ID: "mem-sql", Queries: []string{"sql injection prevention", "safe query writing"}, Project: "api", ExpectInTop: 5},
		{ID: "mem-tx", Queries: []string{"transaction boundaries"}, Project: "api", ExpectInTop: 5},
	}}

	rep, samples, err := verify.CalibrateGate(context.Background(), svc, fix, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("gate calibration report: floor=%.4f penalty=%.2f balanced_acc=%.3f samples=%d on_topic=%d used=%d",
		rep.Floor, rep.Penalty, rep.BalancedAcc, rep.Samples, rep.OnTopic, rep.Used)

	if rep.Samples == 0 {
		t.Fatal("report must carry samples from the seeded store")
	}
	if rep.OnTopic == 0 {
		t.Fatal("probe targets must be labeled on-topic")
	}
	if rep.Floor <= 0 || rep.Floor > memory.ScoreCeiling {
		t.Errorf("floor %v must land on the contract scale (0, %v]", rep.Floor, memory.ScoreCeiling)
	}
	if rep.BalancedAcc < 0.6 {
		t.Errorf("seeded classes should be mostly separable, got acc %v", rep.BalancedAcc)
	}
	if len(samples) != rep.Samples {
		t.Errorf("returned samples %d must match report %d", len(samples), rep.Samples)
	}
}

func TestCalibrateGateMarksUsedFromSurfacedCount(t *testing.T) {
	svc, store := seedCalibrationStore(t)
	ctx := context.Background()
	// mem-sql was surfaced twice but accessed three times: at least one
	// deliberate fetch, so it counts as used.
	for i := 0; i < 3; i++ {
		if err := store.BumpAccess(ctx, "mem-sql"); err != nil {
			t.Fatal(err)
		}
	}
	fix := &verify.RetrievalFixture{Probes: []verify.RetrievalProbe{
		{ID: "mem-sql", Queries: []string{"sql injection prevention"}, Project: "api", ExpectInTop: 5},
	}}

	rep, samples, err := verify.CalibrateGate(ctx, svc, fix, func(context.Context, string) int { return 2 })
	if err != nil {
		t.Fatal(err)
	}
	if rep.Used == 0 {
		t.Errorf("accessed-beyond-surfaced memory must count as used; samples=%+v", samples)
	}
}

func TestCalibrateGateNilFixture(t *testing.T) {
	if _, _, err := verify.CalibrateGate(context.Background(), nil, nil, nil); err == nil {
		t.Error("nil fixture must error")
	}
}

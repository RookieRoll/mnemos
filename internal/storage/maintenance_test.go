package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/maintenance"
	"github.com/polyxmedia/mnemos/internal/memory"
)

func TestMaintenanceClaimCycleCompletionAndStaleFence(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "maintenance.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := db.Maintenance()

	first, err := store.TryClaim(ctx, maintenance.ClaimRequest{
		OwnerToken: "owner-one", Mode: maintenance.ClaimIfDue,
		Interval: time.Hour, LeaseDuration: 2 * time.Second,
	})
	if err != nil || first.Status != maintenance.ClaimAcquired || first.Fence.CycleID == "" {
		t.Fatalf("first claim = %+v, %v", first, err)
	}
	busy, err := store.TryClaim(ctx, maintenance.ClaimRequest{
		OwnerToken: "owner-busy", Mode: maintenance.ClaimManual,
		LeaseDuration: time.Second,
	})
	if err != nil || busy.Status != maintenance.ClaimBusy {
		t.Fatalf("active claim = %+v, %v", busy, err)
	}

	// Expire the row with SQLite's own clock, modeling a crashed owner without
	// adding a timer race to this stale-fence assertion.
	if _, err := db.SQL().ExecContext(ctx, `UPDATE maintenance_coordinator
		SET lease_until = 0 WHERE singleton = 1`); err != nil {
		t.Fatal(err)
	}
	second, err := store.TryClaim(ctx, maintenance.ClaimRequest{
		OwnerToken: "owner-two", Mode: maintenance.ClaimManual,
		LeaseDuration: time.Second,
	})
	if err != nil || second.Status != maintenance.ClaimAcquired || second.Fence.CycleID != first.Fence.CycleID {
		t.Fatalf("takeover = %+v, %v; original cycle %q", second, err, first.Fence.CycleID)
	}
	if _, err := store.Renew(ctx, first.Fence, time.Second); !errors.Is(err, maintenance.ErrFenceLost) {
		t.Fatalf("stale renew error = %v", err)
	}
	if err := store.Complete(ctx, first.Fence); !errors.Is(err, maintenance.ErrFenceLost) {
		t.Fatalf("stale completion error = %v", err)
	}
	if err := store.Complete(ctx, second.Fence); err != nil {
		t.Fatalf("complete current owner: %v", err)
	}
	notDue, err := store.TryClaim(ctx, maintenance.ClaimRequest{
		OwnerToken: "owner-three", Mode: maintenance.ClaimIfDue,
		Interval: time.Hour, LeaseDuration: time.Second,
	})
	if err != nil || notDue.Status != maintenance.ClaimNotDue {
		t.Fatalf("claim before interval = %+v, %v", notDue, err)
	}
	manual, err := store.TryClaim(ctx, maintenance.ClaimRequest{
		OwnerToken: "owner-four", Mode: maintenance.ClaimManual,
		LeaseDuration: time.Second,
	})
	if err != nil || manual.Status != maintenance.ClaimAcquired || manual.Fence.CycleID == second.Fence.CycleID {
		t.Fatalf("manual new cycle = %+v, %v", manual, err)
	}
}

func TestMaintenanceFileClaimSkipsWriterContentionQuickly(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "maintenance-lock.db")
	writer, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	claimer, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer claimer.Close()

	lock, err := writer.sql.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	result, err := claimer.Maintenance().TryClaim(ctx, maintenance.ClaimRequest{
		OwnerToken: "contended", Mode: maintenance.ClaimManual,
		LeaseDuration: time.Second,
	})
	elapsed := time.Since(started)
	_ = lock.Rollback()
	if err != nil || result.Status != maintenance.ClaimBusy {
		t.Fatalf("contended claim = %+v, %v", result, err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("claim took %s under a writer lock; expected short skip", elapsed)
	}
	claimed, err := claimer.Maintenance().TryClaim(ctx, maintenance.ClaimRequest{
		OwnerToken: "after-release", Mode: maintenance.ClaimManual,
		LeaseDuration: time.Second,
	})
	if err != nil || claimed.Status != maintenance.ClaimAcquired {
		t.Fatalf("claim after lock release = %+v, %v", claimed, err)
	}
}

func TestMaintenanceMemoryFallbackUsesPrimaryDatabaseAndHonorsContext(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.maintenance != db.sql {
		t.Fatal("in-memory maintenance store must reuse the primary pool")
	}

	lock, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
	defer cancel()
	result, err := db.Maintenance().TryClaim(short, maintenance.ClaimRequest{
		OwnerToken: "memory-wait", Mode: maintenance.ClaimManual,
		LeaseDuration: time.Second,
	})
	_ = lock.Rollback()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("claim blocked on primary memory connection: result=%+v err=%v", result, err)
	}

	claim, err := db.Maintenance().TryClaim(ctx, maintenance.ClaimRequest{
		OwnerToken: "same-database", Mode: maintenance.ClaimManual,
		LeaseDuration: time.Second,
	})
	if err != nil || claim.Status != maintenance.ClaimAcquired {
		t.Fatalf("claim from reused in-memory pool = %+v, %v", claim, err)
	}
	var rows int
	if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM maintenance_coordinator`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("coordinator table rows=%d err=%v", rows, err)
	}
}

func TestMaintenanceCompetingConnectionsOneClaimsAndOneSkips(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "maintenance-race.db")
	left, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()
	right, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer right.Close()

	start := make(chan struct{})
	type outcome struct {
		result maintenance.ClaimResult
		err    error
	}
	results := make(chan outcome, 2)
	for i, db := range []*DB{left, right} {
		go func(i int, db *DB) {
			<-start
			result, err := db.Maintenance().TryClaim(ctx, maintenance.ClaimRequest{
				OwnerToken: fmt.Sprintf("racer-%d", i), Mode: maintenance.ClaimManual,
				LeaseDuration: 5 * time.Second,
			})
			results <- outcome{result: result, err: err}
		}(i, db)
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("claim outcomes: first=%+v second=%+v", first, second)
	}
	statuses := map[maintenance.ClaimStatus]int{first.result.Status: 1}
	statuses[second.result.Status]++
	if statuses[maintenance.ClaimAcquired] != 1 || statuses[maintenance.ClaimBusy] != 1 {
		t.Fatalf("competing claims should be acquired/busy, got %+v %+v", first.result, second.result)
	}
}

func TestMaintenanceResumeKeepsCycleAndDoesNotRepeatDecay(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "maintenance-resume.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mem := memory.NewService(memory.Config{Store: db.Observations()})
	saved, err := mem.Save(ctx, memory.SaveInput{Title: "stale", Content: "stale", Type: memory.TypeContext, Importance: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.ExecContext(ctx, `UPDATE observations SET created_at = datetime('now', '-30 days') WHERE id = ?`, saved.Observation.ID); err != nil {
		t.Fatal(err)
	}

	store := db.Maintenance()
	completed, err := store.TryClaim(ctx, maintenance.ClaimRequest{OwnerToken: "completed", Mode: maintenance.ClaimManual, LeaseDuration: time.Minute})
	if err != nil || completed.Status != maintenance.ClaimAcquired {
		t.Fatalf("initial claim = %+v, %v", completed, err)
	}
	if err := store.Complete(ctx, completed.Fence); err != nil {
		t.Fatal(err)
	}

	partial, err := store.TryClaim(ctx, maintenance.ClaimRequest{OwnerToken: "partial", Mode: maintenance.ClaimManual, LeaseDuration: time.Minute})
	if err != nil || partial.Status != maintenance.ClaimAcquired || partial.Fence.CycleID == completed.Fence.CycleID {
		t.Fatalf("manual cycle = %+v, %v", partial, err)
	}
	firstDecay, err := db.FencedObservations().DecayImportanceFenced(ctx, partial.Fence, 1, 1)
	if err != nil || firstDecay != 1 {
		t.Fatalf("first decay = %d, %v", firstDecay, err)
	}
	if _, err := db.sql.ExecContext(ctx, `UPDATE maintenance_coordinator SET lease_until = 0 WHERE singleton = 1`); err != nil {
		t.Fatal(err)
	}

	resumed, err := store.TryClaim(ctx, maintenance.ClaimRequest{
		OwnerToken: "resumed", Mode: maintenance.ClaimIfDue, Interval: time.Hour, LeaseDuration: time.Minute,
	})
	if err != nil || resumed.Status != maintenance.ClaimAcquired || resumed.Fence.CycleID != partial.Fence.CycleID || !resumed.DecayApplied {
		t.Fatalf("resume unfinished cycle = %+v, %v", resumed, err)
	}
	secondDecay, err := db.FencedObservations().DecayImportanceFenced(ctx, resumed.Fence, 1, 1)
	if err != nil || secondDecay != 0 {
		t.Fatalf("replayed decay = %d, %v", secondDecay, err)
	}
	observation, err := db.Observations().Get(ctx, saved.Observation.ID)
	if err != nil || observation.Importance != 4 {
		t.Fatalf("importance after recovery = %v, %v", observation, err)
	}
	if err := store.Complete(ctx, resumed.Fence); err != nil {
		t.Fatal(err)
	}
}

func TestDreamJournalUsesCycleIdentityAndDoesNotBumpAccess(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "maintenance-journal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	claim, err := db.Maintenance().TryClaim(ctx, maintenance.ClaimRequest{
		OwnerToken: "journal-owner", Mode: maintenance.ClaimManual, LeaseDuration: time.Minute,
	})
	if err != nil || claim.Status != maintenance.ClaimAcquired {
		t.Fatalf("claim = %+v, %v", claim, err)
	}
	mem := memory.NewService(memory.Config{Store: db.Observations()})
	first, err := mem.SaveDreamJournalFenced(ctx, claim.Fence, memory.SaveInput{
		Title: "first summary", Content: "original cycle summary", Type: memory.TypeDream,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := mem.SaveDreamJournalFenced(ctx, claim.Fence, memory.SaveInput{
		Title: "retry summary", Content: "replacement must not overwrite", Type: memory.TypeDream,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Deduped || second.Observation.ID != first.Observation.ID || second.Observation.Content != "original cycle summary" {
		t.Fatalf("same cycle should return original journal: first=%+v second=%+v", first, second)
	}
	var count int
	if err := db.sql.QueryRowContext(ctx, `SELECT access_count FROM observations WHERE id = ?`, claim.Fence.CycleID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("journal retry changed access_count: %d", count)
	}
}

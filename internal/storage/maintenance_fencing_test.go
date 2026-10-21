package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/maintenance"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/rumination"
	"github.com/polyxmedia/mnemos/internal/skills"
)

func TestFencedDreamWritesRejectInvalidOwnerAndExpiredLease(t *testing.T) {
	for _, state := range []string{"invalid owner", "expired lease", "restart stale owner"} {
		for _, write := range []string{"prune", "promotion", "candidate upsert", "auto-resolve", "journal"} {
			t.Run(state+"/"+write, func(t *testing.T) {
				ctx := context.Background()
				path := filepath.Join(t.TempDir(), "fencing.db")
				db, err := Open(ctx, path)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = db.Close() }()
				claim, err := db.Maintenance().TryClaim(ctx, maintenance.ClaimRequest{
					OwnerToken: "valid-owner", Mode: maintenance.ClaimManual, LeaseDuration: time.Minute,
				})
				if err != nil || claim.Status != maintenance.ClaimAcquired {
					t.Fatalf("claim = %+v, %v", claim, err)
				}
				fence := claim.Fence
				if state == "invalid owner" {
					fence.OwnerToken = "stale-owner"
				} else {
					if _, err := db.sql.ExecContext(ctx, `UPDATE maintenance_coordinator SET lease_until = 0 WHERE singleton = 1`); err != nil {
						t.Fatal(err)
					}
					if state == "restart stale owner" {
						if err := db.Close(); err != nil {
							t.Fatal(err)
						}
						db, err = Open(ctx, path)
						if err != nil {
							t.Fatal(err)
						}
						takeover, err := db.Maintenance().TryClaim(ctx, maintenance.ClaimRequest{
							OwnerToken: "new-owner", Mode: maintenance.ClaimManual, LeaseDuration: time.Minute,
						})
						if err != nil || takeover.Status != maintenance.ClaimAcquired || takeover.Fence.CycleID != claim.Fence.CycleID {
							t.Fatalf("restart takeover = %+v err=%v; old cycle=%q", takeover, err, claim.Fence.CycleID)
						}
					}
				}

				var callErr error
				switch write {
				case "prune":
					seedExpiredObservation(t, ctx, db)
					_, callErr = db.FencedObservations().PruneFenced(ctx, fence, time.Now().UTC())
				case "promotion":
					_, callErr = db.Skills().(skills.FencedStore).UpsertFenced(ctx, fence, skills.SaveInput{
						Name: "fenced", Description: "test", Procedure: "must not save",
					})
				case "candidate upsert":
					_, callErr = db.Rumination().(rumination.FencedStore).UpsertFenced(ctx, fence, rumination.Candidate{
						ID: "candidate", MonitorName: "test", Severity: rumination.SeverityLow,
						TargetKind: rumination.TargetObservation, TargetID: "target", Reason: "test",
					})
				case "auto-resolve":
					if _, err := db.Rumination().Upsert(ctx, rumination.Candidate{
						ID: "candidate", MonitorName: "test", Severity: rumination.SeverityLow,
						TargetKind: rumination.TargetObservation, TargetID: "target", Reason: "test",
					}); err != nil {
						t.Fatal(err)
					}
					callErr = db.Rumination().(rumination.FencedStore).ResolveFenced(ctx, fence, "candidate", "revision", time.Now().UTC())
				case "journal":
					_, callErr = db.FencedObservations().(memory.FencedJournalStore).SaveDreamJournalFenced(ctx, fence, &memory.Observation{
						ID: claim.Fence.CycleID, AgentID: "default", Title: "journal", Content: "test",
						Type: memory.TypeDream, Tags: []string{"dream-cycle:" + claim.Fence.CycleID},
						Importance: 3, CreatedAt: time.Now().UTC(), ValidFrom: time.Now().UTC(),
					})
				}
				if !errors.Is(callErr, maintenance.ErrFenceLost) {
					t.Fatalf("fenced %s error = %v, want ErrFenceLost", write, callErr)
				}
				assertFencedWriteUnchanged(t, ctx, db, write)
			})
		}
	}
}

func TestFencedPruneRollsBackWhenLeaseExpiresDuringWrite(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "fenced-prune-expiry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	claim, err := db.Maintenance().TryClaim(ctx, maintenance.ClaimRequest{
		OwnerToken: "expiry-owner", Mode: maintenance.ClaimManual, LeaseDuration: time.Minute,
	})
	if err != nil || claim.Status != maintenance.ClaimAcquired {
		t.Fatalf("claim = %+v, %v", claim, err)
	}
	seedExpiredObservation(t, ctx, db)
	if _, err := db.sql.ExecContext(ctx, `CREATE TRIGGER expire_fence_after_prune
		AFTER DELETE ON observations BEGIN
		UPDATE maintenance_coordinator SET lease_until = 0 WHERE singleton = 1;
	END`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.FencedObservations().PruneFenced(ctx, claim.Fence, time.Now().UTC()); !errors.Is(err, maintenance.ErrFenceLost) {
		t.Fatalf("prune error = %v, want ErrFenceLost after the write", err)
	}
	var rows int
	if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM observations WHERE id = 'expired-test'`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("prune write was not rolled back: rows=%d err=%v", rows, err)
	}
	var leaseValid int
	if err := db.sql.QueryRowContext(ctx, `SELECT lease_until > ((julianday('now') - 2440587.5) * 86400.0) FROM maintenance_coordinator`).Scan(&leaseValid); err != nil || leaseValid != 1 {
		t.Fatalf("trigger's fence expiry was not rolled back with write: valid=%d err=%v", leaseValid, err)
	}
}

func assertFencedWriteUnchanged(t *testing.T, ctx context.Context, db *DB, write string) {
	t.Helper()
	switch write {
	case "prune":
		var count int
		if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM observations WHERE title = 'expired-test'`).Scan(&count); err != nil || count != 1 {
			t.Fatalf("prune changed expired row count=%d err=%v", count, err)
		}
	case "promotion":
		var count int
		if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM skills WHERE name = 'fenced'`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("promotion wrote skill count=%d err=%v", count, err)
		}
	case "candidate upsert":
		var count int
		if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM rumination_candidates WHERE id = 'candidate'`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("candidate upsert wrote row count=%d err=%v", count, err)
		}
	case "auto-resolve":
		candidate, err := db.Rumination().Get(ctx, "candidate")
		if err != nil || candidate.Status != rumination.StatusPending {
			t.Fatalf("candidate status after rejected resolve=%v err=%v", candidate, err)
		}
	case "journal":
		var count int
		if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM observations WHERE obs_type = 'dream'`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("journal write count=%d err=%v", count, err)
		}
	}
}

func seedExpiredObservation(t *testing.T, ctx context.Context, db *DB) {
	t.Helper()
	o := &memory.Observation{
		ID: "expired-test", AgentID: "default", Title: "expired-test", Content: "expired",
		Type: memory.TypeContext, Importance: 3,
		CreatedAt: time.Now().Add(-time.Hour), ValidFrom: time.Now().Add(-time.Hour),
	}
	if err := db.Observations().Insert(ctx, o); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.ExecContext(ctx, `UPDATE observations SET expires_at = datetime('now', '-1 day') WHERE id = ?`, o.ID); err != nil {
		t.Fatal(err)
	}
}

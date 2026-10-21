package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/polyxmedia/mnemos/internal/maintenance"
	"github.com/polyxmedia/mnemos/internal/memory"
)

const dbUnixSeconds = "((julianday('now') - 2440587.5) * 86400.0)"

// MaintenanceStore coordinates deterministic maintenance against one SQLite
// database. DB owns its connection pool and closes it with the parent DB.
type MaintenanceStore struct {
	db *sql.DB
}

// Maintenance returns the coordinator store backed by this DB. File-backed
// databases use a dedicated short-wait pool; in-memory databases reuse the
// primary pool so the store can never accidentally point at a separate empty
// database.
func (d *DB) Maintenance() *MaintenanceStore {
	return &MaintenanceStore{db: d.maintenance}
}

// TryClaim atomically claims an available cycle and, when requested, checks
// whether the configured interval has elapsed. Busy and not-due are ordinary
// skip outcomes. The operation has a short internal deadline in addition to
// the caller's context so a claim never waits for the primary five-second
// SQLite busy timeout.
func (s *MaintenanceStore) TryClaim(ctx context.Context, req maintenance.ClaimRequest) (maintenance.ClaimResult, error) {
	if req.OwnerToken == "" {
		return maintenance.ClaimResult{}, fmt.Errorf("storage: maintenance owner token required")
	}
	if req.LeaseDuration <= 0 {
		return maintenance.ClaimResult{}, fmt.Errorf("storage: maintenance lease duration must be positive")
	}
	if req.Mode != maintenance.ClaimIfDue && req.Mode != maintenance.ClaimManual {
		return maintenance.ClaimResult{}, fmt.Errorf("storage: invalid maintenance claim mode")
	}
	if req.Mode == maintenance.ClaimIfDue && req.Interval <= 0 {
		return maintenance.ClaimResult{}, fmt.Errorf("storage: maintenance interval must be positive")
	}

	claimCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	tx, err := s.db.BeginTx(claimCtx, nil)
	if err != nil {
		if ctx.Err() != nil {
			return maintenance.ClaimResult{}, ctx.Err()
		}
		if errors.Is(claimCtx.Err(), context.DeadlineExceeded) || isSQLiteBusy(err) {
			return maintenance.ClaimResult{Status: maintenance.ClaimBusy}, nil
		}
		return maintenance.ClaimResult{}, fmt.Errorf("storage: begin maintenance claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	claimFailure := func(stage string, err error) (maintenance.ClaimResult, error) {
		if ctx.Err() != nil {
			return maintenance.ClaimResult{}, ctx.Err()
		}
		if errors.Is(claimCtx.Err(), context.DeadlineExceeded) || isSQLiteBusy(err) {
			return maintenance.ClaimResult{Status: maintenance.ClaimBusy}, nil
		}
		return maintenance.ClaimResult{}, fmt.Errorf("storage: %s: %w", stage, err)
	}

	var lastSuccess, leaseUntil sql.NullFloat64
	var ownerToken, cycleID sql.NullString
	var decayApplied bool
	var nowSeconds float64
	if err := tx.QueryRowContext(claimCtx, `SELECT last_success, owner_token, lease_until,
		cycle_id, decay_applied, `+dbUnixSeconds+`
		FROM maintenance_coordinator WHERE singleton = 1`).Scan(
		&lastSuccess, &ownerToken, &leaseUntil, &cycleID, &decayApplied, &nowSeconds); err != nil {
		return claimFailure("read maintenance state", err)
	}
	if ownerToken.Valid && leaseUntil.Valid && leaseUntil.Float64 > nowSeconds {
		return maintenance.ClaimResult{Status: maintenance.ClaimBusy}, nil
	}
	if req.Mode == maintenance.ClaimIfDue && cycleID.String == "" && lastSuccess.Valid &&
		nowSeconds-lastSuccess.Float64 < req.Interval.Seconds() {
		return maintenance.ClaimResult{Status: maintenance.ClaimNotDue}, nil
	}

	cycle := cycleID.String
	if cycle == "" {
		cycle = ulid.Make().String()
		decayApplied = false
	}
	if _, err := tx.ExecContext(claimCtx, `UPDATE maintenance_coordinator
		SET owner_token = ?, lease_until = `+dbUnixSeconds+` + ?, cycle_id = ?, decay_applied = ?
		WHERE singleton = 1`, req.OwnerToken, req.LeaseDuration.Seconds(), cycle, decayApplied); err != nil {
		return claimFailure("claim maintenance cycle", err)
	}
	var claimedUntil float64
	if err := tx.QueryRowContext(claimCtx, `SELECT lease_until FROM maintenance_coordinator
		WHERE singleton = 1 AND owner_token = ? AND cycle_id = ?`, req.OwnerToken, cycle).Scan(&claimedUntil); err != nil {
		return claimFailure("verify maintenance claim", err)
	}
	if err := tx.Commit(); err != nil {
		if ctx.Err() != nil {
			return maintenance.ClaimResult{}, ctx.Err()
		}
		if isSQLiteBusy(err) || errors.Is(claimCtx.Err(), context.DeadlineExceeded) {
			return maintenance.ClaimResult{Status: maintenance.ClaimBusy}, nil
		}
		return maintenance.ClaimResult{}, fmt.Errorf("storage: commit maintenance claim: %w", err)
	}
	return maintenance.ClaimResult{
		Status:     maintenance.ClaimAcquired,
		Fence:      maintenance.Fence{OwnerToken: req.OwnerToken, CycleID: cycle},
		LeaseUntil: fromUnixSeconds(claimedUntil), DecayApplied: decayApplied,
	}, nil
}

// Renew extends the active lease using SQLite's clock. A missing, expired, or
// replaced fence cannot renew the cycle.
func (s *MaintenanceStore) Renew(ctx context.Context, fence maintenance.Fence, lease time.Duration) (time.Time, error) {
	if lease <= 0 {
		return time.Time{}, fmt.Errorf("storage: maintenance lease duration must be positive")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, fmt.Errorf("storage: begin maintenance renewal: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkMaintenanceFence(ctx, tx, fence); err != nil {
		return time.Time{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE maintenance_coordinator
		SET lease_until = `+dbUnixSeconds+` + ?
		WHERE singleton = 1 AND owner_token = ? AND cycle_id = ? AND lease_until > `+dbUnixSeconds,
		lease.Seconds(), fence.OwnerToken, fence.CycleID); err != nil {
		return time.Time{}, fmt.Errorf("storage: update maintenance lease: %w", err)
	}
	if err := checkMaintenanceFence(ctx, tx, fence); err != nil {
		return time.Time{}, err
	}
	var until float64
	if err := tx.QueryRowContext(ctx, `SELECT lease_until FROM maintenance_coordinator
		WHERE singleton = 1 AND owner_token = ? AND cycle_id = ?`, fence.OwnerToken, fence.CycleID).Scan(&until); err != nil {
		return time.Time{}, fmt.Errorf("storage: read renewed lease: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return time.Time{}, fmt.Errorf("storage: commit maintenance renewal: %w", err)
	}
	return fromUnixSeconds(until), nil
}

// Complete records successful completion using SQLite's clock and clears the
// active lease and cycle only while the caller still owns an unexpired fence.
func (s *MaintenanceStore) Complete(ctx context.Context, fence maintenance.Fence) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin maintenance completion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkMaintenanceFence(ctx, tx, fence); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE maintenance_coordinator
		SET last_success = `+dbUnixSeconds+`, owner_token = NULL, lease_until = NULL,
		cycle_id = NULL, decay_applied = 0
		WHERE singleton = 1 AND owner_token = ? AND cycle_id = ? AND lease_until > `+dbUnixSeconds,
		fence.OwnerToken, fence.CycleID)
	if err != nil {
		return fmt.Errorf("storage: complete maintenance cycle: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: completion rows affected: %w", err)
	}
	if changed != 1 {
		return maintenance.ErrFenceLost
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: commit maintenance completion: %w", err)
	}
	return nil
}

// PruneFenced removes expired, unpinned observations under a valid lease.
func (s *obsStore) PruneFenced(ctx context.Context, fence maintenance.Fence, now time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("storage: begin fenced prune: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkMaintenanceFence(ctx, tx, fence); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM observations WHERE pinned = 0 AND expires_at IS NOT NULL AND expires_at <= ?`,
		now.UTC())
	if err != nil {
		return 0, fmt.Errorf("storage: fenced prune observations: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("storage: fenced prune rows affected: %w", err)
	}
	if err := checkMaintenanceFence(ctx, tx, fence); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("storage: commit fenced prune: %w", err)
	}
	return n, nil
}

// DecayImportanceFenced commits decay and the cycle marker atomically. A
// takeover of the same cycle observes the marker and does not decay twice.
func (s *obsStore) DecayImportanceFenced(ctx context.Context, fence maintenance.Fence, staleDays int, amount int) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("storage: begin fenced decay: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkMaintenanceFence(ctx, tx, fence); err != nil {
		return 0, err
	}
	var applied bool
	if err := tx.QueryRowContext(ctx, `SELECT decay_applied FROM maintenance_coordinator
		WHERE singleton = 1 AND owner_token = ? AND cycle_id = ?`, fence.OwnerToken, fence.CycleID).Scan(&applied); err != nil {
		return 0, fmt.Errorf("storage: read decay marker: %w", err)
	}
	if applied {
		if err := checkMaintenanceFence(ctx, tx, fence); err != nil {
			return 0, err
		}
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("storage: commit skipped fenced decay: %w", err)
		}
		return 0, nil
	}
	var changed int64
	if staleDays > 0 && amount > 0 {
		cutoff := time.Now().UTC().AddDate(0, 0, -staleDays)
		res, err := tx.ExecContext(ctx, `UPDATE observations
			SET importance = MAX(1, importance - ?)
			WHERE importance > 1 AND pinned = 0
			AND COALESCE(last_accessed_at, created_at) < ?`, amount, cutoff)
		if err != nil {
			return 0, fmt.Errorf("storage: fenced decay observations: %w", err)
		}
		changed, err = res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("storage: fenced decay rows affected: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE maintenance_coordinator
		SET decay_applied = 1
		WHERE singleton = 1 AND owner_token = ? AND cycle_id = ? AND lease_until > `+dbUnixSeconds,
		fence.OwnerToken, fence.CycleID); err != nil {
		return 0, fmt.Errorf("storage: mark fenced decay applied: %w", err)
	}
	if err := checkMaintenanceFence(ctx, tx, fence); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("storage: commit fenced decay: %w", err)
	}
	return changed, nil
}

var _ memory.FencedMaintenance = (*obsStore)(nil)

// SaveDreamJournalFenced inserts one explicit journal keyed by cycle ID. An
// existing matching cycle journal is returned unchanged, without bumping
// access counts or replacing its original summary.
func (s *obsStore) SaveDreamJournalFenced(ctx context.Context, fence maintenance.Fence, o *memory.Observation) (*memory.SaveResult, error) {
	if o == nil || fence.OwnerToken == "" || fence.CycleID == "" || o.ID != fence.CycleID || o.Type != memory.TypeDream {
		return nil, fmt.Errorf("storage: invalid fenced dream journal identity")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("storage: begin fenced dream journal: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := checkMaintenanceFence(ctx, tx, fence); err != nil {
		return nil, err
	}
	existing, err := scanObs(tx.QueryRowContext(ctx, selectObsSQL+` WHERE id = ?`, fence.CycleID))
	if err == nil {
		cycleTag := "dream-cycle:" + fence.CycleID
		matches := existing.Type == memory.TypeDream
		hasCycleTag := false
		for _, tag := range existing.Tags {
			hasCycleTag = hasCycleTag || tag == cycleTag
		}
		if !matches || !hasCycleTag {
			return nil, fmt.Errorf("storage: dream journal ID %q belongs to another observation: %w", fence.CycleID, memory.ErrConflict)
		}
		if err := checkMaintenanceFence(ctx, tx, fence); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("storage: commit existing dream journal: %w", err)
		}
		return &memory.SaveResult{Observation: existing, Deduped: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("storage: read dream journal cycle: %w", err)
	}
	if err := writeObservationTx(ctx, tx, o); err != nil {
		return nil, fmt.Errorf("storage: insert dream journal: %w", err)
	}
	if err := checkMaintenanceFence(ctx, tx, fence); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("storage: commit dream journal: %w", err)
	}
	return &memory.SaveResult{Observation: o}, nil
}

var _ memory.FencedJournalStore = (*obsStore)(nil)

func checkMaintenanceFence(ctx context.Context, tx *sql.Tx, fence maintenance.Fence) error {
	if fence.OwnerToken == "" || fence.CycleID == "" {
		return maintenance.ErrFenceLost
	}
	var owned bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM maintenance_coordinator
		WHERE singleton = 1 AND owner_token = ? AND cycle_id = ? AND lease_until > `+dbUnixSeconds+`
	)`, fence.OwnerToken, fence.CycleID).Scan(&owned)
	if err != nil {
		return fmt.Errorf("storage: check maintenance fence: %w", err)
	}
	if !owned {
		return maintenance.ErrFenceLost
	}
	return nil
}

func fromUnixSeconds(value float64) time.Time {
	seconds := int64(value)
	nanos := int64((value - float64(seconds)) * float64(time.Second))
	return time.Unix(seconds, nanos).UTC()
}

func isSQLiteBusy(err error) bool {
	var coded interface{ Code() int }
	return errors.As(err, &coded) && coded.Code()&0xff == 5
}

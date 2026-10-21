package dream

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/polyxmedia/mnemos/internal/maintenance"
)

// LeaseStore is the coordinator boundary consumed by dream. Storage performs
// each CAS and DB-clock expiry check in a short SQLite transaction.
type LeaseStore interface {
	TryClaim(context.Context, maintenance.ClaimRequest) (maintenance.ClaimResult, error)
	Renew(context.Context, maintenance.Fence, time.Duration) (time.Time, error)
	Complete(context.Context, maintenance.Fence) error
}

// CoordinatorConfig injects the dream runner, lease store, and validated
// budgets supplied by the TOML configuration layer.
type CoordinatorConfig struct {
	Store            LeaseStore
	Runner           *Service
	AutomaticEnabled bool
	Interval         time.Duration
	MaxPassDuration  time.Duration
	LeaseDuration    time.Duration
	RenewInterval    time.Duration
	Logger           *slog.Logger
}

// Coordinator serializes opportunity, manual, and watch dream passes through
// one durable lease and cycle fence.
type Coordinator struct {
	store            LeaseStore
	runner           *Service
	automaticEnabled bool
	interval         time.Duration
	maxPassDuration  time.Duration
	leaseDuration    time.Duration
	renewInterval    time.Duration
	log              *slog.Logger
}

// ErrLeaseBusy means another process currently owns the shared dream lease.
var ErrLeaseBusy = errors.New("dream: maintenance lease is busy")

// NewCoordinator constructs the shared driver. Budgets must already have
// defaults and product validation applied by internal/config.
func NewCoordinator(cfg CoordinatorConfig) (*Coordinator, error) {
	if cfg.Store == nil || cfg.Runner == nil {
		return nil, fmt.Errorf("dream: coordinator store and runner are required")
	}
	if cfg.Interval <= 0 || cfg.MaxPassDuration <= 0 || cfg.LeaseDuration <= 0 || cfg.RenewInterval <= 0 {
		return nil, fmt.Errorf("dream: coordinator durations must be positive")
	}
	if cfg.MaxPassDuration >= cfg.LeaseDuration || cfg.RenewInterval >= cfg.LeaseDuration {
		return nil, fmt.Errorf("dream: max pass and renewal durations must be shorter than the lease")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Coordinator{
		store: cfg.Store, runner: cfg.Runner, automaticEnabled: cfg.AutomaticEnabled,
		interval: cfg.Interval, maxPassDuration: cfg.MaxPassDuration,
		leaseDuration: cfg.LeaseDuration, renewInterval: cfg.RenewInterval, log: cfg.Logger,
	}, nil
}

// MaybeRun makes one due-only automatic attempt. Disabled, busy, or not-due
// opportunities return nil; stage, storage, and cancellation failures remain
// visible to the calling facade without failing its already-committed receipt.
func (c *Coordinator) MaybeRun(ctx context.Context) error {
	if !c.automaticEnabled {
		return nil
	}
	_, err := c.run(ctx, maintenance.ClaimIfDue, false)
	return err
}

// Run executes an explicit pass. It bypasses only the due-time check; the
// shared lease, pass budget, renewal, and stage fencing still apply.
func (c *Coordinator) Run(ctx context.Context, writeJournal bool) (*Journal, error) {
	return c.run(ctx, maintenance.ClaimManual, writeJournal)
}

// Watch runs an explicit pass immediately, then on each interval tick, while
// all attempts continue to contend for the same coordinator lease.
func (c *Coordinator) Watch(ctx context.Context, interval time.Duration, writeJournal bool) error {
	if interval <= 0 {
		return nil
	}
	if _, err := c.Run(ctx, writeJournal); err != nil {
		c.log.Warn("initial dream pass failed", "err", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := c.Run(ctx, writeJournal); err != nil {
				c.log.Warn("dream pass failed", "err", err)
			}
		}
	}
}

func (c *Coordinator) run(ctx context.Context, mode maintenance.ClaimMode, writeJournal bool) (*Journal, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	passCtx, cancel := context.WithTimeout(ctx, c.maxPassDuration)
	defer cancel()
	owner := ulid.Make().String()
	claim, err := c.store.TryClaim(passCtx, maintenance.ClaimRequest{
		OwnerToken: owner, Mode: mode, Interval: c.interval, LeaseDuration: c.leaseDuration,
	})
	if err != nil {
		return nil, fmt.Errorf("dream: claim maintenance lease: %w", err)
	}
	switch claim.Status {
	case maintenance.ClaimNotDue:
		return nil, nil
	case maintenance.ClaimBusy:
		if mode == maintenance.ClaimIfDue {
			return nil, nil
		}
		return nil, ErrLeaseBusy
	case maintenance.ClaimAcquired:
	default:
		return nil, fmt.Errorf("dream: invalid maintenance claim status %d", claim.Status)
	}

	lost := make(chan error, 1)
	stopRenewal := make(chan struct{})
	renewalDone := make(chan struct{})
	go c.renewLoop(passCtx, claim.Fence, stopRenewal, renewalDone, lost, cancel)
	journal, runErr := c.runner.Run(passCtx, claim.Fence, writeJournal)
	close(stopRenewal)
	<-renewalDone
	select {
	case renewErr := <-lost:
		runErr = errors.Join(runErr, renewErr)
	default:
	}
	if runErr == nil && passCtx.Err() != nil {
		runErr = passCtx.Err()
	}
	if runErr != nil {
		return journal, runErr
	}
	if err := c.store.Complete(passCtx, claim.Fence); err != nil {
		return journal, fmt.Errorf("dream: complete maintenance cycle: %w", err)
	}
	return journal, nil
}

func (c *Coordinator) renewLoop(ctx context.Context, fence maintenance.Fence, stop <-chan struct{}, done chan<- struct{}, lost chan<- error, cancel context.CancelFunc) {
	defer close(done)
	ticker := time.NewTicker(c.renewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			if _, err := c.store.Renew(ctx, fence, c.leaseDuration); err != nil {
				lost <- fmt.Errorf("dream: renew maintenance lease: %w", err)
				cancel()
				return
			}
		}
	}
}

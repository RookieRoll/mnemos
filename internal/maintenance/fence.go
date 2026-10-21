// Package maintenance contains the explicit fencing values shared by
// maintenance consumers and their storage implementations.
package maintenance

import (
	"errors"
	"time"
)

// Fence identifies one owner and one recoverable maintenance cycle.
// It is passed explicitly to every mutating stage; lease validity is checked
// against the coordinator row in the same database transaction as the write.
type Fence struct {
	OwnerToken string
	CycleID    string
}

// ClaimMode selects whether a claim observes the last-success due interval.
type ClaimMode uint8

const (
	// ClaimIfDue claims only when the configured interval has elapsed.
	ClaimIfDue ClaimMode = iota
	// ClaimManual skips only the due-time check; it still shares the lease.
	ClaimManual
)

// ClaimRequest contains validated coordinator inputs for one short claim.
type ClaimRequest struct {
	OwnerToken    string
	Mode          ClaimMode
	Interval      time.Duration
	LeaseDuration time.Duration
}

// ClaimStatus describes why a coordinator claim did or did not start.
type ClaimStatus uint8

const (
	ClaimAcquired ClaimStatus = iota + 1
	ClaimNotDue
	ClaimBusy
)

// ClaimResult returns the lease fence and recovery state when acquired.
type ClaimResult struct {
	Status       ClaimStatus
	Fence        Fence
	LeaseUntil   time.Time
	DecayApplied bool
}

// ErrFenceLost reports that a worker no longer owns the unexpired cycle.
var ErrFenceLost = errors.New("maintenance: lease fence lost")

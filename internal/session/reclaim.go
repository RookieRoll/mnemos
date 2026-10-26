package session

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// OpenBacklog reports how many sessions are still open and when the oldest
// one started. The CLI stats command and both MCP stats surfaces render this
// single value, so the three views cannot disagree about the backlog.
type OpenBacklog struct {
	// Count is the number of sessions with no end time.
	Count int
	// OldestStartedAt is the start time of the oldest open session. It is
	// the zero time when Count is 0.
	OldestStartedAt time.Time
}

// OpenBacklog reads the number of open sessions and the start time of the
// oldest one in a single pass. Every project and every agent counts: the
// backlog line answers "how much bookkeeping is unreclaimed", not "who is
// active where".
func (s *Service) OpenBacklog(ctx context.Context) (OpenBacklog, error) {
	open, err := s.store.ListOpen(ctx, "")
	if err != nil {
		return OpenBacklog{}, fmt.Errorf("session backlog: %w", err)
	}
	backlog := OpenBacklog{Count: len(open)}
	for i := range open {
		if i == 0 || open[i].StartedAt.Before(backlog.OldestStartedAt) {
			backlog.OldestStartedAt = open[i].StartedAt
		}
	}
	return backlog, nil
}

// StaleOpenInput selects open sessions for bulk reclamation, the
// `mnemos sessions close --stale <duration>` path.
type StaleOpenInput struct {
	// Before selects only sessions that started strictly earlier than this
	// instant. Strictness matters at the boundary: a session that started
	// exactly at the cutoff is not stale, which is what keeps
	// `--stale 0` (had it been allowed) from closing the session that just
	// started.
	Before time.Time
	// Project narrows the selection to one project. Empty means every
	// project.
	Project string
	// Limit caps how many sessions are returned, oldest stale sessions
	// first. Zero or negative means no cap.
	Limit int
}

// StaleCutoff converts a --stale duration into the cutoff instant
// SelectStaleOpen expects. The duration must be positive: zero or negative
// would put the cutoff at or after the present and reclaim live sessions.
// Callers map the error to a usage error; the CLI checks the parsed duration
// before calling so it can exit with the usage-status code.
func (s *Service) StaleCutoff(d time.Duration) (time.Time, error) {
	if d <= 0 {
		return time.Time{}, fmt.Errorf("session stale: duration must be positive, got %s", d)
	}
	return s.clock().UTC().Add(-d), nil
}

// SelectStaleOpen returns the open sessions that started strictly before
// in.Before, oldest first, truncated to in.Limit. It is a pure read: the
// caller closes the returned sessions through CloseEach, or skips that call
// for --dry-run, so preview and execution share exactly one selection path.
//
// Closed sessions are never returned, and continuity-bound sessions are
// included — reclaim is administrative and has to be able to collect every
// open row, bound or not.
//
// shortcut: selection reuses the store's open-session snapshot and applies
// the started_at predicate, ordering, and limit in Go; move the predicate
// into SQL if the open-session table exceeds ~10^4 rows.
func (s *Service) SelectStaleOpen(ctx context.Context, in StaleOpenInput) ([]Session, error) {
	if in.Before.IsZero() {
		return nil, fmt.Errorf("session stale select: before required")
	}
	open, err := s.store.ListOpen(ctx, in.Project)
	if err != nil {
		return nil, fmt.Errorf("session stale select: %w", err)
	}
	stale := make([]Session, 0, len(open))
	for _, sess := range open {
		if sess.StartedAt.Before(in.Before) {
			stale = append(stale, sess)
		}
	}
	sort.Slice(stale, func(i, j int) bool {
		if !stale[i].StartedAt.Equal(stale[j].StartedAt) {
			return stale[i].StartedAt.Before(stale[j].StartedAt)
		}
		return stale[i].ID < stale[j].ID
	})
	if in.Limit > 0 && len(stale) > in.Limit {
		stale = stale[:in.Limit]
	}
	return stale, nil
}

// CloseEach closes every session in targets with the same close payload and
// returns how many it closed. in.ID is ignored; each target keeps its own
// ID. It is the write half of SelectStaleOpen: callers preview with the
// selection and, unless --dry-run was given, close exactly those rows
// through the same per-session close path CloseAllOpen uses.
//
// A target that a concurrent writer already closed is skipped and not
// counted, matching CloseAllOpen's tolerance for double closes.
func (s *Service) CloseEach(ctx context.Context, targets []Session, in CloseInput) (int, error) {
	if in.Status != "" && !in.Status.Valid() {
		return 0, fmt.Errorf("session close: invalid status %q", in.Status)
	}
	closed, err := s.closeEach(ctx, targets, in)
	if err != nil {
		return closed, fmt.Errorf("session close each: %w", err)
	}
	return closed, nil
}

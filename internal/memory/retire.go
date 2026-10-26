package memory

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// retireSampleLimit caps the preview attached to a retirement report. Five
// lines are enough for an operator to recognise the rows a maintenance command
// is about to touch without turning the terminal into a dump.
const retireSampleLimit = 5

// RetireInput selects the observations a bulk fact-time retirement applies to.
//
// Every field is optional, but at least one selector must be supplied. With
// none, the whole live table would match, so both SelectRetire and Retire
// refuse instead of writing (the CLI reports the same condition as a usage
// error before it reaches the service).
type RetireInput struct {
	// Project restricts retirement to one project when non-empty. An empty
	// value retires across projects, subject to the remaining selectors.
	Project string
	// ImportanceMax retires observations whose importance is at or below this
	// value. Values <= 0 disable the importance predicate: importance starts
	// at 1 at save time, so 0 is never a meaningful threshold.
	ImportanceMax int
	// NoAccess restricts retirement to observations that were never accessed
	// (access_count == 0).
	NoAccess bool
	// OlderThan retires only observations created strictly before this
	// instant. The zero value disables the predicate, which is what lets the
	// other selectors stand alone. Strictness is deliberate: a row created
	// exactly at the threshold is not selected, so a duration converted at T
	// cannot catch a row saved at T. The CLI accepts a relative duration and
	// converts it to this absolute instant.
	OlderThan time.Time
}

// hasSelector reports whether the input carries at least one usable selector.
// OlderThan counts only when set: the zero time means "no created_at
// predicate", not "everything created before year one" (which would silently
// retire the whole live table).
func (in RetireInput) hasSelector() bool {
	return in.Project != "" || in.ImportanceMax > 0 || in.NoAccess || !in.OlderThan.IsZero()
}

// RetireSample is one preview line of a retirement report: the observation ID
// and title, which is all a CLI needs to show what matched.
type RetireSample struct {
	ID    string
	Title string
}

// RetireReport is the count-and-sample outcome shared by a dry-run preview and
// a real retirement, so the two paths print identical output.
//
// Count is the number of observations matched by the selection in a preview,
// and the number of rows the store actually retired in a run. Sample holds at
// most five selected observations ordered by CreatedAt then ID, which keeps
// the preview deterministic regardless of store scan order.
type RetireReport struct {
	Count  int
	Sample []RetireSample
}

// RetirementStore is the transactional bulk-write capability the retirement
// flow consumes. The adapter is expected to set valid_until on every id in one
// transaction and to skip ids that stopped being live (already invalidated or
// already retired), so a concurrent supersession is never re-stamped with a
// retirement timestamp. It is deliberately separate from Writer: retirement is
// fact-time housekeeping, not a per-row supersession, and must not set
// invalidated_at.
type RetirementStore interface {
	RetireObservations(ctx context.Context, ids []string, validUntil time.Time) (int64, error)
}

// SelectRetire returns the read-only half of retirement: the exact rows a
// --dry-run preview would print. It never writes and never bumps access
// counts, so a preview and the run that follows it select the same ids.
//
// Selection is fact-time retirement eligibility: the observation must not be
// system-invalidated (invalidated_at IS NULL), its valid_until must be unset or
// in the future at the service clock, and, when RetireInput.OlderThan is set,
// its created_at must be strictly before that instant. TTL expiry (expires_at)
// is prune's concern, not retirement's. Selection reads the full export because
// Reader exposes no predicate scan; retirement is an offline maintenance
// command, and the same read is what makes preview and execution share one code
// path.
func (s *Service) SelectRetire(ctx context.Context, in RetireInput) (RetireReport, error) {
	selected, err := s.selectRetire(ctx, in, s.clock().UTC())
	if err != nil {
		return RetireReport{}, err
	}
	return newRetireReport(selected), nil
}

// Retire sets valid_until = now on every observation selected by in, in one
// batch write, and reports the affected count with a bounded sample.
//
// Retirement only touches valid_until: created_at, invalidated_at,
// content_hash, and the indexed text stay as they were, no row is deleted, and
// no supersedes link is written. That is what keeps a historical query
// (SearchInput.AsOf before the retirement instant) returning the observation
// while a current query no longer does.
//
// The selection is the same read SelectRetire performs, and the write is
// delegated to the store's RetirementStore capability with the selected ids, so
// preview and execution cannot diverge. A store without that capability returns
// an error instead of falling back to per-row system-time invalidation.
func (s *Service) Retire(ctx context.Context, in RetireInput) (RetireReport, error) {
	now := s.clock().UTC()
	selected, err := s.selectRetire(ctx, in, now)
	if err != nil {
		return RetireReport{}, err
	}
	report := newRetireReport(selected)
	if len(selected) == 0 {
		return report, nil
	}
	store, ok := s.store.(RetirementStore)
	if !ok {
		return RetireReport{}, fmt.Errorf("memory: store does not support bulk fact-time retirement")
	}
	ids := make([]string, len(selected))
	for i := range selected {
		ids[i] = selected[i].ID
	}
	retired, err := store.RetireObservations(ctx, ids, now)
	if err != nil {
		return RetireReport{}, fmt.Errorf("memory: %w", err)
	}
	report.Count = int(retired)
	return report, nil
}

// selectRetire is the single selection path shared by preview and write. The
// returned rows are ordered by CreatedAt then ID so the bounded sample is
// stable across stores with different scan orders. The selector guard lives
// here rather than in the two entry points so preview and execution refuse the
// same empty selection, including an OlderThan that is only a zero time.
func (s *Service) selectRetire(ctx context.Context, in RetireInput, now time.Time) ([]Observation, error) {
	if !in.hasSelector() {
		return nil, fmt.Errorf("retire: at least one of project, importance_max, no_access, or older_than is required")
	}
	all, err := s.store.Export(ctx)
	if err != nil {
		return nil, fmt.Errorf("memory: %w", err)
	}
	selected := make([]Observation, 0, len(all))
	for _, o := range all {
		if retireEligible(o, in, now) {
			selected = append(selected, o)
		}
	}
	sort.SliceStable(selected, func(i, j int) bool {
		if !selected[i].CreatedAt.Equal(selected[j].CreatedAt) {
			return selected[i].CreatedAt.Before(selected[j].CreatedAt)
		}
		return selected[i].ID < selected[j].ID
	})
	return selected, nil
}

// retireEligible is the single predicate behind preview and write: a live
// observation (not system-invalidated, fact-time validity unset or in the
// future) inside the requested project and below the importance and access
// thresholds, created before the optional age cutoff.
func retireEligible(o Observation, in RetireInput, now time.Time) bool {
	if o.InvalidatedAt != nil {
		return false
	}
	if o.ValidUntil != nil && !o.ValidUntil.After(now) {
		return false
	}
	if in.Project != "" && o.Project != in.Project {
		return false
	}
	if in.ImportanceMax > 0 && o.Importance > in.ImportanceMax {
		return false
	}
	if in.NoAccess && o.AccessCount != 0 {
		return false
	}
	if !in.OlderThan.IsZero() && !o.CreatedAt.Before(in.OlderThan) {
		return false
	}
	return true
}

// newRetireReport builds the bounded sample from an already ordered selection.
func newRetireReport(selected []Observation) RetireReport {
	report := RetireReport{Count: len(selected)}
	limit := len(selected)
	if limit > retireSampleLimit {
		limit = retireSampleLimit
	}
	report.Sample = make([]RetireSample, 0, limit)
	for i := 0; i < limit; i++ {
		report.Sample = append(report.Sample, RetireSample{ID: selected[i].ID, Title: selected[i].Title})
	}
	return report
}

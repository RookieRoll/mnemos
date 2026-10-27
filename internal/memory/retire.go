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
// a real retirement, so the two paths print the same report for an unchanged
// store.
//
// Count is the number of observations matched by the selection in a preview,
// and the number of rows the store actually retired in a run. Sample holds at
// most five of those same rows ordered by CreatedAt then ID, which keeps the
// report deterministic regardless of store scan order.
//
// On the run path both fields are drawn from the rows the store reported as
// written, so a row that stopped matching between the selection read and the
// write is neither counted nor sampled. A store that offers only
// RetirementStore returns no ids; for it Count is still the rows actually
// written, but Sample falls back to the pre-write selection and can name a row
// the write skipped.
type RetireReport struct {
	Count  int
	Sample []RetireSample
}

// RetirementStore is the transactional bulk-write capability the retirement
// flow consumes. The adapter is expected to set valid_until on every id in one
// transaction and to skip ids that stopped being live (already invalidated, not
// yet effective, or already retired), so a concurrent supersession is never
// re-stamped with a retirement timestamp. It is deliberately separate from
// Writer: retirement is fact-time housekeeping, not a per-row supersession, and
// must not set invalidated_at.
//
// The capability is deliberately narrow: it sees ids and a timestamp, so it can
// re-check only the lifecycle columns. A store that can also re-check the
// selectors should implement GuardedRetirementStore, which the service prefers.
type RetirementStore interface {
	RetireObservations(ctx context.Context, ids []string, validUntil time.Time) (int64, error)
}

// GuardedRetirementStore is the stronger bulk-write capability: it re-applies
// the caller's selectors inside the retirement UPDATE and returns the ids it
// actually stamped.
//
// Retirement reads the candidates and then writes them, and those are separate
// statements, so a row can change in between: a read bumps its access counter,
// the dream pass lowers its importance, a supersession invalidates it. This
// capability closes that window by repeating every selector in the same
// statement as the write — a row that stopped matching is skipped instead of
// stamped — and by naming the retired rows so the caller can report what the
// write really did rather than what the earlier read selected.
type GuardedRetirementStore interface {
	RetirementStore
	RetireObservationsGuarded(ctx context.Context, ids []string, in RetireInput, validUntil time.Time) ([]string, error)
}

// SelectRetire returns the read-only half of retirement: the exact rows a
// --dry-run preview would print. It never writes and never bumps access
// counts, so a preview and a run that follows it over an unchanged store filter
// the same rows.
//
// Selection is fact-time retirement eligibility: the observation must not be
// system-invalidated (invalidated_at IS NULL), it must already be effective
// (valid_from is not after the service clock), its valid_until must be unset or
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

// Retire sets valid_until = now, in one batch write, on the observations
// selected by in that still match at write time, and reports the affected count
// with a bounded sample.
//
// Retirement only touches valid_until: created_at, invalidated_at,
// content_hash, and the indexed text stay as they were, no row is deleted, and
// no supersedes link is written. That is what keeps a historical query
// (SearchInput.AsOf before the retirement instant) returning the observation
// while a current query no longer does.
//
// The selection is the same read SelectRetire performs, so the run retires the
// rows the preview would have printed. The read and the write are separate
// statements, though, so a row can change in between: a read can bump its
// access count, the dream pass can lower its importance, a supersession can
// invalidate it. A store offering GuardedRetirementStore re-checks every
// selector inside the retirement UPDATE and names the rows it stamped, so a row
// that stopped matching is skipped and the report is built from the write
// itself (see RetireReport). A store offering only RetirementStore can re-check
// nothing but the lifecycle columns: the count stays exact, but the sample comes
// from the pre-write selection and may name a row the write skipped. A store
// without either capability returns an error instead of falling back to
// per-row system-time invalidation.
func (s *Service) Retire(ctx context.Context, in RetireInput) (RetireReport, error) {
	now := s.clock().UTC()
	selected, err := s.selectRetire(ctx, in, now)
	if err != nil {
		return RetireReport{}, err
	}
	if len(selected) == 0 {
		return newRetireReport(selected), nil
	}
	ids := make([]string, len(selected))
	for i := range selected {
		ids[i] = selected[i].ID
	}
	if store, ok := s.store.(GuardedRetirementStore); ok {
		retired, err := store.RetireObservationsGuarded(ctx, ids, in, now)
		if err != nil {
			return RetireReport{}, fmt.Errorf("memory: %w", err)
		}
		return newRetireReportRetired(selected, retired), nil
	}
	store, ok := s.store.(RetirementStore)
	if !ok {
		return RetireReport{}, fmt.Errorf("memory: store does not support bulk fact-time retirement")
	}
	retired, err := store.RetireObservations(ctx, ids, now)
	if err != nil {
		return RetireReport{}, fmt.Errorf("memory: %w", err)
	}
	report := newRetireReport(selected)
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
// observation (not system-invalidated, already effective, fact-time validity
// unset or in the future) inside the requested project and below the importance
// and access thresholds, created before the optional age cutoff.
//
// "Already effective" is ValidFrom at or before now. Retiring a row that is not
// yet effective would stamp valid_until before valid_from and leave an inverted
// fact-time interval, so a future-effective observation is not eligible until
// its instant arrives; ValidFrom is NOT NULL by schema, so the zero value is
// simply year one and never excludes a real row.
func retireEligible(o Observation, in RetireInput, now time.Time) bool {
	if o.InvalidatedAt != nil {
		return false
	}
	if o.ValidFrom.After(now) {
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

// newRetireReportRetired builds a report from the ids the store reported as
// stamped, keeping the selection's CreatedAt-then-ID order so a run's sample is
// as deterministic as a preview's. Count comes from the store, not from the
// intersection: the store is the authority on what it wrote, while the sample
// can only name rows the selection described.
func newRetireReportRetired(selected []Observation, retiredIDs []string) RetireReport {
	retired := make(map[string]struct{}, len(retiredIDs))
	for _, id := range retiredIDs {
		retired[id] = struct{}{}
	}
	report := RetireReport{Count: len(retiredIDs), Sample: make([]RetireSample, 0, retireSampleLimit)}
	for i := 0; i < len(selected) && len(report.Sample) < retireSampleLimit; i++ {
		if _, ok := retired[selected[i].ID]; !ok {
			continue
		}
		report.Sample = append(report.Sample, RetireSample{ID: selected[i].ID, Title: selected[i].Title})
	}
	return report
}

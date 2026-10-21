// Package dream runs the sleep-time compute consolidation pass: the
// "dream cycle" where Mnemos organises memory while the agent is idle.
// Each pass produces a dream journal — a single observation of type
// TypeDream summarising what was changed. This gives the agent a way to
// query what happened while it was "asleep".
//
// Consolidation operations:
//   - Importance decay: observations untouched for N days drop in importance.
//   - Prune: expired observations are removed.
//   - Dedup sweep: near-duplicate titles within the same project are linked
//     with 'refines' edges so search doesn't double-count them.
//   - (Hook for future) Skill promotion: patterns reused across sessions
//     become skill candidates.
package dream

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/maintenance"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/rumination"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
)

// Journal records the outcome of one consolidation pass. Mirrored into
// memory as an observation of type 'dream' so the agent can query past
// dreams via regular search.
type Journal struct {
	StartedAt  time.Time
	FinishedAt time.Time
	Pruned     int64
	Decayed    int64
	Linked     int64
	Promoted   int

	// PromotionDeferred counts correction clusters that cleared the
	// frequency floor but were held back by the outcome gate this pass.
	// They re-evaluate every pass as surfacing/outcome data accumulates.
	PromotionDeferred int

	// Ruminated counts candidates raised by the rumination detection
	// monitors on this pass. Inserted = fresh breaches; Updated = repeat
	// detections of a still-pending candidate whose severity or evidence
	// shifted. Resolved = candidates auto-closed because a skill or
	// observation carrying a `ruminated-from:<id>` tag was found in the
	// store.
	RuminatedInserted int
	RuminatedUpdated  int
	RuminatedResolved int

	Notes []string
}

// Summary produces the text persisted as the dream observation content.
func (j Journal) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "dream pass %s → %s\n", j.StartedAt.Format(time.RFC3339), j.FinishedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "- pruned: %d\n", j.Pruned)
	fmt.Fprintf(&b, "- decayed: %d\n", j.Decayed)
	fmt.Fprintf(&b, "- linked: %d\n", j.Linked)
	fmt.Fprintf(&b, "- promoted: %d\n", j.Promoted)
	if j.PromotionDeferred > 0 {
		fmt.Fprintf(&b, "- promotion deferred (outcome gate): %d\n", j.PromotionDeferred)
	}
	fmt.Fprintf(&b, "- ruminated: %d new, %d updated, %d auto-resolved\n",
		j.RuminatedInserted, j.RuminatedUpdated, j.RuminatedResolved)
	for _, n := range j.Notes {
		fmt.Fprintf(&b, "- %s\n", n)
	}
	return strings.TrimRight(b.String(), "\n")
}

// Service runs consolidation. Stateless — every Run is independent.
// Depends on the Maintenance surface of the observation store, the
// memory.Service for dream journal writes, and (optionally) the reader
// + skills service for correction → skill promotion and the rumination
// service for threshold-breach detection.
type Service struct {
	mem        *memory.Service
	store      memory.FencedMaintenance
	reader     memory.Reader
	skills     *skills.Service
	rumination *rumination.Service
	sessions   session.Store
	injections injection.Store
	log        *slog.Logger

	staleDays      int
	decayAmount    int
	dedupThreshold int
}

// Config bundles dependencies.
type Config struct {
	Memory      *memory.Service
	Store       memory.FencedMaintenance
	Reader      memory.Reader       // optional; enables correction → skill promotion
	Skills      *skills.Service     // optional; required alongside Reader for promotion
	Rumination  *rumination.Service // optional; enables threshold-breach detection
	Sessions    session.Store       // optional; enables the failed-session outcome signal in the promotion gate
	Injections  injection.Store     // optional; enables the recurred-after-surfacing outcome signal in the promotion gate
	Logger      *slog.Logger
	StaleDays   int // importance decay kicks in past this many days idle; default 30
	DecayAmount int // how much to subtract; default 1
	DedupWindow int // how many title-similar candidates to link per primary; default 3
}

// NewService constructs a dream service.
func NewService(cfg Config) *Service {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.StaleDays == 0 {
		cfg.StaleDays = 30
	}
	if cfg.DecayAmount == 0 {
		cfg.DecayAmount = 1
	}
	if cfg.DedupWindow == 0 {
		cfg.DedupWindow = 3
	}
	return &Service{
		mem:            cfg.Memory,
		store:          cfg.Store,
		reader:         cfg.Reader,
		skills:         cfg.Skills,
		rumination:     cfg.Rumination,
		sessions:       cfg.Sessions,
		injections:     cfg.Injections,
		log:            cfg.Logger,
		staleDays:      cfg.StaleDays,
		decayAmount:    cfg.DecayAmount,
		dedupThreshold: cfg.DedupWindow,
	}
}

// Run executes one explicitly fenced consolidation pass and returns its
// partial journal together with any stage errors. Every mutation validates
// the owner, cycle, and DB-clock lease in its write transaction.
func (s *Service) Run(ctx context.Context, fence maintenance.Fence, writeJournal bool) (*Journal, error) {
	j := &Journal{StartedAt: time.Now().UTC()}
	var stageErrors []error
	stage := func(name string, err error) bool {
		if err == nil {
			return false
		}
		stageErrors = append(stageErrors, fmt.Errorf("%s: %w", name, err))
		j.Notes = append(j.Notes, name+" stage failed")
		return errors.Is(err, maintenance.ErrFenceLost) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil
	}

	// 1. Prune expired observations.
	pruned, err := s.store.PruneFenced(ctx, fence, time.Now().UTC())
	if stage("prune", err) {
		j.FinishedAt = time.Now().UTC()
		return j, errors.Join(stageErrors...)
	}
	if err == nil {
		j.Pruned = pruned
	}

	// 2. Importance decay on stale observations.
	decayed, err := s.store.DecayImportanceFenced(ctx, fence, s.staleDays, s.decayAmount)
	if stage("decay", err) {
		j.FinishedAt = time.Now().UTC()
		return j, errors.Join(stageErrors...)
	}
	if err == nil {
		j.Decayed = decayed
	}

	// 3. Skill promotion from correction clusters. A missing reader or skill
	// service remains an intentional no-op; configured-stage errors are
	// returned so the coordinator cannot report a false success.
	if promoted, deferred, err := s.promoteSkillsFromCorrections(ctx, fence); stage("promotion", err) {
		j.FinishedAt = time.Now().UTC()
		return j, errors.Join(stageErrors...)
	} else if err == nil {
		j.Promoted = promoted
		j.PromotionDeferred = deferred
	}

	// 4. Rumination detection. Flags skills whose effectiveness has
	// dropped below the configured floor (and future monitors: stale
	// skills, contradicted conventions, superseded-fact leaks). Same
	// pattern as promotion: optional, non-fatal, guarded on nil service.
	if s.rumination != nil {
		ins, upd, err := s.rumination.PersistDetectedFenced(ctx, fence)
		if stage("rumination detection", err) {
			j.FinishedAt = time.Now().UTC()
			return j, errors.Join(stageErrors...)
		} else if err == nil {
			j.RuminatedInserted = ins
			j.RuminatedUpdated = upd
		}
		// 5. Auto-resolve candidates whose target carries the provenance
		// tag. Runs after detection so a freshly-raised candidate can be
		// closed in the same pass if the revision already landed.
		if closed, err := s.autoResolveRuminations(ctx, fence); stage("rumination auto-resolve", err) {
			j.FinishedAt = time.Now().UTC()
			return j, errors.Join(stageErrors...)
		} else if err == nil {
			j.RuminatedResolved = closed
		}
	}

	j.FinishedAt = time.Now().UTC()

	s.log.Info("dream pass",
		"pruned", j.Pruned, "decayed", j.Decayed, "linked", j.Linked,
		"promoted", j.Promoted,
		"ruminated_new", j.RuminatedInserted,
		"ruminated_upd", j.RuminatedUpdated,
		"ruminated_res", j.RuminatedResolved,
		"duration", j.FinishedAt.Sub(j.StartedAt))

	// 3. Write the dream journal as a memory observation so the agent can
	// retrieve it via standard search. Guarded because callers may run
	// consolidation frequently and don't always want an observation per run.
	if writeJournal && (len(stageErrors) > 0 || j.Pruned > 0 || j.Decayed > 0 || j.Linked > 0 || j.Promoted > 0 ||
		j.RuminatedInserted > 0 || j.RuminatedUpdated > 0 || j.RuminatedResolved > 0) {
		_, err := s.mem.SaveDreamJournalFenced(ctx, fence, memory.SaveInput{
			Title:      "dream pass " + j.StartedAt.Format("2006-01-02 15:04"),
			Content:    j.Summary(),
			Type:       memory.TypeDream,
			Tags:       []string{"consolidation", "dream"},
			Importance: 3, // low — housekeeping, not content
		})
		if err != nil {
			stage("journal", err)
			if errors.Is(err, maintenance.ErrFenceLost) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
				return j, errors.Join(stageErrors...)
			}
		}
	}

	return j, errors.Join(stageErrors...)
}

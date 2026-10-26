package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/maintenance"
)

// Service is the agent-facing API for procedural memory. It validates input
// and delegates to the Store, which handles versioning and effectiveness
// tracking.
type Service struct {
	store Store
}

// Config bundles dependencies for NewService.
type Config struct {
	Store Store
}

// NewService constructs a skill service.
func NewService(cfg Config) *Service {
	return &Service{store: cfg.Store}
}

// Save creates or versions-up a skill. A skill is keyed by (agent_id, name);
// saving the same name again bumps the version and keeps history in the
// source_sessions provenance field (if the agent includes it).
func (s *Service) Save(ctx context.Context, in SaveInput) (*Skill, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("skill save: name required")
	}
	if strings.TrimSpace(in.Description) == "" {
		return nil, fmt.Errorf("skill save: description required")
	}
	if strings.TrimSpace(in.Procedure) == "" {
		return nil, fmt.Errorf("skill save: procedure required")
	}
	return s.store.Upsert(ctx, in)
}

// SaveFenced is the dream-promotion path. It retains Save's validation and
// requires a store that can fence the upsert in its write transaction.
func (s *Service) SaveFenced(ctx context.Context, fence maintenance.Fence, in SaveInput) (*Skill, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("skill save: name required")
	}
	if strings.TrimSpace(in.Description) == "" {
		return nil, fmt.Errorf("skill save: description required")
	}
	if strings.TrimSpace(in.Procedure) == "" {
		return nil, fmt.Errorf("skill save: procedure required")
	}
	store, ok := s.store.(FencedStore)
	if !ok {
		return nil, fmt.Errorf("skill save: fenced store not configured")
	}
	return store.UpsertFenced(ctx, fence, in)
}

// Match returns skills ranked by FTS relevance, with effectiveness as a
// tiebreaker nudge (so skills that actually worked rise to the top).
func (s *Service) Match(ctx context.Context, in MatchInput) ([]Match, error) {
	if strings.TrimSpace(in.Query) == "" {
		return nil, fmt.Errorf("skill match: query required")
	}
	return s.store.Match(ctx, in)
}

// Restore reinserts an exported skill verbatim, preserving its ID, version,
// effectiveness, use/success counts, and timestamps. Unlike Save (which
// version-bumps an existing name), it is the fidelity-import path: an already
// present skill is skipped, not re-versioned. Returns true when a row was
// written.
func (s *Service) Restore(ctx context.Context, sk *Skill) (bool, error) {
	if sk.ID == "" {
		return false, fmt.Errorf("skill restore: id required")
	}
	return s.store.Restore(ctx, sk)
}

// Get returns a skill by ID.
func (s *Service) Get(ctx context.Context, id string) (*Skill, error) {
	return s.store.Get(ctx, id)
}

// List returns all skills for an agent (or all agents if empty).
func (s *Service) List(ctx context.Context, agentID string) ([]Skill, error) {
	return s.store.List(ctx, agentID)
}

// Select returns every skill whose name matches exactly, narrowed by scope.
// It is the one selection primitive behind Delete and Merge, so a --dry-run
// preview and the real run always select the same rows. Results are ordered by
// version descending, then most recently updated, then ID, which makes the
// keeper choice and the printed preview deterministic.
//
// Project scoping reads the "project:<name>" tag the promotion path writes:
// the skills table has no project column, so a skill without that tag is
// excluded by a project filter rather than being treated as matching every
// project. That is the safe direction — a filtered delete must never remove
// more than it was asked to.
func (s *Service) Select(ctx context.Context, name string, scope Scope) ([]Skill, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("skill select: name required")
	}
	all, err := s.store.List(ctx, scope.AgentID)
	if err != nil {
		return nil, fmt.Errorf("skill select: %w", err)
	}
	project := strings.TrimSpace(scope.Project)
	rows := make([]Skill, 0, len(all))
	for _, sk := range all {
		if sk.Name != name {
			continue
		}
		if project != "" && !hasTag(sk.Tags, projectTag(project)) {
			continue
		}
		rows = append(rows, sk)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Version != rows[j].Version {
			return rows[i].Version > rows[j].Version
		}
		if !rows[i].UpdatedAt.Equal(rows[j].UpdatedAt) {
			return rows[i].UpdatedAt.After(rows[j].UpdatedAt)
		}
		return rows[i].ID < rows[j].ID
	})
	return rows, nil
}

// Delete removes every skill selected by the input through the skill store's
// normal delete, so the skills_fts mirror is maintained by the table triggers
// (the skill-lifecycle contract forbids a raw SQL DELETE, which would leave
// orphan index rows behind). Selection is shared with Select, so DryRun prints
// exactly the rows a real run removes while writing nothing.
//
// A name that matches nothing is not an error: the result simply carries zero
// rows, which is what the CLI reports.
func (s *Service) Delete(ctx context.Context, in DeleteInput) (*DeleteResult, error) {
	rows, err := s.Select(ctx, in.Name, in.Scope)
	if err != nil {
		return nil, fmt.Errorf("skill delete: %w", err)
	}
	res := &DeleteResult{Rows: rows, DryRun: in.DryRun}
	if in.DryRun {
		return res, nil
	}
	for _, sk := range rows {
		if err := s.store.Delete(ctx, sk.ID); err != nil {
			return res, fmt.Errorf("skill delete %q: %w", sk.ID, err)
		}
	}
	return res, nil
}

// Merge keeps one of the skills selected by the input and absorbs the rest.
// The keeper is the highest version by default, or the version named by
// KeepVersion; every other match is folded into it — tags and source sessions
// are unioned, and the keeper's content plus version are rewritten through the
// store's normal Upsert, which is the versioning path every other skill write
// uses.
//
// "Mark superseded" is implemented as absorb-then-remove. The skills table has
// no status column and this change is deliberately migration-free (design D6),
// so a merged loser does not survive as a flagged row: its content and
// provenance live on in the keeper at a higher version, and the loser row is
// removed through the store delete so the full-text mirror drops it too.
//
// Two rows can share a name only across agents, because (agent_id, name) is
// unique, so a single-agent scope has at most one match and merges nothing.
// Merge is idempotent: a second run over an already merged name selects one row
// and writes nothing. DryRun returns the same selection and writes nothing. The
// keeper is written before any loser is removed, so a failure part-way through
// leaves the merged content in the keeper rather than deleting it.
func (s *Service) Merge(ctx context.Context, in MergeInput) (*MergeResult, error) {
	rows, err := s.Select(ctx, in.Name, in.Scope)
	if err != nil {
		return nil, fmt.Errorf("skill merge: %w", err)
	}
	res := &MergeResult{DryRun: in.DryRun}
	if len(rows) == 0 {
		return res, nil
	}
	keeper, losers, err := splitMergeRows(rows, in.KeepVersion)
	if err != nil {
		return nil, fmt.Errorf("skill merge: %w", err)
	}
	res.Keeper = &keeper
	res.Superseded = losers
	if in.DryRun || len(losers) == 0 {
		return res, nil
	}
	merged, err := s.mergeInto(ctx, keeper, losers)
	if err != nil {
		return nil, fmt.Errorf("skill merge: %w", err)
	}
	res.Keeper = merged
	for _, sk := range losers {
		if err := s.store.Delete(ctx, sk.ID); err != nil {
			return res, fmt.Errorf("skill merge: delete %q: %w", sk.ID, err)
		}
	}
	return res, nil
}

// splitMergeRows picks the keeper out of a Select result. KeepVersion above zero
// must name an exact version; zero keeps the highest version. The selection is
// already ordered, so ties break deterministically instead of by map iteration.
func splitMergeRows(rows []Skill, keepVersion int) (Skill, []Skill, error) {
	if keepVersion < 0 {
		return Skill{}, nil, fmt.Errorf("keep version must be positive, got %d", keepVersion)
	}
	keeperIdx := 0
	if keepVersion > 0 {
		keeperIdx = -1
		for i := range rows {
			if rows[i].Version == keepVersion {
				keeperIdx = i
				break
			}
		}
		if keeperIdx < 0 {
			return Skill{}, nil, fmt.Errorf("no match at version %d", keepVersion)
		}
	}
	losers := make([]Skill, 0, len(rows)-1)
	for i, sk := range rows {
		if i == keeperIdx {
			continue
		}
		losers = append(losers, sk)
	}
	return rows[keeperIdx], losers, nil
}

// mergeSectionHeader labels content a merge appended to the keeper, so a reader
// can tell the keeper's own text from what arrived from another row.
const mergeSectionHeader = "## Merged from"

// mergeInto folds losers into keeper. The keeper's identity fields (agent, name,
// id) are untouched — only its content, tags, provenance, and version move — so
// anything referencing the keeper (rumination records, promoted-origin tags)
// keeps pointing at a live skill.
func (s *Service) mergeInto(ctx context.Context, keeper Skill, losers []Skill) (*Skill, error) {
	in := SaveInput{
		AgentID:        keeper.AgentID,
		Name:           keeper.Name,
		Description:    strings.TrimSpace(keeper.Description),
		Procedure:      strings.TrimRight(keeper.Procedure, "\n"),
		Pitfalls:       strings.TrimRight(keeper.Pitfalls, "\n"),
		Tags:           append([]string(nil), keeper.Tags...),
		SourceSessions: append([]string(nil), keeper.SourceSessions...),
	}
	for _, loser := range losers {
		source := fmt.Sprintf("%s v%d", loser.AgentID, loser.Version)
		in.Description = mergeText(in.Description, strings.TrimSpace(loser.Description))
		in.Procedure = mergeSection(in.Procedure, source, loser.Procedure)
		in.Pitfalls = mergeSection(in.Pitfalls, source, loser.Pitfalls)
		in.Tags = unionStrings(in.Tags, loser.Tags)
		in.SourceSessions = unionStrings(in.SourceSessions, loser.SourceSessions)
	}
	if in.Description == "" {
		in.Description = fmt.Sprintf("Merged skill %q", keeper.Name)
	}
	return s.Save(ctx, in)
}

// mergeText appends extra to base once, keeping the merged description free of
// verbatim repeats when rows already shared text.
func mergeText(base, extra string) string {
	if extra == "" || strings.Contains(base, extra) {
		return base
	}
	if base == "" {
		return extra
	}
	return base + "\n" + extra
}

// mergeSection appends a loser's body under a labelled header. A body that is
// empty or already present is left out, so merging is stable under repetition.
func mergeSection(base, source, body string) string {
	body = strings.TrimSpace(body)
	if body == "" || strings.Contains(base, body) {
		return base
	}
	section := mergeSectionHeader + " " + source + "\n\n" + body
	if base == "" {
		return section
	}
	return base + "\n\n" + section
}

// hasTag reports whether tags contains want.
func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

// projectTag renders the tag the promotion path stamps on a promoted skill.
func projectTag(project string) string { return "project:" + project }

// unionStrings returns base followed by every extra value not already present,
// preserving base order so a no-op merge rewrites identical content.
func unionStrings(base, extra []string) []string {
	seen := make(map[string]bool, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, list := range [][]string{base, extra} {
		for _, v := range list {
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// confidenceFloor is the use_count at which a skill's measured
// effectiveness is considered fully trustworthy. Below this, we shrink
// toward the 0.5 prior so a 1/1 record doesn't outrank a 9/10 one.
const confidenceFloor = 10

// stalenessHorizonDays sets the linear decay window for the recency
// factor. A skill last used today scores 1.0; one untouched for this
// many days scores 0.0. Tuned to roughly match the dream pass's
// stale_skill_days default.
const stalenessHorizonDays = 90

// Score returns a structured quality report for one skill, combining
// raw effectiveness, use count, recency, and provenance into one
// composite number plus the inputs that built it. The intent: clients
// (dashboards, "skills ranked by lift" registries, the MCP tool surface)
// pick whichever weighting they want without recomputing from scratch.
func (s *Service) Score(ctx context.Context, id string) (*ScoreReport, error) {
	if id == "" {
		return nil, fmt.Errorf("skill score: id required")
	}
	sk, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	rep := &ScoreReport{
		SkillID:       sk.ID,
		Effectiveness: sk.Effectiveness,
		UseCount:      sk.UseCount,
		SuccessCount:  sk.SuccessCount,
		UpdatedAt:     sk.UpdatedAt,
	}

	// Bayesian shrinkage: a 1/1 skill at 1.0 effectiveness is much less
	// trustworthy than a 9/10 skill at 0.9. Pulling the low-volume
	// estimate toward the 0.5 prior corrects the small-sample
	// over-confidence; at confidenceFloor uses, the raw rate stands.
	confidence := float64(sk.UseCount) / float64(confidenceFloor)
	if confidence > 1.0 {
		confidence = 1.0
	}
	rep.AdjustedEffectiveness = sk.Effectiveness*confidence + 0.5*(1-confidence)

	// Linear decay from last update. now() reads from the same monotonic
	// source the store uses, so a skill written this turn scores 1.0.
	since := time.Since(sk.UpdatedAt).Hours() / 24
	rep.Recency = 1.0 - since/float64(stalenessHorizonDays)
	if rep.Recency < 0 {
		rep.Recency = 0
	}
	if sk.UpdatedAt.IsZero() {
		rep.Recency = 0
	}

	rep.Score = rep.AdjustedEffectiveness * rep.Recency
	return rep, nil
}

// RecordUse updates usage and effectiveness based on agent feedback. This is
// how skills earn their rank position over time.
func (s *Service) RecordUse(ctx context.Context, in FeedbackInput) error {
	if in.ID == "" {
		return fmt.Errorf("skill feedback: id required")
	}
	return s.store.RecordUse(ctx, in)
}

// ExportPack builds a shareable pack from an agent's skills. If names is
// empty, every skill for the agent is included. Passing specific names
// lets users publish a focused subset (e.g. "just the go error-handling
// skills"). Returns ErrNotFound if any requested name is missing.
func (s *Service) ExportPack(ctx context.Context, agentID string, names []string, source PackSource) (*Pack, error) {
	all, err := s.store.List(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	if len(names) == 0 {
		if len(all) == 0 {
			return nil, fmt.Errorf("export: no skills to pack")
		}
		return BuildPack(source, all), nil
	}
	byName := make(map[string]Skill, len(all))
	for _, s := range all {
		byName[s.Name] = s
	}
	selected := make([]Skill, 0, len(names))
	for _, n := range names {
		sk, ok := byName[n]
		if !ok {
			return nil, fmt.Errorf("export: skill %q not found", n)
		}
		selected = append(selected, sk)
	}
	return BuildPack(source, selected), nil
}

// ImportResult summarises what ImportPack did. Counts distinguish skills
// that were created brand-new from ones that updated an existing skill
// under the same name (producing a version bump).
type ImportResult struct {
	Created int
	Updated int
	Source  PackSource
}

// ImportPack persists every skill in the pack for the given agent.
// Existing skills with the same name are version-bumped via Upsert; new
// ones are created. Returns a summary of what changed.
func (s *Service) ImportPack(ctx context.Context, agentID string, pack *Pack) (ImportResult, error) {
	result := ImportResult{Source: pack.Source}
	existing, err := s.store.List(ctx, agentID)
	if err != nil {
		return result, fmt.Errorf("list existing: %w", err)
	}
	existingNames := make(map[string]bool, len(existing))
	for _, sk := range existing {
		existingNames[sk.Name] = true
	}
	for _, ps := range pack.Skills {
		_, err := s.store.Upsert(ctx, SaveInput{
			AgentID: agentID, Name: ps.Name,
			Description: ps.Description,
			Procedure:   ps.Procedure, Pitfalls: ps.Pitfalls,
			Tags: ps.Tags,
		})
		if err != nil {
			return result, fmt.Errorf("import %q: %w", ps.Name, err)
		}
		if existingNames[ps.Name] {
			result.Updated++
		} else {
			result.Created++
		}
	}
	return result, nil
}

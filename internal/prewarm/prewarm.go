// Package prewarm composes conventions, recent sessions, matching skills,
// correction-journal entries, and a file heat map into a single
// token-budgeted context block returned on session_start and on
// compaction-recovery calls to mnemos_context.
//
// The key idea: push, don't pull. LLMs don't reliably invoke memory tools
// on their own. When a session starts, Mnemos delivers the relevant
// context upfront — no second call required.
package prewarm

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/contextfmt"
	"github.com/polyxmedia/mnemos/internal/continuity"
	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/rumination"
	"github.com/polyxmedia/mnemos/internal/safety"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
)

// InjectionRecorder is the narrow surface prewarm needs from the injection
// log: one batched write per built block. Declared at the consumer so tests
// can capture surfacings with a two-line fake. A failed write must never
// fail the prewarm — callers treat Log as fire-and-forget.
type InjectionRecorder interface {
	Log(ctx context.Context, channel injection.Channel, agentID, project, sessionID string, refs []injection.Ref) error
}

// RuminationReader is the narrow surface prewarm needs from the rumination
// service: a queue pull (for the dedicated section) and a target lookup
// (for inline badges on matching skills). Declared at the consumer so
// tests can supply a minimal fake without touching the SQLite-backed
// store or dragging in the full rumination.Service.
type RuminationReader interface {
	Pending(ctx context.Context, limit int) ([]rumination.Candidate, error)
	PendingByTarget(ctx context.Context, kind rumination.TargetKind, targetID string) ([]rumination.Candidate, error)
}

// Service builds pre-warm blocks. It depends on the read-side of the
// observation store plus the session/skill readers and the safety scanner.
// Writes are not needed here, so we accept memory.Reader — narrower is
// easier to mock and harder to misuse.
type Service struct {
	obs        memory.Reader
	sessions   session.Store
	skills     skills.Store
	touches    memory.TouchStore
	scanner    *safety.Scanner
	rumination RuminationReader
	injections InjectionRecorder
	maxTokens  int
	clock      func() time.Time
}

// Config bundles dependencies. Rumination is optional; when nil the
// prewarm block omits the "pending reviews" section and skips the skill
// badge lookup quietly, so upgrading mnemos never breaks existing callers
// that did not yet wire the rumination service.
type Config struct {
	Observations memory.Reader
	Sessions     session.Store
	Skills       skills.Store
	Touches      memory.TouchStore
	Scanner      *safety.Scanner
	Rumination   RuminationReader
	// Injections is optional; when nil, surfacings are still returned on
	// the Block but nothing is persisted.
	Injections InjectionRecorder
	MaxTokens  int
	// Clock is optional and defaults to time.Now.
	Clock func() time.Time
}

// NewService constructs a pre-warm service. MaxTokens defaults to 500 —
// the research-backed sweet spot for session context (curate aggressively;
// bloat hurts agent performance).
func NewService(cfg Config) *Service {
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 500
	}
	if cfg.Scanner == nil {
		cfg.Scanner = safety.NewScanner()
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return &Service{
		obs:        cfg.Observations,
		sessions:   cfg.Sessions,
		skills:     cfg.Skills,
		touches:    cfg.Touches,
		scanner:    cfg.Scanner,
		rumination: cfg.Rumination,
		injections: cfg.Injections,
		maxTokens:  cfg.MaxTokens,
		clock:      cfg.Clock,
	}
}

// Mode selects which slice of context to prioritise.
type Mode int

const (
	// ModeSessionStart: a new session is opening. Emphasise conventions
	// and recent session summaries for project continuity.
	ModeSessionStart Mode = iota

	// ModeCompactionRecovery: the agent's context was compacted and we're
	// restoring mid-session state. Emphasise current session goal, in-
	// session observations, recent decisions.
	ModeCompactionRecovery

	// ModePremortem: the agent submitted a plan (Request.Goal) before
	// executing it. Emphasise how similar attempts failed: corrections,
	// failed sessions with matching goals, skills with rumination flags,
	// conventions the plan must respect.
	ModePremortem
)

// Request parameters.
type Request struct {
	Mode      Mode
	AgentID   string
	Project   string
	SessionID string
	Goal      string
	MaxTokens int
	// Continuity is a prevalidated read result for an opt-in independent
	// session start. Nil retains the legacy pipeline, including recent sessions.
	Continuity *continuity.ContextResult
}

// Block is the composed pre-warm payload. Surfaced lists the observation
// and skill IDs that made it into Text after token budgeting — sections
// dropped by the budget do not count as surfaced.
type Block struct {
	Text          string
	TokenEstimate int
	Sections      []Section
	Surfaced      []injection.Ref
	SafetyReport  safety.Report
	// ContinuityRefs identifies only source bodies actually rendered in Text.
	// These are not observation/skill injection references or consumption events.
	ContinuityRefs []continuity.Ref
}

// Section is one labeled piece of the block.
type Section struct {
	Title string
	Body  string
}

// Build assembles the pre-warm block for a given request. The block is
// capped at Request.MaxTokens (or the service default) and each section is
// sanitised before inclusion — memory stores are an attack surface and
// we never inject raw content blindly.
func (s *Service) Build(ctx context.Context, req Request) (*Block, error) {
	if req.Continuity != nil {
		if req.Mode != ModeSessionStart || strings.TrimSpace(req.AgentID) == "" || req.SessionID == "" {
			return nil, fmt.Errorf("prewarm: continuity requires session-start with an explicit owner and session")
		}
		current, err := s.sessions.Get(ctx, req.SessionID)
		if err != nil {
			return nil, fmt.Errorf("prewarm: continuity current session: %w", err)
		}
		if current.AgentID != req.AgentID || current.Project != req.Project {
			return nil, fmt.Errorf("prewarm: continuity current session scope mismatch")
		}
	}
	budget := req.MaxTokens
	if budget <= 0 {
		budget = s.maxTokens
	}

	pipeline := s.pipeline(req.Mode)
	if req.Continuity != nil {
		// The scoped continuity result replaces all legacy session-history
		// delivery, including when it contains no candidate or selected source.
		pipeline = []stepFunc{stepPinnedCore, stepContinuity, stepConventions,
			stepPendingRumination, stepMatchingSkills, stepCorrections, stepHotFiles}
	}

	block := &Block{}
	var sections []sectionDraft
	for _, step := range pipeline {
		draft, err := step(ctx, s, req)
		if err != nil {
			return nil, fmt.Errorf("prewarm %s: %w", draft.title, err)
		}
		if draft.body == "" && len(draft.items) == 0 {
			continue
		}
		sections = append(sections, draft)
	}

	// Scan complete candidate material before selection; section scans preserve
	// detection across item boundaries even when compact forms are selected.
	seen := map[injection.Ref]bool{}
	for _, d := range sections {
		var ids []string
		for _, item := range d.items {
			if item.ref.Kind == injection.KindObservation {
				ids = append(ids, item.ref.ID)
			}
		}
		if len(ids) > 0 {
			if contradictions, err := s.obs.ListContradictors(ctx, ids); err == nil {
				for i := range d.items {
					for _, c := range contradictions {
						if c.CandidateID == d.items[i].ref.ID && !strings.Contains(d.items[i].full, "[contradicted") {
							warning := "[contradicted] "
							d.items[i].full = warning + d.items[i].full
							d.items[i].compact = warning + d.items[i].compact
							d.items[i].minimal = warning + d.items[i].minimal
						}
					}
				}
			}
		}
		rep := s.scanner.Scan(d.material())
		sanitisedReport := s.scanner.Scan(rep.Sanitised)
		rep.Findings = append(rep.Findings, sanitisedReport.Findings...)
		if sanitisedReport.MaxRisk > rep.MaxRisk {
			rep.MaxRisk = sanitisedReport.MaxRisk
		}
		if rep.MaxRisk > block.SafetyReport.MaxRisk {
			block.SafetyReport.MaxRisk = rep.MaxRisk
		}
		block.SafetyReport.Findings = append(block.SafetyReport.Findings, rep.Findings...)
		var selected []string
		var refs []injection.Ref
		var continuityRefs []continuity.Ref
		total := len(d.items)
		if total == 0 && d.body != "" {
			d.items = []renderItem{{full: d.body}}
		}
		frame := func(lines []string) string {
			body := strings.Join(lines, "\n")
			if d.pinned && len(lines) < total {
				if body != "" {
					body += "\n"
				}
				body += fmt.Sprintf("[pinned: %d of %d shown]", len(lines), total)
			}
			selectedReport := s.scanner.Scan(body)
			body = selectedReport.Sanitised
			if selectedReport.MaxRisk > rep.MaxRisk {
				selectedReport.Findings = append(selectedReport.Findings, rep.Findings...)
			} else {
				selectedReport = rep
			}
			if body != "" && selectedReport.MaxRisk >= safety.RiskHigh {
				body = safety.WrapFlagged(body, selectedReport)
			}
			return body
		}
		for _, item := range d.items {
			if item.ref.ID != "" && seen[item.ref] {
				continue
			}
			for _, form := range []string{item.full, item.compact, item.minimal} {
				if form == "" {
					continue
				}
				form = s.scanner.Scan(form).Sanitised
				next := append(append([]string{}, selected...), form)
				body := frame(next)
				prefix := block.Text
				if prefix != "" {
					prefix += "\n\n"
				}
				entry := "## " + d.title + "\n" + body
				if contextfmt.Choose(prefix, budget, entry) == "" {
					continue
				}
				if d.pinned && contextfmt.Estimate(body) > 150 {
					continue
				}
				selected = next
				if item.ref.ID != "" {
					refs = append(refs, item.ref)
					seen[item.ref] = true
				}
				if item.continuityRef != nil {
					continuityRefs = append(continuityRefs, *item.continuityRef)
				}
				break
			}
		}
		body := frame(selected)
		if body == "" {
			continue
		}
		entry := "## " + d.title + "\n" + body
		prefix := block.Text
		if prefix != "" {
			prefix += "\n\n"
		}
		if contextfmt.Choose(prefix, budget, entry) == "" {
			continue
		}
		finalReport := s.scanner.Scan(body)
		if finalReport.MaxRisk > block.SafetyReport.MaxRisk {
			block.SafetyReport.MaxRisk = finalReport.MaxRisk
		}
		block.SafetyReport.Findings = append(block.SafetyReport.Findings, finalReport.Findings...)
		block.Text = prefix + entry
		block.Sections = append(block.Sections, Section{Title: d.title, Body: body})
		block.Surfaced = append(block.Surfaced, refs...)
		block.ContinuityRefs = append(block.ContinuityRefs, continuityRefs...)
	}
	block.TokenEstimate = contextfmt.Estimate(block.Text)

	// Fire-and-forget: the surfacing log is measurement, and measurement
	// must never break the surface being measured.
	if s.injections != nil && len(block.Surfaced) > 0 {
		channel := injection.ChannelPrewarm
		switch req.Mode {
		case ModeCompactionRecovery:
			channel = injection.ChannelRecovery
		case ModePremortem:
			channel = injection.ChannelPremortem
		}
		_ = s.injections.Log(ctx, channel, req.AgentID, req.Project, req.SessionID, block.Surfaced)
	}
	return block, nil
}

// --- pipeline -----------------------------------------------------------

type renderItem struct {
	full, compact, minimal string
	ref                    injection.Ref
	continuityRef          *continuity.Ref
	safetyMaterial         string
}

type sectionDraft struct {
	title  string
	body   string
	items  []renderItem
	pinned bool
}

func (d sectionDraft) material() string {
	parts := []string{d.body}
	for _, item := range d.items {
		parts = append(parts, item.full)
		if item.safetyMaterial != "" {
			parts = append(parts, item.safetyMaterial)
		}
	}
	return strings.Join(parts, "\n")
}

func observationItem(o memory.Observation, text, warning string) renderItem {
	identity := fmt.Sprintf("- [%s id: %s]", o.Type, o.ID)
	return renderItem{full: warning + text, compact: warning + identity + " " + oneLine(o.Title), minimal: warning + identity,
		ref: injection.Ref{Kind: injection.KindObservation, ID: o.ID}}
}

type stepFunc func(ctx context.Context, s *Service, req Request) (sectionDraft, error)

func (s *Service) pipeline(mode Mode) []stepFunc {
	switch mode {
	case ModeCompactionRecovery:
		return []stepFunc{
			stepPinnedCore,
			stepCurrentSession,
			stepInSessionObservations,
			stepConventions,
			stepPendingRumination,
			stepCorrections,
			stepHotFiles,
		}
	case ModePremortem:
		// Failure-first ordering: the question is "how have attempts like
		// this plan died", so corrections and failed sessions lead, and
		// the correction cap is wider than the session-start block's.
		return []stepFunc{
			stepPinnedCore,
			stepPremortemCorrections,
			stepFailedSessions,
			stepMatchingSkills,
			stepConventions,
			stepPendingRumination,
		}
	default: // ModeSessionStart
		return []stepFunc{
			stepPinnedCore,
			stepConventions,
			stepPendingRumination,
			stepRecentSessions,
			stepMatchingSkills,
			stepCorrections,
			stepHotFiles,
		}
	}
}

// stepPinnedCore renders Tier-0 pinned core memories before all other
// sections in all modes. Budget: ~150 tokens. Ordering: importance desc,
// created_at asc. Transparent overflow notes how many are shown.
func stepPinnedCore(ctx context.Context, s *Service, req Request) (sectionDraft, error) {
	draft := sectionDraft{title: "core invariants (pinned)", pinned: true}
	list, err := s.obs.ListPinned(ctx, req.AgentID, req.Project, 50)
	if err != nil {
		return draft, err
	}
	for _, o := range list {
		text := fmt.Sprintf("- [%s] %s — %s", o.Type, oneLine(o.Title), oneLine(o.Content))
		draft.items = append(draft.items, observationItem(o, text, ""))
	}
	return draft, nil
}

func stepPendingRumination(ctx context.Context, s *Service, req Request) (sectionDraft, error) {
	draft := sectionDraft{title: "pending reviews"}
	if s.rumination == nil {
		return draft, nil
	}
	cands, err := s.rumination.Pending(ctx, 3)
	if err != nil {
		// Non-fatal: rumination surface is best-effort. The rest of the
		// prewarm must still assemble even if the queue read fails.
		return draft, nil
	}
	if len(cands) == 0 {
		return draft, nil
	}
	var b strings.Builder
	for _, c := range cands {
		fmt.Fprintf(&b, "- [%s] %s · %s (id: %s)\n",
			c.Severity, c.MonitorName, oneLine(c.Reason), c.ID)
	}
	b.WriteString("\nResolve via mnemos_ruminate_pack <id> then mnemos_ruminate_resolve | _dismiss.")
	draft.body = b.String()
	return draft, nil
}

func stepConventions(ctx context.Context, s *Service, req Request) (sectionDraft, error) {
	draft := sectionDraft{title: "conventions"}
	if req.Project == "" {
		return draft, nil
	}
	list, err := s.obs.ListByProject(ctx, req.AgentID, req.Project, memory.TypeConvention, 10)
	if err != nil {
		return draft, err
	}
	for _, o := range list {
		text := "- " + oneLine(o.Title)
		if strings.TrimSpace(o.Rationale) != "" {
			text += " — " + oneLine(o.Rationale)
		}
		draft.items = append(draft.items, observationItem(o, text, ""))
	}
	return draft, nil
}

// recentSessionsShown caps the recent-sessions section.
const recentSessionsShown = 3

// stepRecentSessions lists the project's previous sessions. It queries the
// project directly: taking the five newest sessions across every project
// and then filtering usually left only the session being opened right now,
// so the section read "- 2026-09-29" and nothing else. That session and
// any with neither a goal nor a summary are skipped; they carry nothing to
// continue from.
func stepRecentSessions(ctx context.Context, s *Service, req Request) (sectionDraft, error) {
	// Over-fetch so skipped rows do not starve the section.
	fetch := recentSessionsShown * 4
	var (
		recent []session.Session
		err    error
	)
	if req.Project != "" {
		recent, err = s.sessions.RecentInProject(ctx, req.AgentID, req.Project, fetch)
	} else {
		recent, err = s.sessions.Recent(ctx, req.AgentID, fetch)
	}
	if err != nil {
		return sectionDraft{title: "recent sessions"}, err
	}
	draft := sectionDraft{title: "recent sessions"}
	shown := 0
	for _, r := range recent {
		if shown >= recentSessionsShown {
			break
		}
		if r.ID == req.SessionID || (r.Goal == "" && r.Summary == "") {
			continue
		}
		shown++
		var b strings.Builder
		b.WriteString("- ")
		b.WriteString(r.StartedAt.Format("2006-01-02"))
		if r.Goal != "" {
			b.WriteString(" · ")
			b.WriteString(oneLine(r.Goal))
		}
		if r.Summary != "" {
			b.WriteString(" → ")
			b.WriteString(oneLine(r.Summary))
		}
		if r.Status != "" && r.Status != session.StatusOK {
			b.WriteString(" (")
			b.WriteString(string(r.Status))
			b.WriteString(")")
		}
		draft.items = append(draft.items, renderItem{full: b.String()})
	}
	return draft, nil
}

func stepMatchingSkills(ctx context.Context, s *Service, req Request) (sectionDraft, error) {
	if req.Goal == "" {
		return sectionDraft{title: "applicable skills"}, nil
	}
	matches, err := s.skills.Match(ctx, skills.MatchInput{
		Query:   req.Goal,
		AgentID: req.AgentID,
		Limit:   3,
	})
	if err != nil {
		return sectionDraft{title: "applicable skills"}, err
	}
	if len(matches) == 0 {
		return sectionDraft{title: "applicable skills"}, nil
	}
	draft := sectionDraft{title: "applicable skills"}
	for _, m := range matches {
		warning := ""
		if s.rumination != nil {
			pending, err := s.rumination.PendingByTarget(ctx, rumination.TargetSkill, m.Skill.ID)
			if err == nil && len(pending) > 0 {
				warning = fmt.Sprintf(" [rumination pending: %s]", pending[0].ID)
			}
		}
		identity := fmt.Sprintf("- [skill id: %s]", m.Skill.ID)
		draft.items = append(draft.items, renderItem{full: "- " + oneLine(m.Skill.Name) + ": " + oneLine(m.Skill.Description) + warning,
			compact: identity + " " + oneLine(m.Skill.Name) + warning, minimal: identity + warning,
			ref: injection.Ref{Kind: injection.KindSkill, ID: m.Skill.ID}})
	}
	return draft, nil
}

func stepCorrections(ctx context.Context, s *Service, req Request) (sectionDraft, error) {
	if req.Goal == "" && req.Project == "" {
		return sectionDraft{title: "relevant corrections"}, nil
	}
	query := req.Goal
	if query == "" {
		query = req.Project
	}
	results, err := s.obs.Search(ctx, memory.SearchInput{
		Query:   query,
		Type:    memory.TypeCorrection,
		AgentID: req.AgentID,
		Project: req.Project,
		Limit:   5,
	})
	if err != nil {
		return sectionDraft{title: "relevant corrections"}, err
	}
	if len(results) == 0 {
		return sectionDraft{title: "relevant corrections"}, nil
	}
	contradictedIDs := map[string]bool{}
	for _, r := range results {
		if len(r.ContradictedBy) > 0 {
			contradictedIDs[r.Observation.ID] = true
		}
	}
	if len(results) > 0 {
		cids := make([]string, len(results))
		for i, r := range results {
			cids[i] = r.Observation.ID
		}
		if contras, err := s.obs.ListContradictors(ctx, cids); err == nil {
			for _, c := range contras {
				contradictedIDs[c.CandidateID] = true
			}
		}
	}

	draft := sectionDraft{title: "relevant corrections"}
	for _, r := range results {
		if len(draft.items) >= 3 {
			break
		}
		o := r.Observation
		warning := ""
		if contradictedIDs[o.ID] {
			warning = "[contradicted] "
		}
		draft.items = append(draft.items, observationItem(o, "- "+oneLine(o.Title)+": "+oneLine(o.Content), warning))
	}
	return draft, nil
}

// stepPremortemCorrections is stepCorrections with a wider cap and the
// full content rendered. In a premortem the corrections ARE the payload —
// the agent asked "how does this plan fail", so we spend tokens on the
// tried/wrong/fix substance rather than headline-only lines.
func stepPremortemCorrections(ctx context.Context, s *Service, req Request) (sectionDraft, error) {
	draft := sectionDraft{title: "how similar attempts failed"}
	if req.Goal == "" {
		return draft, nil
	}
	results, err := s.obs.Search(ctx, memory.SearchInput{
		Query:   req.Goal,
		Type:    memory.TypeCorrection,
		AgentID: req.AgentID,
		Project: req.Project,
		Limit:   6,
	})
	if err != nil {
		return draft, err
	}
	if len(results) == 0 {
		return draft, nil
	}
	contradictedIDs := map[string]bool{}
	for _, r := range results {
		if len(r.ContradictedBy) > 0 {
			contradictedIDs[r.Observation.ID] = true
		}
	}
	if len(results) > 0 {
		cids := make([]string, len(results))
		for i, r := range results {
			cids[i] = r.Observation.ID
		}
		if contras, err := s.obs.ListContradictors(ctx, cids); err == nil {
			for _, c := range contras {
				contradictedIDs[c.CandidateID] = true
			}
		}
	}

	for _, r := range results {
		o := r.Observation
		warning := ""
		if contradictedIDs[o.ID] {
			warning = "[contradicted] "
		}
		draft.items = append(draft.items, observationItem(o, "- "+oneLine(o.Title)+": "+oneLine(o.Content), warning))
	}
	return draft, nil
}

// stepFailedSessions surfaces past sessions that ended failed, blocked, or
// abandoned and whose goal/summary overlaps the plan. These are the
// concrete precedents a premortem exists to surface: not "what went wrong
// somewhere" but "this exact kind of attempt has died before".
func stepFailedSessions(ctx context.Context, s *Service, req Request) (sectionDraft, error) {
	draft := sectionDraft{title: "similar sessions that failed"}
	if req.Goal == "" {
		return draft, nil
	}
	recent, err := s.sessions.Recent(ctx, req.AgentID, 200)
	if err != nil {
		return draft, err
	}
	planTokens := overlapTokens(req.Goal)
	type scored struct {
		sess    session.Session
		overlap int
	}
	var candidates []scored
	for _, r := range recent {
		switch r.Status {
		case session.StatusFailed, session.StatusBlocked, session.StatusAbandoned:
		default:
			continue
		}
		if req.Project != "" && r.Project != "" && r.Project != req.Project {
			continue
		}
		n := countOverlap(planTokens, overlapTokens(r.Goal+" "+r.Summary))
		if n == 0 {
			continue
		}
		candidates = append(candidates, scored{sess: r, overlap: n})
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].overlap > candidates[j].overlap })
	if len(candidates) == 0 {
		return draft, nil
	}
	for i, c := range candidates {
		if i >= 3 {
			break
		}
		var b strings.Builder
		b.WriteString("- ")
		b.WriteString(c.sess.StartedAt.Format("2006-01-02"))
		if c.sess.Goal != "" {
			b.WriteString(" · ")
			b.WriteString(oneLine(c.sess.Goal))
		}
		if c.sess.Summary != "" {
			b.WriteString(" → ")
			b.WriteString(oneLine(c.sess.Summary))
		}
		fmt.Fprintf(&b, " (%s)", c.sess.Status)
		draft.items = append(draft.items, renderItem{full: b.String()})
	}
	return draft, nil
}

// overlapTokens lowercases and splits text into a set of words longer than
// two characters, for cheap goal-vs-plan similarity.
func overlapTokens(text string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(strings.ToLower(text)) {
		w = strings.Trim(w, ".,;:!?()[]{}\"'`")
		if len(w) > 2 {
			out[w] = true
		}
	}
	return out
}

func countOverlap(a, b map[string]bool) int {
	n := 0
	for w := range a {
		if b[w] {
			n++
		}
	}
	return n
}

func stepHotFiles(ctx context.Context, s *Service, req Request) (sectionDraft, error) {
	if s.touches == nil || req.Project == "" {
		return sectionDraft{title: "hot files"}, nil
	}
	hot, err := s.touches.Hot(ctx, req.AgentID, req.Project, 5)
	if err != nil {
		return sectionDraft{title: "hot files"}, err
	}
	if len(hot) == 0 {
		return sectionDraft{title: "hot files"}, nil
	}
	draft := sectionDraft{title: "hot files"}
	for _, h := range hot {
		draft.items = append(draft.items, renderItem{full: fmt.Sprintf("- %s (%d touches)", h.Path, h.TouchCount)})
	}
	return draft, nil
}

func stepCurrentSession(ctx context.Context, s *Service, req Request) (sectionDraft, error) {
	if req.SessionID == "" {
		return sectionDraft{title: "current session"}, nil
	}
	sess, err := s.sessions.Get(ctx, req.SessionID)
	if err != nil {
		return sectionDraft{title: "current session"}, nil
	}
	age := s.clock().Sub(sess.StartedAt).Truncate(time.Minute)
	var b strings.Builder
	fmt.Fprintf(&b, "started %s ago in %s", age, sess.Project)
	if sess.Goal != "" {
		fmt.Fprintf(&b, "\ngoal: %s", sess.Goal)
	}
	return sectionDraft{title: "current session", body: b.String()}, nil
}

func stepInSessionObservations(ctx context.Context, s *Service, req Request) (sectionDraft, error) {
	if req.SessionID == "" {
		return sectionDraft{title: "session observations"}, nil
	}
	// We piggy-back on ListByProject, then filter by session — there's no
	// ListBySession in Store yet. This is cheap for realistic session sizes.
	all, err := s.obs.ListByProject(ctx, req.AgentID, req.Project, "", 50)
	if err != nil {
		return sectionDraft{title: "session observations"}, err
	}
	draft := sectionDraft{title: "session observations"}
	for _, o := range all {
		if o.SessionID != req.SessionID {
			continue
		}
		if len(draft.items) >= 8 {
			break
		}
		draft.items = append(draft.items, observationItem(o, fmt.Sprintf("- [%s] %s", o.Type, oneLine(o.Title)), ""))
	}
	return draft, nil
}

// --- helpers ------------------------------------------------------------

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.Join(strings.Fields(s), " ")
	return s
}

func estimateTokens(s string) int { return contextfmt.Estimate(s) }

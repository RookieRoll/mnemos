package dream

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/injection"
	"github.com/polyxmedia/mnemos/internal/maintenance"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
)

// correctionData is the structured payload every correction carries. Mirrors
// what mnemos_correct writes into memory.SaveInput.Structured.
type correctionData struct {
	Tried          string `json:"tried"`
	WrongBecause   string `json:"wrong_because"`
	Fix            string `json:"fix"`
	TriggerContext string `json:"trigger_context"`
}

// promotionDefaults
const (
	// minCorrectionsPerGroup is the floor that proves a pattern rather than
	// a one-off. Three is the smallest N where the mean is robust to noise
	// — two is a coincidence, three is a trend.
	minCorrectionsPerGroup = 3

	// overwhelmingGroupSize is the cluster size at which frequency alone
	// overrides the outcome gate. Five corrections on the same topic is
	// no longer plausibly a burst of duplicate saves — by then waiting for
	// outcome evidence just delays a skill the store obviously needs.
	overwhelmingGroupSize = 5

	// tagPromoted marks skills produced by this pipeline. Makes them easy
	// to filter (`mnemos skill list --promoted`) and recognise in stats.
	tagPromoted = "auto-promoted"

	// originPrefix is the tag prefix for the stable group hash. Used as
	// the idempotency key: promoting again finds the same skill by this
	// tag and bumps its version rather than creating duplicates.
	originPrefix = "promoted-origin:"
)

// promoteSkillsFromCorrections scans live correction observations across
// all projects, clusters them by (agent_id, project, label), and when a
// cluster reaches minCorrectionsPerGroup synthesises a skill. Returns the
// count of skills created or version-bumped.
//
// Labels:
//   - When a correction has tags, the first tag is the label (projects
//     naturally tag oauth/retry/serialisation corrections with matching
//     tokens).
//   - Otherwise the label is the first three words of the title, normalised.
//
// Idempotency: each group carries a stable hash (sha256 of
// agent_id|project|label, truncated). Promotion writes that hash as a
// skill tag `promoted-origin:<hash>`. A later pass finds the existing skill
// by tag and upserts — version bumps, source_sessions extend, no dupes.
// Admission: a cluster that clears the frequency floor must additionally
// pass the outcome gate (see promotionGate) before its FIRST promotion —
// frequency alone is weak evidence that a skill would change behavior.
// Version bumps of an already-admitted skill skip the gate: updating an
// existing skill with new corrections is maintenance, not admission.
func (s *Service) promoteSkillsFromCorrections(ctx context.Context, fence maintenance.Fence) (promoted, deferred int, err error) {
	if s.skills == nil || s.reader == nil {
		return 0, 0, nil
	}
	// A single bulk fetch; corrections are rarer than generic observations
	// so the limit is forgiving. If a user ever exceeds it in practice we
	// can add pagination, but 10k in-memory is cheap.
	corrections, err := s.reader.ListByProject(ctx, "", "", memory.TypeCorrection, 10000)
	if err != nil {
		return 0, 0, fmt.Errorf("list corrections: %w", err)
	}
	// Quarantine guard: promotion elevates corrections into a trusted,
	// surfaced skill, so raw-tier corrections must never feed it. Raw is
	// where untrusted content lives (tool output, agent inference, imports,
	// injection-driven saves clamped down in memory.Service.Save). Letting a
	// raw correction promote would launder poisoned input into durable
	// procedure — the exact MemoryGraft/MINJA persistence vector Bet 2
	// exists to close. ListByProject has no tier predicate, so filter here.
	eligible := make([]memory.Observation, 0, len(corrections))
	for _, c := range corrections {
		if c.TrustTier == memory.TrustRaw {
			continue
		}
		eligible = append(eligible, c)
	}
	groups := groupCorrections(eligible)

	for _, g := range groups {
		if err := ctx.Err(); err != nil {
			return promoted, deferred, err
		}
		if len(g.corrections) < minCorrectionsPerGroup {
			continue
		}
		existing, err := s.findSkillByOrigin(ctx, g.hash)
		if err != nil {
			return promoted, deferred, fmt.Errorf("find promoted origin %s: %w", g.hash, err)
		}
		if existing != nil && existing.AgentID != g.agentID {
			updated, err := s.upsertAbsorbedPromotion(ctx, fence, g, existing)
			if err != nil {
				return promoted, deferred, fmt.Errorf("refresh absorbed promotion %s: %w", g.hash, err)
			}
			if updated {
				promoted++
			}
			continue
		}
		if existing == nil {
			// No skill carries this group's origin tag yet. Before writing a new
			// row, look for one that already owns the (project, label) identity:
			// a skill promoted before the group had a tag, or a hand-written skill
			// under the derived name. Merging into it keeps one skill per
			// (project, label) instead of inserting a same-named second row.
			existing, err = s.findSkillByProjectLabel(ctx, g.agentID, g.project, g.label)
			if err != nil {
				return promoted, deferred, fmt.Errorf("find promoted skill %s/%s: %w", g.project, g.label, err)
			}
			if existing != nil {
				if foreign, ok := foreignOriginTag(existing.Tags, g.hash); ok {
					// The name and project match, but the row is already owned by
					// a different promotion group. Merging would rewrite another
					// group's skill with this group's corrections, so the promotion
					// stands down and says why; a hand-written row (no origin tag)
					// still merges. With an agent-scoped lookup and a hash over
					// (agent, project, label) this pairing should be unreachable —
					// it would take a hand-edited tag — so this guard exists to
					// keep a double-origin cycle from ever starting, not to serve a
					// live code path.
					s.log.Info("skill promotion skipped: existing skill owns a different promoted origin",
						"label", g.label, "project", g.project, "skill", existing.Name,
						"existing_origin", foreign, "group_origin", originPrefix+g.hash)
					continue
				}
				s.log.Info("skill promotion merging into existing skill",
					"label", g.label, "project", g.project,
					"skill", existing.Name, "version", existing.Version)
			}
		}
		// The gate guards admission of the group, not of the row: a group with no
		// origin tag anywhere has never been admitted, even when a same-named row
		// exists to merge into.
		if existing == nil || !hasTag(existing.Tags, originPrefix+g.hash) {
			gate := s.promotionGate(ctx, g)
			if !gate.promote {
				deferred++
				s.log.Info("skill promotion deferred",
					"label", g.label, "project", g.project,
					"corrections", len(g.corrections), "reason", gate.reason)
				continue
			}
			s.log.Info("skill promotion admitted",
				"label", g.label, "project", g.project, "reason", gate.reason)
		}
		ok, err := s.upsertPromotedSkill(ctx, fence, g, existing)
		if err != nil {
			return promoted, deferred, fmt.Errorf("promote %s: %w", g.hash, err)
		}
		if ok {
			promoted++
		}
	}
	return promoted, deferred, ctx.Err()
}

// gateResult is the explainable outcome of the promotion gate: admit or
// defer, plus the human-readable reason that lands in the dream log. The
// reason is the point — "why was this skill created" must be answerable
// from the log, the same way Bet 2 made "why was this stored" answerable.
type gateResult struct {
	promote bool
	reason  string
}

// promotionGate decides whether a correction cluster has earned its first
// promotion. Frequency thresholds alone admit skills that never change
// behavior (the Skill-Pro finding: outcome-verified admission beats
// frequency-style baselines by an order of magnitude on reuse), so beyond
// the count floor the cluster must show recurrence spread plus one piece
// of outcome evidence:
//
//   - it recurred AFTER being surfaced into context (the injection log
//     proves passive memory alone is not preventing the mistake), or
//   - a correction was born in a failed/blocked session (the mistake had
//     real cost), or
//   - the cluster hit overwhelmingGroupSize (frequency so high the gate
//     would only delay the inevitable).
//
// When neither outcome store is wired (embedded users constructing the
// dream service without Sessions/Injections), the gate falls back to
// legacy frequency-only admission rather than silently never promoting.
func (s *Service) promotionGate(ctx context.Context, g correctionGroup) gateResult {
	if s.injections == nil && s.sessions == nil {
		return gateResult{true, "outcome stores not wired; legacy frequency-only admission"}
	}

	// Recurrence spread: the same mistake across sessions (or, when
	// session stamps are missing, across days). A burst of saves inside
	// one sitting is one incident recorded three times, not a trend.
	sessionSet := map[string]bool{}
	daySet := map[string]bool{}
	for _, o := range g.corrections {
		if o.SessionID != "" {
			sessionSet[o.SessionID] = true
		}
		daySet[o.CreatedAt.UTC().Format("2006-01-02")] = true
	}
	if len(sessionSet) < 2 && len(daySet) < 2 {
		return gateResult{false, "all corrections from a single session/day burst; recurrence across sessions not yet shown"}
	}

	if len(g.corrections) >= overwhelmingGroupSize {
		return gateResult{true, fmt.Sprintf("%d corrections is overwhelming frequency evidence", len(g.corrections))}
	}

	if s.injections != nil {
		if ok, detail := s.recurredAfterSurfacing(ctx, g); ok {
			return gateResult{true, detail}
		}
	}
	if s.sessions != nil {
		if ok, detail := s.bornFromFailedSession(ctx, g); ok {
			return gateResult{true, detail}
		}
	}
	return gateResult{false, "no outcome evidence yet: never recurred after being surfaced, no failed-session origin, below overwhelming frequency"}
}

// recurredAfterSurfacing reports whether any correction in the cluster was
// saved after an earlier cluster member had already been surfaced into
// agent context. That sequence is the cheap counterfactual: the
// observation arm already ran and lost, so a stronger intervention (a
// skill) is warranted. Best-effort — injection log read failures just
// withhold this signal.
func (s *Service) recurredAfterSurfacing(ctx context.Context, g correctionGroup) (bool, string) {
	var earliest time.Time
	for _, o := range g.corrections {
		events, err := s.injections.ListByRef(ctx, injection.KindObservation, o.ID, 50)
		if err != nil {
			continue
		}
		for _, e := range events {
			if earliest.IsZero() || e.CreatedAt.Before(earliest) {
				earliest = e.CreatedAt
			}
		}
	}
	if earliest.IsZero() {
		return false, ""
	}
	for _, o := range g.corrections {
		if o.CreatedAt.After(earliest) {
			return true, fmt.Sprintf(
				"correction recurred at %s after the cluster was first surfaced at %s — passive memory is not preventing the mistake",
				o.CreatedAt.UTC().Format(time.RFC3339), earliest.UTC().Format(time.RFC3339))
		}
	}
	return false, ""
}

// bornFromFailedSession reports whether any correction in the cluster came
// from a session that ended failed or blocked. Corrections born from
// failure carry demonstrated cost, which is outcome evidence in a way that
// an ok-session save is not. Best-effort on session lookups.
func (s *Service) bornFromFailedSession(ctx context.Context, g correctionGroup) (bool, string) {
	for _, o := range g.corrections {
		if o.SessionID == "" {
			continue
		}
		sess, err := s.sessions.Get(ctx, o.SessionID)
		if err != nil || sess == nil {
			continue
		}
		if sess.Status == session.StatusFailed || sess.Status == session.StatusBlocked {
			return true, fmt.Sprintf("correction %q came from a %s session — the mistake had demonstrated cost", o.Title, sess.Status)
		}
	}
	return false, ""
}

// correctionGroup is an in-memory cluster of correction observations that
// share the same (agent_id, project, label). The hash is the idempotency
// key carried on the produced skill's tags.
type correctionGroup struct {
	agentID     string
	project     string
	label       string
	hash        string
	corrections []memory.Observation
}

// groupCorrections clusters by (agent_id, project, label). Order matters
// for deterministic output in tests: we sort keys alphabetically before
// building the result slice so the same input always produces the same
// iteration order.
func groupCorrections(obs []memory.Observation) []correctionGroup {
	// Use a keyed map while building, then flatten in deterministic order.
	type key struct{ agent, project, label string }
	byKey := make(map[key]*correctionGroup)
	for _, o := range obs {
		label := correctionLabel(o)
		if label == "" {
			continue // skip un-groupable corrections silently
		}
		k := key{agent: defaultAgent(o.AgentID), project: o.Project, label: label}
		g, ok := byKey[k]
		if !ok {
			g = &correctionGroup{
				agentID: k.agent, project: k.project, label: k.label,
				hash: groupHash(k.agent, k.project, k.label),
			}
			byKey[k] = g
		}
		g.corrections = append(g.corrections, o)
	}
	keys := make([]key, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].agent != keys[j].agent {
			return keys[i].agent < keys[j].agent
		}
		if keys[i].project != keys[j].project {
			return keys[i].project < keys[j].project
		}
		return keys[i].label < keys[j].label
	})
	out := make([]correctionGroup, 0, len(keys))
	for _, k := range keys {
		group := *byKey[k]
		sort.Slice(group.corrections, func(i, j int) bool {
			if !group.corrections[i].CreatedAt.Equal(group.corrections[j].CreatedAt) {
				return group.corrections[i].CreatedAt.Before(group.corrections[j].CreatedAt)
			}
			return group.corrections[i].ID < group.corrections[j].ID
		})
		out = append(out, group)
	}
	return out
}

// correctionLabel picks the clustering label for one correction: first tag
// if present, otherwise the first three whitespace-separated tokens of the
// title, lowercased. Empty titles yield "" (caller skips).
func correctionLabel(o memory.Observation) string {
	for _, t := range o.Tags {
		t = strings.TrimSpace(t)
		// Skip purely structural tags so the grouping key reflects the
		// subject matter rather than boilerplate.
		if t == "" || t == tagPromoted || strings.HasPrefix(t, originPrefix) {
			continue
		}
		return strings.ToLower(t)
	}
	words := strings.Fields(strings.ToLower(strings.TrimSpace(o.Title)))
	if len(words) == 0 {
		return ""
	}
	if len(words) > 3 {
		words = words[:3]
	}
	return strings.Join(words, " ")
}

func groupHash(agentID, project, label string) string {
	sum := sha256.Sum256([]byte(agentID + "|" + project + "|" + label))
	return hex.EncodeToString(sum[:])[:12]
}

func defaultAgent(id string) string {
	if id == "" {
		return "default"
	}
	return id
}

// upsertPromotedSkill synthesises the skill for the given group and writes it
// in place. existing is the row the promotion owns: either the skill already
// carrying this group's origin tag, or a skill that already owns the same
// (project, label) identity (nil on first promotion). The caller already
// resolved and gated it, and never passes a row owned by a different promotion
// group — that case stands down before the write.
//
// Merging is the whole point: a promotion never inserts a second skill with the
// same name. When a row exists its identity survives — name, tags (including the
// promoted-origin tag that keys it), and source sessions are preserved and
// unioned — while the synthesised procedure refreshes through the store's normal
// Upsert, which bumps the version. Manual prose and sections absorbed by
// skills.Merge survive later synthesis updates.
//
// Returns true when the skill was created or updated (version went up), false
// when the existing skill already matched exactly, so a pass with no membership
// change stays a no-op.
func (s *Service) upsertPromotedSkill(ctx context.Context, fence maintenance.Fence, g correctionGroup, existing *skills.Skill) (bool, error) {
	procedure, pitfalls := synthesisePromotion(g)

	sources := sessionIDs(g.corrections)
	description := fmt.Sprintf("Auto-promoted from %d corrections in %s: %s",
		len(g.corrections), g.project, g.label)
	name := promotedSkillName(g)

	tags := []string{
		tagPromoted,
		originPrefix + g.hash,
		projectTag(g.project),
	}

	if existing != nil {
		// Preserve the original name even if the label has shifted — once a
		// skill is out in the world, its identity is stable. Tags and provenance
		// union into what the row already carries, so its promoted-origin tag and
		// any hand-applied tag survive the merge.
		name = existing.Name
		tags = mergePromotedTags(existing.Tags, g)
		sources = unionStrings(existing.SourceSessions, sources)
		if !hasOriginTag(existing.Tags) {
			// Preserve the manually authored text as an attributed section before
			// the first promotion takes ownership of the synthesized portion.
			procedure = preserveManualPromotionText(procedure, existing.Procedure)
			pitfalls = preserveManualPromotionText(pitfalls, existing.Pitfalls)
			description = preserveManualPromotionText(description, existing.Description)
		} else {
			description = preservePromotedDescription(description, existing.Description)
			procedure = withMergeSections(procedure, existing.Procedure)
			pitfalls = withMergeSections(pitfalls, existing.Pitfalls)
		}
		// The body is the deterministic synthesis of the live correction set, so
		// it is rewritten rather than appended: the same set re-renders the same
		// text, and a changed set replaces it instead of accumulating history.
		// Preserved manual and absorbed sections survive subsequent rewrites.
		//
		// A pass with no new corrections since last run is a no-op — skip the
		// upsert so we don't bump the version needlessly.
		if sameSourceSet(existing.SourceSessions, sources) &&
			existing.Procedure == procedure &&
			existing.Pitfalls == pitfalls &&
			existing.Description == description &&
			sameStringSet(existing.Tags, tags) {
			return false, nil
		}
	}

	_, err := s.skills.SaveFenced(ctx, fence, skills.SaveInput{
		AgentID:        g.agentID,
		Name:           name,
		Description:    description,
		Procedure:      procedure,
		Pitfalls:       pitfalls,
		Tags:           tags,
		SourceSessions: sources,
	})
	if err != nil {
		return false, fmt.Errorf("save skill: %w", err)
	}
	return true, nil
}

// upsertAbsorbedPromotion refreshes only this admitted group's section in the
// explicit merge keeper. The owner and other absorbed/manual sections survive.
func (s *Service) upsertAbsorbedPromotion(ctx context.Context, fence maintenance.Fence, g correctionGroup, keeper *skills.Skill) (bool, error) {
	procedure, pitfalls := synthesisePromotion(g)
	procedure = refreshAbsorbedSection(keeper.Procedure, g, procedure)
	pitfalls = refreshAbsorbedSection(keeper.Pitfalls, g, pitfalls)
	sources := unionStrings(keeper.SourceSessions, sessionIDs(g.corrections))
	if procedure == keeper.Procedure && pitfalls == keeper.Pitfalls && sameSourceSet(sources, keeper.SourceSessions) {
		return false, nil
	}
	_, err := s.skills.SaveFenced(ctx, fence, skills.SaveInput{
		AgentID: keeper.AgentID, Name: keeper.Name, Description: keeper.Description,
		Procedure: procedure, Pitfalls: pitfalls, Tags: keeper.Tags, SourceSessions: sources,
	})
	if err != nil {
		return false, fmt.Errorf("save absorbed skill: %w", err)
	}
	return true, nil
}

// The first refresh upgrades skills.Merge's versioned heading to a stable
// origin heading. Later corrections replace that section instead of appending
// another copy; nested manual/merge headings remain separate preserved sections.
func refreshAbsorbedSection(body string, g correctionGroup, generated string) string {
	header := skills.MergeSectionHeader + " " + g.agentID + " (" + originPrefix + g.hash + ")"
	oldPrefix := skills.MergeSectionHeader + " " + g.agentID + " v"
	start, end, offset := -1, len(body), 0
	for _, line := range strings.SplitAfter(body, "\n") {
		if start >= 0 && strings.HasPrefix(line, skills.MergeSectionHeader+" ") {
			end = offset
			break
		}
		if strings.TrimSuffix(line, "\n") == header || strings.HasPrefix(line, oldPrefix) {
			start = offset
		}
		offset += len(line)
	}
	section := ""
	if strings.TrimSpace(generated) != "" {
		section = header + "\n\n" + strings.TrimSpace(generated)
	}
	if start < 0 {
		if section == "" {
			return body
		}
		return strings.TrimRight(body, "\n") + "\n\n" + section
	}
	var parts []string
	for _, part := range []string{strings.TrimRight(body[:start], "\n"), section, strings.TrimLeft(body[end:], "\n")} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "\n\n")
}

func preserveManualPromotionText(generated, manual string) string {
	if strings.TrimSpace(manual) == "" {
		return generated
	}
	return generated + "\n\n" + skills.MergeSectionHeader + " manual skill\n\n" + strings.TrimSpace(manual)
}

// Descriptions merged by skills.Merge predate section markers, so retain their
// appended paragraphs as well as explicitly marked sections on later passes.
func preservePromotedDescription(generated, existing string) string {
	if strings.HasPrefix(existing, "Auto-promoted from ") {
		_, rest, found := strings.Cut(existing, "\n")
		if !found {
			return generated
		}
		rest = strings.TrimPrefix(rest, "\n")
		if strings.HasPrefix(rest, skills.MergeSectionHeader) {
			return generated + "\n\n" + rest
		}
		return preserveManualPromotionText(generated, rest)
	}
	return preserveManualPromotionText(generated, existing)
}

// promotedSkillName is derived once, at first synthesis. Keeping it in one
// place makes the naming convention discoverable.
func promotedSkillName(g correctionGroup) string {
	return promotedSkillNameFor(g.project, g.label)
}

// promotedSkillNameFor renders the name for one (project, label) pair. The name
// is part of the (project, label) identity the dedup lookup matches on, so both
// the writer and the lookup go through here.
func promotedSkillNameFor(project, label string) string {
	return fmt.Sprintf("auto: %s (%s)", label, project)
}

// synthesisePromotion renders the procedure and pitfalls text from a
// group's correction observations. Each correction contributes one
// "avoid → do" pair. Triggers are collected once (deduped) to anchor the
// skill to the situations it applies to.
func synthesisePromotion(g correctionGroup) (procedure, pitfalls string) {
	triggers := map[string]bool{}
	var avoids, fixes []string
	var pitfallLines []string

	for _, o := range g.corrections {
		c := decodeCorrection(o)
		if c.TriggerContext != "" {
			triggers[strings.TrimSpace(c.TriggerContext)] = true
		}
		if c.Tried != "" && c.WrongBecause != "" {
			avoids = append(avoids, fmt.Sprintf("- %s — %s", c.Tried, c.WrongBecause))
		}
		if c.Fix != "" {
			fixes = append(fixes, "- "+c.Fix)
		}
		if c.WrongBecause != "" {
			pitfallLines = append(pitfallLines, "- "+c.WrongBecause)
		}
	}

	var b strings.Builder
	if len(triggers) > 0 {
		fmt.Fprintln(&b, "## When this applies")
		for _, t := range sortedKeys(triggers) {
			fmt.Fprintln(&b, "- "+t)
		}
		fmt.Fprintln(&b)
	}
	if len(avoids) > 0 {
		fmt.Fprintln(&b, "## Avoid")
		for _, a := range dedupeStable(avoids) {
			fmt.Fprintln(&b, a)
		}
		fmt.Fprintln(&b)
	}
	if len(fixes) > 0 {
		fmt.Fprintln(&b, "## Do")
		for _, f := range dedupeStable(fixes) {
			fmt.Fprintln(&b, f)
		}
	}

	procedure = strings.TrimRight(b.String(), "\n")
	pitfalls = strings.TrimRight(strings.Join(dedupeStable(pitfallLines), "\n"), "\n")
	return procedure, pitfalls
}

// decodeCorrection pulls the structured payload. Returns zero values if
// the observation has no structured JSON (should not happen for type=
// correction but we stay defensive).
func decodeCorrection(o memory.Observation) correctionData {
	var c correctionData
	if o.Structured == "" {
		return c
	}
	_ = json.Unmarshal([]byte(o.Structured), &c)
	return c
}

// findSkillByOrigin follows a group's stable identity even when an explicit
// cross-agent merge moved that identity into another keeper.
func (s *Service) findSkillByOrigin(ctx context.Context, hash string) (*skills.Skill, error) {
	want := originPrefix + hash
	list, err := s.skills.List(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	for i := range list {
		for _, t := range list[i].Tags {
			if t == want {
				return &list[i], nil
			}
		}
	}
	return nil, nil
}

// sessionIDs returns the union of distinct session_ids contributing to the
// group, in insertion order. Used as provenance on the promoted skill.
func sessionIDs(obs []memory.Observation) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(obs))
	for _, o := range obs {
		if o.SessionID == "" || seen[o.SessionID] {
			continue
		}
		seen[o.SessionID] = true
		out = append(out, o.SessionID)
	}
	return out
}

// findSkillByProjectLabel locates the row a promotion would otherwise duplicate:
// an existing skill for the same (project, label) that carries no tag from this
// group. Skills have no project or label column, so the identity is materialised
// as the derived promoted name plus the "project:<name>" tag the pipeline writes,
// and this lookup scans for exactly those two facts.
func (s *Service) findSkillByProjectLabel(ctx context.Context, agentID, project, label string) (*skills.Skill, error) {
	name := promotedSkillNameFor(project, label)
	wantTag := projectTag(project)
	list, err := s.skills.List(ctx, agentID)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	for i := range list {
		if list[i].Name != name {
			continue
		}
		if hasTag(list[i].Tags, wantTag) {
			return &list[i], nil
		}
	}
	return nil, nil
}

// mergePromotedTags unions the promotion's structural tags into a row it merges
// into. The row's own promoted-origin tag wins: re-running a group must never
// rewrite the identity every already-promoted skill is keyed by. A row with no
// origin tag adopts this group's, so later passes find it by hash. Any other tag
// the row already carried (manual labels, project tags) survives.
func mergePromotedTags(existing []string, g correctionGroup) []string {
	tags := append([]string(nil), existing...)
	if !hasTag(tags, tagPromoted) {
		tags = append(tags, tagPromoted)
	}
	if !hasOriginTag(tags) {
		tags = append(tags, originPrefix+g.hash)
	}
	if !hasTag(tags, projectTag(g.project)) {
		tags = append(tags, projectTag(g.project))
	}
	return tags
}

// projectTag renders the project tag the promotion path stamps on a skill.
func projectTag(project string) string { return "project:" + project }

// hasTag reports whether tags contains want.
func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

// hasOriginTag reports whether tags carries any promoted-origin identity.
func hasOriginTag(tags []string) bool {
	for _, t := range tags {
		if strings.HasPrefix(t, originPrefix) {
			return true
		}
	}
	return false
}

// foreignOriginTag reports the promoted-origin tag on a row when it belongs to a
// different group than the one being promoted. A row carrying this group's own
// tag is ours by definition, whatever else a hand edit left on it, and a row with
// no origin tag at all (hand-written, or promoted before the tag existed) is free
// to adopt this group's identity; neither counts as foreign.
func foreignOriginTag(tags []string, hash string) (string, bool) {
	want := originPrefix + hash
	foreign := ""
	for _, t := range tags {
		if t == want {
			return "", false
		}
		if foreign == "" && strings.HasPrefix(t, originPrefix) {
			foreign = t
		}
	}
	return foreign, foreign != ""
}

// withMergeSections carries the sections a skills.Merge appended to a body across
// a promotion rewrite: the synthesised text replaces the prose the promotion
// owns, while the labelled "## Merged from ..." tail is content absorbed from
// another skill and must survive. A body with no such section is returned
// unchanged. Manual text is marked as a section on its first promotion.
func withMergeSections(synthesised, existing string) string {
	preserved := mergedSectionTail(existing)
	switch {
	case preserved == "":
		return synthesised
	case synthesised == "":
		return preserved
	default:
		return synthesised + "\n\n" + preserved
	}
}

// mergedSectionTail returns everything from the first "## Merged from ..." heading
// to the end of body, or "" when the body carries none. skills.Merge only ever
// appends its sections after the keeper's own text, so the tail is exactly the
// absorbed content — including any headings inside an absorbed body, which is why
// this looks for the first marker line instead of splitting on every heading.
func mergedSectionTail(body string) string {
	if body == "" {
		return ""
	}
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), skills.MergeSectionHeader) {
			return strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
	}
	return ""
}

// unionStrings returns base followed by every extra value not already present,
// preserving base order so a repeated merge rewrites identical content.
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

// sameSourceSet reports whether two source-session lists represent the
// same set, ignoring order. Stringified skill source lists come out of
// JSON so we compare by set membership not sequence.
func sameSourceSet(a, b []string) bool {
	return sameStringSet(a, b)
}

// sameStringSet reports whether two lists hold the same values, ignoring order.
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, x := range a {
		set[x] = true
	}
	for _, x := range b {
		if !set[x] {
			return false
		}
	}
	return true
}

func dedupeStable(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

package dream

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
)

// seedCorrectionForAgent writes a curated correction owned by a specific agent.
// Two promotion groups can share a label and a project and differ only in owner;
// they must still promote to two distinct skills.
func (f *promoteFixture) seedCorrectionForAgent(t *testing.T, agent, project, tag, title string) string {
	t.Helper()
	sess, err := f.sess.Open(context.Background(), session.OpenInput{AgentID: agent, Project: project})
	if err != nil {
		t.Fatal(err)
	}
	c := correctionData{
		Tried:          "tried " + title,
		WrongBecause:   "because " + title,
		Fix:            "fix for " + title,
		TriggerContext: "trigger for " + tag,
	}
	raw, _ := json.Marshal(c)
	if _, err := f.mem.Save(context.Background(), memory.SaveInput{
		Title: title, Content: title, Type: memory.TypeCorrection,
		Tags: []string{tag}, Project: project, SessionID: sess.ID, AgentID: agent,
		Structured: string(raw), Importance: 8,
	}); err != nil {
		t.Fatal(err)
	}
	return sess.ID
}

// onlySkill asserts that exactly one skill exists and returns it. Every
// promotion-dedup case ends here, because "one skill per (project, label)" is the
// property under test.
func (f *promoteFixture) onlySkill(t *testing.T) skills.Skill {
	t.Helper()
	list, err := f.sk.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("want exactly 1 skill, got %d: %v", len(list), skillNamesForTest(list))
	}
	return list[0]
}

func skillNamesForTest(list []skills.Skill) []string {
	out := make([]string, 0, len(list))
	for _, sk := range list {
		out = append(out, sk.Name)
	}
	return out
}

// originTagOf returns the row's promoted-origin tag, failing when it carries
// none: the tag is the group identity, so its absence is always a defect.
func originTagOf(t *testing.T, sk skills.Skill) string {
	t.Helper()
	for _, tag := range sk.Tags {
		if strings.HasPrefix(tag, originPrefix) {
			return tag
		}
	}
	t.Fatalf("skill %q carries no %s tag: %v", sk.Name, originPrefix, sk.Tags)
	return ""
}

func seedCorrectionGroup(t *testing.T, f *promoteFixture, project, tag, prefix string) {
	t.Helper()
	for i := 0; i < 3; i++ {
		f.seedCorrection(t, project, tag, prefix+" "+string(rune('a'+i)))
	}
}

// TestPromotionDoesNotDuplicateSameProjectLabel is the promotion-dedup contract:
// re-promoting a group whose body changed updates the existing row — one skill,
// incremented version, unchanged origin tag — instead of writing a second one.
func TestPromotionDoesNotDuplicateSameProjectLabel(t *testing.T) {
	f := newPromoteFixture(t)
	ctx := context.Background()
	seedCorrectionGroup(t, f, "api", "oauth", "oauth retry")

	if _, err := runFencedTestPass(t, ctx, f.ds, f.db, false); err != nil {
		t.Fatal(err)
	}
	first := f.onlySkill(t)
	origin := originTagOf(t, first)

	// A fourth correction changes the synthesised body, so this pass really does
	// rewrite content rather than short-circuiting on an unchanged set.
	f.seedCorrection(t, "api", "oauth", "oauth retry d")
	j, err := runFencedTestPass(t, ctx, f.ds, f.db, false)
	if err != nil {
		t.Fatal(err)
	}
	if j.Promoted != 1 {
		t.Errorf("want 1 promotion (the version bump), got %d", j.Promoted)
	}

	merged := f.onlySkill(t)
	if merged.ID != first.ID {
		t.Errorf("promotion wrote a new row: %q -> %q", first.ID, merged.ID)
	}
	if want := first.Version + 1; merged.Version != want {
		t.Errorf("version = %d, want %d after the merge", merged.Version, want)
	}
	if got := originTagOf(t, merged); got != origin {
		t.Errorf("promoted-origin tag changed across a merge: %q -> %q", origin, got)
	}
	if !strings.Contains(merged.Procedure, "fix for oauth retry d") {
		t.Errorf("merged procedure missing the new correction: %s", merged.Procedure)
	}
	if len(merged.SourceSessions) != 4 {
		t.Errorf("source sessions = %d, want 4 after the merge", len(merged.SourceSessions))
	}
}

// TestPromotionOriginTagUnchanged pins the idempotency key: the tag is the
// 12-character sha256 of (agent_id, project, label), and neither its inputs nor
// its format may drift.
func TestPromotionOriginTagUnchanged(t *testing.T) {
	f := newPromoteFixture(t)
	ctx := context.Background()
	seedCorrectionGroup(t, f, "api", "oauth", "oauth retry")

	if _, err := runFencedTestPass(t, ctx, f.ds, f.db, false); err != nil {
		t.Fatal(err)
	}
	want := originPrefix + groupHash("default", "api", "oauth")
	first := f.onlySkill(t)
	if got := originTagOf(t, first); got != want {
		t.Fatalf("promoted-origin tag = %q, want %q", got, want)
	}

	// A second pass over the same live set keeps the tag and the row.
	if _, err := runFencedTestPass(t, ctx, f.ds, f.db, false); err != nil {
		t.Fatal(err)
	}
	again := f.onlySkill(t)
	if got := originTagOf(t, again); got != want {
		t.Errorf("promoted-origin tag drifted on rerun: %q, want %q", got, want)
	}
	if again.ID != first.ID {
		t.Errorf("rerun replaced the row: %q -> %q", first.ID, again.ID)
	}
}

// TestPromotionSameLabelDifferentProjectsMakeTwoSkills: the same label in two
// projects is legitimately two skills, so the dedup lookup must stay
// project-scoped.
func TestPromotionSameLabelDifferentProjectsMakeTwoSkills(t *testing.T) {
	f := newPromoteFixture(t)
	ctx := context.Background()
	seedCorrectionGroup(t, f, "api", "oauth", "api oauth")
	seedCorrectionGroup(t, f, "web", "oauth", "web oauth")

	j, err := runFencedTestPass(t, ctx, f.ds, f.db, false)
	if err != nil {
		t.Fatal(err)
	}
	if j.Promoted != 2 {
		t.Errorf("want 2 promotions, got %d", j.Promoted)
	}
	list, err := f.sk.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 skills (one per project), got %d: %v", len(list), skillNamesForTest(list))
	}
	byProject := map[string]skills.Skill{}
	for _, sk := range list {
		for _, tag := range sk.Tags {
			if strings.HasPrefix(tag, "project:") {
				byProject[tag] = sk
			}
		}
	}
	for _, want := range []string{"project:api", "project:web"} {
		if _, ok := byProject[want]; !ok {
			t.Errorf("no skill tagged %q: %v", want, skillNamesForTest(list))
		}
	}
	if list[0].Name == list[1].Name {
		t.Errorf("both projects produced the same skill name %q", list[0].Name)
	}
}

// TestPromotionSameLabelDifferentAgentsMakeTwoSkills: the group key includes
// agent_id, so the same label in one project promoted by two agents stays two
// skills.
func TestPromotionSameLabelDifferentAgentsMakeTwoSkills(t *testing.T) {
	f := newPromoteFixture(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		f.seedCorrectionForAgent(t, "agent-a", "api", "oauth", "a oauth "+string(rune('a'+i)))
		f.seedCorrectionForAgent(t, "agent-b", "api", "oauth", "b oauth "+string(rune('a'+i)))
	}

	if _, err := runFencedTestPass(t, ctx, f.ds, f.db, false); err != nil {
		t.Fatal(err)
	}
	list, err := f.sk.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 skills (one per agent), got %d: %v", len(list), skillNamesForTest(list))
	}
	owners := map[string]bool{}
	for _, sk := range list {
		owners[sk.AgentID] = true
	}
	for _, want := range []string{"agent-a", "agent-b"} {
		if !owners[want] {
			t.Errorf("no skill owned by %q: %v", want, skillNamesForTest(list))
		}
	}
}

// TestPromotionMergesIntoExistingSameProjectLabelSkill covers the pre-write
// lookup: a hand-written skill that already owns the derived name is merged into
// — version bumped, promotion tags added once, provenance unioned — instead of a
// second same-named row being inserted.
func TestPromotionMergesIntoExistingSameProjectLabelSkill(t *testing.T) {
	f := newPromoteFixture(t)
	ctx := context.Background()
	name := promotedSkillName(correctionGroup{project: "api", label: "oauth"})
	manual, err := f.sk.Save(ctx, skills.SaveInput{
		AgentID:     "default",
		Name:        name,
		Description: "hand-written runbook",
		Procedure:   "hand step: refresh the token first",
		Tags:        []string{"project:api", "runbook"},
	})
	if err != nil {
		t.Fatal(err)
	}

	seedCorrectionGroup(t, f, "api", "oauth", "oauth retry")
	j, err := runFencedTestPass(t, ctx, f.ds, f.db, false)
	if err != nil {
		t.Fatal(err)
	}
	if j.Promoted != 1 {
		t.Errorf("want 1 promotion into the existing row, got %d", j.Promoted)
	}

	merged := f.onlySkill(t)
	if merged.ID != manual.ID {
		t.Errorf("promotion inserted a second skill instead of merging: %q -> %q", manual.ID, merged.ID)
	}
	if want := manual.Version + 1; merged.Version != want {
		t.Errorf("version = %d, want %d", merged.Version, want)
	}
	if got, want := originTagOf(t, merged), originPrefix+groupHash("default", "api", "oauth"); got != want {
		t.Errorf("merged row adopted origin tag %q, want %q", got, want)
	}
	for _, want := range []string{tagPromoted, "project:api", "runbook"} {
		if !tagPresent(merged.Tags, want) {
			t.Errorf("merged tags %v missing %q", merged.Tags, want)
		}
	}
	if countTags(merged.Tags, "project:api") != 1 {
		t.Errorf("project tag duplicated: %v", merged.Tags)
	}
	if !strings.Contains(merged.Procedure, "## Avoid") {
		t.Errorf("merged procedure missing the synthesised body: %s", merged.Procedure)
	}

	// A rerun over the same set is a no-op rather than another version bump.
	if _, err := runFencedTestPass(t, ctx, f.ds, f.db, false); err != nil {
		t.Fatal(err)
	}
	stable := f.onlySkill(t)
	if stable.Version != merged.Version {
		t.Errorf("rerun bumped the merged skill to v%d", stable.Version)
	}
}

// TestPromotionSkipsForeignOriginTarget is the c3#1 defence: when the matching
// row already carries a promoted-origin tag for a different group, the promotion
// stands down instead of rewriting another group's skill. With an agent-scoped
// lookup and a hash over (agent, project, label) the pairing should be
// unreachable — it takes a hand-edited tag — so this test pins the guard that
// keeps a double-origin cycle from ever starting rather than a live path.
func TestPromotionSkipsForeignOriginTarget(t *testing.T) {
	f := newPromoteFixture(t)
	ctx := context.Background()
	name := promotedSkillName(correctionGroup{project: "api", label: "oauth"})
	foreign := originPrefix + "deadbeefcafe"
	existing, err := f.sk.Save(ctx, skills.SaveInput{
		AgentID:     "default",
		Name:        name,
		Description: "earlier promotion",
		Procedure:   "earlier synthesis body",
		Tags:        []string{tagPromoted, foreign, "project:api"},
	})
	if err != nil {
		t.Fatal(err)
	}

	seedCorrectionGroup(t, f, "api", "oauth", "oauth retry")
	j, err := runFencedTestPass(t, ctx, f.ds, f.db, false)
	if err != nil {
		t.Fatal(err)
	}
	if j.Promoted != 0 {
		t.Errorf("promotion must stand down for a foreign-origin target, got %d promotions", j.Promoted)
	}

	after := f.onlySkill(t)
	if after.ID != existing.ID {
		t.Fatalf("promotion wrote a second row: %q -> %q", existing.ID, after.ID)
	}
	if after.Version != existing.Version {
		t.Errorf("skipped promotion bumped the version %d -> %d", existing.Version, after.Version)
	}
	if after.Procedure != existing.Procedure {
		t.Errorf("skipped promotion rewrote the body:\n%s", after.Procedure)
	}
	if got := originTagOf(t, after); got != foreign {
		t.Errorf("origin tag = %q, want the pre-existing %q", got, foreign)
	}
	if tagPresent(after.Tags, originPrefix+groupHash("default", "api", "oauth")) {
		t.Errorf("skipped promotion stamped this group's origin tag: %v", after.Tags)
	}
	if len(after.Tags) != len(existing.Tags) {
		t.Errorf("skipped promotion changed tags: %v -> %v", existing.Tags, after.Tags)
	}

	// Standing down is stable: the next pass skips again rather than promoting late.
	if _, err := runFencedTestPass(t, ctx, f.ds, f.db, false); err != nil {
		t.Fatal(err)
	}
	stable := f.onlySkill(t)
	if stable.Version != existing.Version || len(stable.Tags) != len(existing.Tags) {
		t.Errorf("rerun changed the foreign-origin row: v%d %v", stable.Version, stable.Tags)
	}
}

// TestPromotionKeepsMergedSections is the c3#2 guarantee: promotion rewrites a
// skill body with its deterministic synthesis, but the sections a skills.Merge
// absorbed into that body belong to another skill, so they survive the rewrite.
// Hand-written prose is still replaced — that documented trade-off is unchanged.
func TestPreservePromotedDescription(t *testing.T) {
	generated := "Auto-promoted from 4 corrections in api: oauth"
	existing := "Auto-promoted from 3 corrections in api: oauth\n\nabsorbed description"
	got := preservePromotedDescription(generated, existing)
	if !strings.Contains(got, "absorbed description") {
		t.Fatalf("lost merged description: %q", got)
	}
	if again := preservePromotedDescription(generated, got); again != got {
		t.Fatalf("description changed on repeat: %q -> %q", got, again)
	}
}

func TestPromotionKeepsMergedSections(t *testing.T) {
	f := newPromoteFixture(t)
	ctx := context.Background()
	name := promotedSkillName(correctionGroup{project: "api", label: "oauth"})

	save := func(in skills.SaveInput, versions int) *skills.Skill {
		var sk *skills.Skill
		for i := 0; i < versions; i++ {
			var err error
			if sk, err = f.sk.Save(ctx, in); err != nil {
				t.Fatalf("save %q: %v", in.Name, err)
			}
		}
		return sk
	}

	// The keeper is hand-written and at v2 so it outranks the duplicate it will
	// absorb. It carries the project tag the promotion lookup matches on.
	keeper := save(skills.SaveInput{
		AgentID:     "default",
		Name:        name,
		Description: "hand-written runbook",
		Procedure:   "hand step: refresh the token first",
		Pitfalls:    "hand pitfall: never retry a 401",
		Tags:        []string{"project:api", "runbook"},
	}, 2)
	// A same-name skill can only exist under another agent, because (agent_id,
	// name) is unique — that is the duplicate shape a merge folds together.
	loser := save(skills.SaveInput{
		AgentID:     "other",
		Name:        name,
		Description: "absorbed runbook",
		Procedure:   "absorbed step: rotate the secret",
		Pitfalls:    "absorbed pitfall",
		Tags:        []string{"project:api", "team:other"},
	}, 1)

	merged, err := f.sk.Merge(ctx, skills.MergeInput{Name: name})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged.Keeper == nil || merged.Keeper.ID != keeper.ID {
		t.Fatalf("merge keeper = %v, want %q", merged.Keeper, keeper.ID)
	}
	section := fmt.Sprintf("%s other v%d", skills.MergeSectionHeader, loser.Version)
	if !strings.Contains(merged.Keeper.Procedure, section) {
		t.Fatalf("setup: keeper missing the merge section %q:\n%s", section, merged.Keeper.Procedure)
	}

	seedCorrectionGroup(t, f, "api", "oauth", "oauth retry")
	j, err := runFencedTestPass(t, ctx, f.ds, f.db, false)
	if err != nil {
		t.Fatal(err)
	}
	if j.Promoted != 1 {
		t.Fatalf("want 1 promotion into the merged row, got %d", j.Promoted)
	}

	promoted := f.onlySkill(t)
	if promoted.ID != keeper.ID {
		t.Errorf("promotion wrote a new row: %q -> %q", keeper.ID, promoted.ID)
	}
	for _, want := range []string{"## Avoid", section, "absorbed step: rotate the secret"} {
		if !strings.Contains(promoted.Procedure, want) {
			t.Errorf("promoted procedure missing %q:\n%s", want, promoted.Procedure)
		}
	}
	if !strings.Contains(promoted.Procedure, "hand step: refresh the token first") {
		t.Errorf("hand-written prose lost during promotion:\n%s", promoted.Procedure)
	}
	if !strings.Contains(promoted.Description, "hand-written runbook") || !strings.Contains(promoted.Description, "absorbed runbook") {
		t.Errorf("merged descriptions lost during promotion: %s", promoted.Description)
	}
	if !strings.Contains(promoted.Pitfalls, "hand pitfall: never retry a 401") {
		t.Errorf("hand-written pitfalls lost during promotion: %s", promoted.Pitfalls)
	}
	for _, want := range []string{section, "absorbed pitfall"} {
		if !strings.Contains(promoted.Pitfalls, want) {
			t.Errorf("promoted pitfalls missing %q:\n%s", want, promoted.Pitfalls)
		}
	}
	for _, want := range []string{tagPromoted, "project:api", "runbook", "team:other"} {
		if !tagPresent(promoted.Tags, want) {
			t.Errorf("promoted tags %v missing %q", promoted.Tags, want)
		}
	}

	// The preserved tail is part of the composed body, so the unchanged set is
	// still a no-op rather than a version treadmill.
	before := promoted.Version
	if _, err := runFencedTestPass(t, ctx, f.ds, f.db, false); err != nil {
		t.Fatal(err)
	}
	stable := f.onlySkill(t)
	if stable.Version != before {
		t.Errorf("rerun bumped the merged skill %d -> %d", before, stable.Version)
	}
	if !strings.Contains(stable.Procedure, section) {
		t.Errorf("rerun dropped the merged section:\n%s", stable.Procedure)
	}
}

// tagPresent reports whether tags contains want.
func tagPresent(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}

// countTags counts occurrences of want in tags.
func countTags(tags []string, want string) int {
	n := 0
	for _, tag := range tags {
		if tag == want {
			n++
		}
	}
	return n
}

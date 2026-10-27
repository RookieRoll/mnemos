package dream

import (
	"context"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/skills"
)

func TestMergedAgentStillUpdatesFromNewCorrections(t *testing.T) {
	f := newPromoteFixture(t)
	ctx := context.Background()
	for _, agent := range []string{"default", "other"} {
		for _, title := range []string{"first", "second", "third"} {
			f.seedCorrectionForAgent(t, agent, "api", "oauth", title+" "+agent)
		}
	}
	if _, err := runFencedTestPass(t, ctx, f.ds, f.db, false); err != nil {
		t.Fatal(err)
	}
	merged, err := f.sk.Merge(ctx, skills.MergeInput{Name: promotedSkillName(correctionGroup{project: "api", label: "oauth"})})
	if err != nil {
		t.Fatal(err)
	}
	loser := "default"
	if merged.Keeper.AgentID == loser {
		loser = "other"
	}
	f.seedCorrectionForAgent(t, loser, "api", "oauth", "fourth feedback after merge")
	if _, err := runFencedTestPass(t, ctx, f.ds, f.db, false); err != nil {
		t.Fatal(err)
	}
	after := f.onlySkill(t)
	if !strings.Contains(after.Procedure, "fix for fourth feedback after merge") {
		t.Fatal("new correction from absorbed agent never reaches the merged skill")
	}
	if after.ID != merged.Keeper.ID || after.AgentID != merged.Keeper.AgentID {
		t.Fatal("refresh changed keeper identity")
	}
	if _, err := runFencedTestPass(t, ctx, f.ds, f.db, false); err != nil {
		t.Fatal(err)
	}
	stable := f.onlySkill(t)
	if stable.Version != after.Version || stable.Procedure != after.Procedure {
		t.Fatal("unchanged merged groups were not idempotent")
	}
}

func TestAbsorbedPromotionRetainsOtherSections(t *testing.T) {
	g := correctionGroup{agentID: "other", hash: "stable-origin"}
	body := "keeper procedure\n\n## Merged from other v2\n\nold generated body\n\n## Merged from manual skill\n\nmanual runbook\n\n## Merged from third v1\n\nthird procedure"
	got := refreshAbsorbedSection(body, g, "new generated body")
	for _, keep := range []string{"keeper procedure", "manual runbook", "third procedure", "new generated body"} {
		if !strings.Contains(got, keep) {
			t.Fatalf("lost %q: %s", keep, got)
		}
	}
	if strings.Contains(got, "old generated body") {
		t.Fatal("kept stale generated text")
	}
	if again := refreshAbsorbedSection(got, g, "new generated body"); again != got {
		t.Fatal("same group appended twice")
	}
}

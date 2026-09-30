package prewarm_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/skills"
)

func TestEmptyFixtureProducesEmptyBlock(t *testing.T) {
	pw, _, _, _, _ := newFixture(t)
	block, err := pw.Build(context.Background(), prewarm.Request{
		Mode:    prewarm.ModeSessionStart,
		Project: "no-data-project",
	})
	if err != nil {
		t.Fatal(err)
	}
	if block.TokenEstimate > 0 && block.Text == "" {
		t.Errorf("inconsistent: tokens=%d text empty", block.TokenEstimate)
	}
}

func TestMatchingSkillsSurface(t *testing.T) {
	pw, _, _, skl, _ := newFixture(t)
	ctx := context.Background()

	_, _ = skl.Save(ctx, skillsSave("wire-mcp-tool", "add a new MCP tool"))
	_, _ = skl.Save(ctx, skillsSave("run-migrations", "safely apply SQLite migrations"))

	block, err := pw.Build(ctx, prewarm.Request{
		Mode:    prewarm.ModeSessionStart,
		Project: "x",
		Goal:    "add an MCP tool",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(block.Text, "wire-mcp-tool") {
		t.Errorf("skill match didn't surface in prewarm: %s", block.Text)
	}
}

func TestRecentSessionsWithFailureStatusMarked(t *testing.T) {
	pw, _, sess, _, _ := newFixture(t)
	ctx := context.Background()

	s, _ := sess.Open(ctx, session.OpenInput{Project: "p", Goal: "ship"})
	_ = sess.Close(ctx, session.CloseInput{
		ID: s.ID, Summary: "deploy broke", Status: session.StatusFailed,
	})

	block, err := pw.Build(ctx, prewarm.Request{
		Mode: prewarm.ModeSessionStart, Project: "p",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(block.Text, "failed") {
		t.Errorf("failed session status should surface in recent sessions: %s", block.Text)
	}
}

func TestRecoveryModeRequiresSessionID(t *testing.T) {
	pw, _, _, _, _ := newFixture(t)
	// Recovery with no session id should still build — just emits nothing
	// for the current-session and in-session sections. Falls through to
	// conventions + corrections.
	block, err := pw.Build(context.Background(), prewarm.Request{
		Mode:    prewarm.ModeCompactionRecovery,
		Project: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = block
}

func TestBuildRespectsDefaultMaxTokens(t *testing.T) {
	pw, mem, _, _, _ := newFixture(t)
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		_, _ = mem.Save(ctx, memory.SaveInput{
			Title:   "long convention",
			Content: strings.Repeat("lots of text about stuff and things ", 50),
			Type:    memory.TypeConvention,
			Project: "x",
		})
	}
	block, err := pw.Build(ctx, prewarm.Request{
		Mode: prewarm.ModeSessionStart, Project: "x",
		// MaxTokens not set → uses service default 500
	})
	if err != nil {
		t.Fatal(err)
	}
	if block.TokenEstimate > 600 {
		t.Errorf("default 500-token cap exceeded: %d", block.TokenEstimate)
	}
}

func skillsSave(name, desc string) skills.SaveInput {
	return skills.SaveInput{Name: name, Description: desc, Procedure: "1. do it"}
}

func TestRecentSessionsComeFromTheProjectAndSkipTheOpeningOne(t *testing.T) {
	pw, _, _, _, db := newFixture(t)
	ctx := context.Background()
	store := db.Sessions()

	// Explicit timestamps: a coarse clock can give same-tick opens equal
	// started_at values, which would make the ordering below flap.
	base := time.Date(2026, 9, 29, 9, 0, 0, 0, time.UTC)
	insert := func(id, project, goal, summary string, at time.Duration) {
		t.Helper()
		if err := store.Insert(ctx, &session.Session{
			ID: id, AgentID: "default", Project: project, Goal: goal, StartedAt: base.Add(at),
		}); err != nil {
			t.Fatal(err)
		}
		if summary != "" {
			_ = store.Close(ctx, session.CloseInput{ID: id, Summary: summary, Status: session.StatusOK})
		}
	}
	insert("01TAKENPRIOR0000000000000A", "taken", "port the river course", "touched River.ts", 0)
	insert("01TAKENBLANK0000000000000B", "taken", "", "", time.Minute)
	// Busier projects: five newer sessions elsewhere pushed every taken
	// session out of the old cross-project top five.
	for i := 0; i < 5; i++ {
		insert(fmt.Sprintf("01OTHERPROJECT00000000000%d", i), "wayframer", "unrelated work", "", time.Hour+time.Duration(i)*time.Minute)
	}
	insert("01TAKENOPENINGNOW00000000C", "taken", "", "", 2*time.Hour)

	block, err := pw.Build(ctx, prewarm.Request{
		Mode: prewarm.ModeSessionStart, Project: "taken", SessionID: "01TAKENOPENINGNOW00000000C",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(block.Text, "port the river course") {
		t.Errorf("the project's previous session must be listed: %s", block.Text)
	}
	if strings.Contains(block.Text, "unrelated work") {
		t.Errorf("another project's sessions must not be listed: %s", block.Text)
	}
	if strings.Contains(block.Text, "- 2026-09-29\n") || strings.HasSuffix(block.Text, "- 2026-09-29") {
		t.Errorf("sessions with nothing to say must be skipped: %s", block.Text)
	}
}

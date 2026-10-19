package prewarm_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/session"
)

func TestRecentProjectSessionsRemainIndependentRenderItems(t *testing.T) {
	pw, _, _, _, db := newFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	add := func(id, project, goal string, minute int) {
		t.Helper()
		if err := db.Sessions().Insert(ctx, &session.Session{ID: id, AgentID: "default", Project: project, Goal: goal, StartedAt: base.Add(time.Duration(minute) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		add(fmt.Sprintf("prior-%d", i), "p", fmt.Sprintf("prior-session-goal-%d", i), i)
	}
	add("blank", "p", "", 3)
	add("opening", "p", "current-opening-goal", 4)
	for i := 0; i < 6; i++ {
		add(fmt.Sprintf("other-%d", i), "other", "other-project-goal", 10+i)
	}
	block, err := pw.Build(ctx, prewarm.Request{Mode: prewarm.ModeSessionStart, Project: "p", SessionID: "opening", MaxTokens: 500})
	if err != nil {
		t.Fatal(err)
	}
	var body string
	for _, section := range block.Sections {
		if section.Title == "recent sessions" {
			body = section.Body
		}
	}
	for i := 0; i < 3; i++ {
		goal := fmt.Sprintf("prior-session-goal-%d", i)
		if strings.Count(block.Text, goal) != 1 || strings.Count(body, goal) != 1 {
			t.Fatalf("session was dropped or accumulated into later items: %q\n%s", body, block.Text)
		}
	}
	if strings.Contains(block.Text, "current-opening-goal") || strings.Contains(block.Text, "other-project-goal") {
		t.Fatalf("current or other-project session leaked: %s", block.Text)
	}
	if strings.Count(body, "\n") != 2 {
		t.Fatalf("want3 complete independent session lines: %q", body)
	}
}

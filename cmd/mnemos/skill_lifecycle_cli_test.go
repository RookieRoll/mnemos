package main

import (
	"bytes"
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/skills"
)

// seedSkill saves name for an agent, optionally tag-scoped to a project, and
// bumps it to the requested version by saving it repeatedly.
func seedSkill(t *testing.T, ctx context.Context, agent, name, project string, version int) {
	t.Helper()
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	defer d.close()
	var tags []string
	if project != "" {
		tags = append(tags, "project:"+project)
	}
	for i := 0; i < version; i++ {
		if _, err := d.skl.Save(ctx, skills.SaveInput{
			AgentID: agent, Name: name,
			Description: "procedure for " + name,
			Procedure:   "1. do the thing",
			Tags:        tags,
		}); err != nil {
			t.Fatalf("save %s/%s: %v", agent, name, err)
		}
	}
}

// selectSkills returns the current rows for a name.
func selectSkills(t *testing.T, ctx context.Context, name string) []skills.Skill {
	t.Helper()
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	defer d.close()
	rows, err := d.skl.Select(ctx, name, skills.Scope{})
	if err != nil {
		t.Fatalf("select %s: %v", name, err)
	}
	return rows
}

func skillVersionByAgent(rows []skills.Skill) map[string]int {
	out := make(map[string]int, len(rows))
	for _, sk := range rows {
		out[sk.AgentID] = sk.Version
	}
	return out
}

func TestSkillDeleteRemovesMatchingRows(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantLeft  []string
		wantCount string
	}{
		{
			name:      "delete one agent",
			args:      []string{"delete", "deploy-checklist", "--agent", "one"},
			wantLeft:  []string{"three", "two"},
			wantCount: "deleted 1 skill(s)",
		},
		{
			name:      "delete by project tag",
			args:      []string{"delete", "deploy-checklist", "--project", "alpha"},
			wantLeft:  []string{"two"},
			wantCount: "deleted 2 skill(s)",
		},
		{
			name:      "no match",
			args:      []string{"delete", "missing"},
			wantLeft:  []string{"one", "three", "two"},
			wantCount: "deleted 0 skill(s)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			homeWithConfig(t, "")
			ctx := context.Background()
			seedSkill(t, ctx, "one", "deploy-checklist", "alpha", 1)
			seedSkill(t, ctx, "two", "deploy-checklist", "beta", 1)
			seedSkill(t, ctx, "three", "deploy-checklist", "alpha", 1)

			out := captureStdout(t, func() {
				if err := runSkill(ctx, tc.args); err != nil {
					t.Fatalf("skill %v: %v", tc.args, err)
				}
			})
			if !strings.Contains(out, tc.wantCount) {
				t.Fatalf("report missing %q: %q", tc.wantCount, out)
			}
			rows := selectSkills(t, ctx, "deploy-checklist")
			got := make([]string, 0, len(rows))
			for _, sk := range rows {
				got = append(got, sk.AgentID)
			}
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(tc.wantLeft, ",") {
				t.Fatalf("remaining agents = %v, want %v", got, tc.wantLeft)
			}
		})
	}
}

func TestSkillMergeKeepsHighestVersion(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()
	seedSkillV(t, ctx, "one", "deploy-checklist", 3)
	seedSkillV(t, ctx, "two", "deploy-checklist", 2)

	out := captureStdout(t, func() {
		if err := runSkill(ctx, []string{"merge", "deploy-checklist"}); err != nil {
			t.Fatalf("skill merge: %v", err)
		}
	})
	if !strings.Contains(out, "merged 1 skill(s)") {
		t.Fatalf("merge report: %q", out)
	}
	out = captureStdout(t, func() {
		if err := runSkill(ctx, []string{"list"}); err != nil {
			t.Fatalf("skill list: %v", err)
		}
	})
	rows := selectSkills(t, ctx, "deploy-checklist")
	if len(rows) != 1 {
		t.Fatalf("after merge rows = %d, want 1: %+v", len(rows), rows)
	}
	if rows[0].AgentID != "one" {
		t.Fatalf("keeper agent = %q, want one (highest version)", rows[0].AgentID)
	}
	if rows[0].Version != 3 {
		t.Fatalf("keeper version = %d, want 3 for identical content", rows[0].Version)
	}
}

func TestSkillMergeKeepsExplicitVersion(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()
	seedSkillV(t, ctx, "one", "deploy-checklist", 3)
	seedSkillV(t, ctx, "two", "deploy-checklist", 2)

	if err := runSkill(ctx, []string{"merge", "deploy-checklist", "--keep-version", "2"}); err != nil {
		t.Fatalf("skill merge --keep-version 2: %v", err)
	}
	rows := selectSkills(t, ctx, "deploy-checklist")
	if len(rows) != 1 || rows[0].AgentID != "two" {
		t.Fatalf("keeper = %+v, want agent two at version 2 bumped", rows)
	}
}

func TestSkillDeleteMergeDryRunWritesNothing(t *testing.T) {
	cases := [][]string{
		{"delete", "deploy-checklist", "--dry-run"},
		{"merge", "deploy-checklist", "--dry-run"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			homeWithConfig(t, "")
			ctx := context.Background()
			seedSkillV(t, ctx, "one", "deploy-checklist", 3)
			seedSkillV(t, ctx, "two", "deploy-checklist", 2)
			before := skillVersionByAgent(selectSkills(t, ctx, "deploy-checklist"))

			out := captureStdout(t, func() {
				if err := runSkill(ctx, args); err != nil {
					t.Fatalf("skill %v: %v", args, err)
				}
			})
			if !strings.Contains(out, "would ") {
				t.Fatalf("dry run must say what it would do: %q", out)
			}
			after := skillVersionByAgent(selectSkills(t, ctx, "deploy-checklist"))
			if len(before) != len(after) {
				t.Fatalf("dry run changed the rows: %v -> %v", before, after)
			}
			for agent, version := range before {
				if after[agent] != version {
					t.Fatalf("dry run versioned %s: %d -> %d", agent, version, after[agent])
				}
			}
		})
	}
}

func TestSkillMergeRejectsNegativeKeepVersion(t *testing.T) {
	homeWithConfig(t, "")
	var stdout, stderr bytes.Buffer
	code := dispatch(context.Background(), []string{"skill", "merge", "x", "--keep-version", "-1"}, &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit = %d, want %d (stderr %q)", code, exitUsage, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--keep-version must not be negative") {
		t.Fatalf("stderr must explain the flag: %q", stderr.String())
	}
}

// seedSkillV is seedSkill without the project tag.
func seedSkillV(t *testing.T, ctx context.Context, agent, name string, version int) {
	t.Helper()
	seedSkill(t, ctx, agent, name, "", version)
}

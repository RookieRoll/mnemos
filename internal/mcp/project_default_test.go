package mcp_test

import (
	"encoding/json"
	"testing"

	"github.com/polyxmedia/mnemos/internal/mcp"
)

// savedProject saves through the named tool and reads the stored project
// back through mnemos_get.
func savedProject(t *testing.T, h *harness, tool string, args map[string]any) string {
	t.Helper()
	res, text := h.call(tool, args)
	if res.IsError {
		t.Fatalf("%s: %s", tool, text)
	}
	var saved struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(text), &saved); err != nil || saved.ID == "" {
		t.Fatalf("%s: no id in %q (%v)", tool, text, err)
	}
	res, text = h.call("mnemos_get", map[string]any{"id": saved.ID})
	if res.IsError {
		t.Fatalf("mnemos_get: %s", text)
	}
	var obs struct {
		Project string `json:"Project"`
	}
	if err := json.Unmarshal([]byte(text), &obs); err != nil {
		t.Fatalf("decode observation %q: %v", text, err)
	}
	return obs.Project
}

func TestWritesDefaultToTheServersProject(t *testing.T) {
	h := newHarnessWith(t, func(c *mcp.Config) { c.DefaultProject = "taken" })

	cases := []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{"save without project", "mnemos_save", map[string]any{
			"title": "terrain sampler budget", "content": "16 texture units", "type": "decision",
		}, "taken"},
		{"correct without project", "mnemos_correct", map[string]any{
			"title": "reuse across contexts", "tried": "reused the spiral road",
			"wrong_because": "built for another purpose", "fix": "build for the seam",
		}, "taken"},
		{"convention without project", "mnemos_convention", map[string]any{
			"title": "no new samplers", "rule": "terrain stays at 16 units",
		}, "taken"},
		{"explicit project wins", "mnemos_save", map[string]any{
			"title": "billing", "content": "actions disabled", "type": "context", "project": "wayframer",
		}, "wayframer"},
		{"star saves a global memory", "mnemos_save", map[string]any{
			"title": "ad pricing", "content": "£150-£250 a month", "type": "decision", "project": mcp.GlobalProject,
		}, ""},
	}
	for _, tc := range cases {
		if got := savedProject(t, h, tc.tool, tc.args); got != tc.want {
			t.Errorf("%s: project = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestWritesStayProjectlessWithoutADefault(t *testing.T) {
	h := newHarness(t)
	got := savedProject(t, h, "mnemos_save", map[string]any{
		"title": "no cwd", "content": "started from /", "type": "context",
	})
	if got != "" {
		t.Errorf("with no default project a write must stay project-less, got %q", got)
	}
}

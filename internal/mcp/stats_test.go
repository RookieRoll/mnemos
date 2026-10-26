package mcp_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/polyxmedia/mnemos/internal/mcp"
	"github.com/polyxmedia/mnemos/internal/session"
)

// TestMCPSurfaces_StatsAgree pins the storage-runtime requirement that the
// mnemos://stats resource and the mnemos_stats tool render one typed value
// through one formatter: for a single fixture both documents must be identical,
// down to the byte.
func TestMCPSurfaces_StatsAgree(t *testing.T) {
	h := newHarness(t)
	// One fixture both surfaces must describe: an open session (so
	// recent_sessions is populated), an observation, and a database file on
	// disk to size.
	h.call("mnemos_session_start", map[string]any{"project": "mnemos", "goal": "stats surfaces agree"})
	h.call("mnemos_save", map[string]any{
		"title":      "wal counts toward storage",
		"content":    "the -wal sidecar is counted, the -shm is not",
		"type":       "pattern",
		"tags":       []string{"sqlite"},
		"importance": 7,
	})

	_, toolText := h.call("mnemos_stats", map[string]any{})
	read, err := h.client.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: "mnemos://stats"})
	if err != nil {
		t.Fatalf("read stats resource: %v", err)
	}
	if len(read.Contents) == 0 {
		t.Fatal("stats resource returned no content")
	}
	resourceText := read.Contents[0].Text

	if toolText != resourceText {
		t.Fatalf("stats surfaces disagree:\n tool:     %s\n resource: %s", toolText, resourceText)
	}

	var toolOut, resourceOut map[string]any
	if err := json.Unmarshal([]byte(toolText), &toolOut); err != nil {
		t.Fatalf("decode tool stats: %v", err)
	}
	if err := json.Unmarshal([]byte(resourceText), &resourceOut); err != nil {
		t.Fatalf("decode resource stats: %v", err)
	}
	if !reflect.DeepEqual(toolOut, resourceOut) {
		t.Fatalf("decoded stats differ:\n tool:     %v\n resource: %v", toolOut, resourceOut)
	}

	size, ok := toolOut["storage_bytes"].(float64)
	if !ok {
		t.Fatalf("stats missing storage_bytes: %v", toolOut)
	}
	if size <= 0 {
		t.Fatalf("storage_bytes = %v, want > 0 for an on-disk fixture", size)
	}
	sessions, ok := toolOut["sessions"].(float64)
	if !ok || sessions < 1 {
		t.Fatalf("sessions = %v, want at least the fixture's open session", toolOut["sessions"])
	}
}

// TestMCPSurfaces_StatsOpenSessions pins the open-session backlog requirement:
// both statistics surfaces report the number of open sessions and when the
// oldest one started, both describe the value session.Service.OpenBacklog
// returns (the single read behind the CLI's "open sessions: N (oldest Xd)"
// line), and the two surfaces agree on every rendered byte, backlog included.
func TestMCPSurfaces_StatsOpenSessions(t *testing.T) {
	cases := []struct {
		name string
		open int
	}{
		{name: "empty backlog", open: 0},
		{name: "one open session", open: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sess *session.Service
			h := newHarnessWith(t, func(cfg *mcp.Config) { sess = cfg.Sessions })
			if sess == nil {
				t.Fatal("harness must wire a session service: the backlog has exactly one source")
			}
			for i := 0; i < tc.open; i++ {
				h.call("mnemos_session_start", map[string]any{"project": "mnemos", "goal": "backlog fixture"})
			}
			want, err := sess.OpenBacklog(context.Background())
			if err != nil {
				t.Fatalf("open backlog: %v", err)
			}
			if want.Count != tc.open {
				t.Fatalf("fixture has %d open session(s), session service reports %d", tc.open, want.Count)
			}

			_, toolText := h.call("mnemos_stats", map[string]any{})
			read, err := h.client.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: "mnemos://stats"})
			if err != nil {
				t.Fatalf("read stats resource: %v", err)
			}
			if len(read.Contents) == 0 {
				t.Fatal("stats resource returned no content")
			}
			resourceText := read.Contents[0].Text
			if toolText != resourceText {
				t.Fatalf("stats surfaces disagree on the backlog:\n tool:     %s\n resource: %s", toolText, resourceText)
			}

			for _, surface := range []struct{ name, text string }{
				{name: "tool", text: toolText},
				{name: "resource", text: resourceText},
			} {
				var out map[string]any
				if err := json.Unmarshal([]byte(surface.text), &out); err != nil {
					t.Fatalf("%s: decode stats: %v", surface.name, err)
				}
				section, ok := out["open_sessions"].(map[string]any)
				if !ok {
					t.Fatalf("%s: stats missing the open_sessions section: %v", surface.name, out)
				}
				count, ok := section["count"].(float64)
				if !ok || int(count) != want.Count {
					t.Errorf("%s: open_sessions.count = %v, want %d", surface.name, section["count"], want.Count)
				}
				raw, present := section["oldest_started_at"]
				if !present {
					t.Fatalf("%s: open_sessions missing oldest_started_at", surface.name)
				}
				if want.OldestStartedAt.IsZero() {
					if raw != nil {
						t.Errorf("%s: oldest_started_at = %v, want null with no open session", surface.name, raw)
					}
					continue
				}
				stamp, ok := raw.(string)
				if !ok {
					t.Fatalf("%s: oldest_started_at must render as an instant, got %T", surface.name, raw)
				}
				got, err := time.Parse(time.RFC3339Nano, stamp)
				if err != nil {
					t.Fatalf("%s: parse oldest_started_at %q: %v", surface.name, stamp, err)
				}
				if !got.Equal(want.OldestStartedAt) {
					t.Errorf("%s: oldest_started_at = %v, want %v", surface.name, got, want.OldestStartedAt)
				}
			}
		})
	}
}

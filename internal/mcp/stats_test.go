package mcp_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
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

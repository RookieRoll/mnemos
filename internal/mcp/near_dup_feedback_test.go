package mcp_test

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSaveReturnsNearDuplicateFeedback(t *testing.T) {
	for _, outside := range []bool{false, true} {
		name := "merge"
		if outside {
			name = "outside window hint"
		}
		t.Run(name, func(t *testing.T) {
			h, db, _ := newSessionEndHarness(t, nil)
			_, raw := h.call("mnemos_save", map[string]any{"title": "first", "content": "Enable WAL", "type": "context", "project": "p"})
			var first map[string]any
			if err := json.Unmarshal([]byte(raw), &first); err != nil {
				t.Fatal(err)
			}
			if outside {
				old := time.Now().UTC().Add(-48 * time.Hour)
				if _, err := db.SQL().Exec(`UPDATE observations SET created_at=?,valid_from=?,last_accessed_at=? WHERE id=?`, old, old, old, first["id"]); err != nil {
					t.Fatal(err)
				}
			}
			_, raw = h.call("mnemos_save", map[string]any{"title": "second", "content": "Enable WAL!", "type": "context", "project": "p"})
			var result map[string]any
			if err := json.Unmarshal([]byte(raw), &result); err != nil {
				t.Fatal(err)
			}
			if outside {
				if result["near_dup_hint"] != first["id"] || result["deduped"] != false {
					t.Fatalf("missing hint: %s", raw)
				}
			} else {
				merge, ok := result["near_dup"].(map[string]any)
				if !ok || merge["survivor_id"] != first["id"] || merge["reason"] != "content_hash" {
					t.Fatalf("missing merge feedback: %s", raw)
				}
			}
		})
	}
}

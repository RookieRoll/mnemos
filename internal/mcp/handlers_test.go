package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Exercises the handler paths the earlier suites didn't hit.

func TestDeleteWithUnknownIDReturnsError(t *testing.T) {
	h := newHarness(t)
	res, err := h.client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "mnemos_delete",
		Arguments: map[string]any{"id": "nonexistent"},
	})
	if err == nil && !res.IsError {
		t.Error("expected delete to fail on unknown id")
	}
}

func TestSessionEndClosesWithSummary(t *testing.T) {
	h := newHarness(t)
	_, startText := h.call("mnemos_session_start", map[string]any{
		"project": "p", "goal": "g",
	})
	var started map[string]any
	_ = json.Unmarshal([]byte(startText), &started)
	sessID := started["session_id"].(string)

	_, text := h.call("mnemos_session_end", map[string]any{
		"session_id":   sessID,
		"summary":      "done",
		"reflection":   "learned",
		"status":       "ok",
		"outcome_tags": []string{"shipped"},
	})
	if !strings.Contains(text, "closed (ok)") {
		t.Errorf("session end should confirm closure with status: %s", text)
	}
}

func TestSkillSaveAndMatchViaMCP(t *testing.T) {
	h := newHarness(t)
	_, saveText := h.call("mnemos_skill_save", map[string]any{
		"name":        "wire-mcp-tool",
		"description": "add an MCP tool",
		"procedure":   "1. define\n2. register\n3. test",
		"tags":        []string{"mcp"},
	})
	var saved map[string]any
	_ = json.Unmarshal([]byte(saveText), &saved)
	if saved["id"] == nil || saved["version"] == nil {
		t.Errorf("skill save should return id+version: %v", saved)
	}

	_, matchText := h.call("mnemos_skill_match", map[string]any{
		"query": "mcp tool",
	})
	if !strings.Contains(matchText, "wire-mcp-tool") {
		t.Errorf("skill match should find saved skill: %s", matchText)
	}
}

func TestLinkUnknownTypeRejected(t *testing.T) {
	h := newHarness(t)

	// Save two observations to link.
	_, t1 := h.call("mnemos_save", map[string]any{
		"title": "a", "content": "a", "type": "context",
	})
	var o1 map[string]any
	_ = json.Unmarshal([]byte(t1), &o1)

	_, t2 := h.call("mnemos_save", map[string]any{
		"title": "b", "content": "b", "type": "context",
	})
	var o2 map[string]any
	_ = json.Unmarshal([]byte(t2), &o2)

	res, err := h.client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "mnemos_link",
		Arguments: map[string]any{
			"source_id": o1["id"], "target_id": o2["id"],
			"link_type": "bogus",
		},
	})
	if err == nil && !res.IsError {
		t.Error("bogus link type should fail")
	}
}

func TestContextWithoutQueryOrRecoveryErrors(t *testing.T) {
	h := newHarness(t)
	res, err := h.client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "mnemos_context",
		Arguments: map[string]any{}, // neither mode nor query
	})
	if err == nil && !res.IsError {
		t.Error("context with no query/mode should fail")
	}
}

func TestRecoveryRequiresSessionID(t *testing.T) {
	h := newHarness(t)
	res, err := h.client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "mnemos_context",
		Arguments: map[string]any{
			"mode": "recovery", // missing session_id
		},
	})
	if err == nil && !res.IsError {
		t.Error("recovery without session_id should fail")
	}
}

func TestTouchWithoutTouchesStore(t *testing.T) {
	// Not directly testable without rewiring the harness; skip.
	t.Skip("touches store is always wired in harness; covered by TestTouchAppearsInStats")
}

func TestSaveWithPinnedEchoesFlag(t *testing.T) {
	h := newHarness(t)
	_, saveText := h.call("mnemos_save", map[string]any{
		"title":   "core architecture constraint",
		"content": "zero cgo binaries always",
		"type":    "convention",
		"pinned":  true,
	})
	var saved map[string]any
	if err := json.Unmarshal([]byte(saveText), &saved); err != nil {
		t.Fatalf("unmarshal save: %v", err)
	}
	if saved["pinned"] != true {
		t.Errorf("mnemos_save should echo pinned: true, got %v", saved["pinned"])
	}

	id := saved["id"].(string)
	_, getText := h.call("mnemos_get", map[string]any{"id": id})
	var got map[string]any
	if err := json.Unmarshal([]byte(getText), &got); err != nil {
		t.Fatalf("unmarshal get: %v", err)
	}
	// jsonResult(o) serializes struct fields
	pinnedVal, ok := got["Pinned"]
	if !ok {
		pinnedVal = got["pinned"]
	}
	if pinnedVal != true {
		t.Errorf("mnemos_get should preserve pinned=true: %v", got)
	}
}

func TestPinToolLifecycle(t *testing.T) {
	h := newHarness(t)
	_, saveText := h.call("mnemos_save", map[string]any{
		"title":   "ephemeral note",
		"content": "note content",
		"type":    "context",
	})
	var saved map[string]any
	_ = json.Unmarshal([]byte(saveText), &saved)
	id := saved["id"].(string)

	// 1. Pin observation
	_, pinText := h.call("mnemos_pin", map[string]any{"id": id, "pinned": true})
	var pinned map[string]any
	if err := json.Unmarshal([]byte(pinText), &pinned); err != nil {
		t.Fatalf("unmarshal pin: %v", err)
	}
	if pinned["pinned"] != true {
		t.Errorf("pin tool should report pinned: true, got %v", pinned["pinned"])
	}

	// 2. Unpin observation
	_, unpinText := h.call("mnemos_pin", map[string]any{"id": id, "pinned": false})
	var unpinned map[string]any
	if err := json.Unmarshal([]byte(unpinText), &unpinned); err != nil {
		t.Fatalf("unmarshal unpin: %v", err)
	}
	if unpinned["pinned"] != false {
		t.Errorf("pin tool should report pinned: false, got %v", unpinned["pinned"])
	}

	// 3. Pin nonexistent observation returns error
	res, err := h.client.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "mnemos_pin",
		Arguments: map[string]any{"id": "nonexistent", "pinned": true},
	})
	if err == nil && !res.IsError {
		t.Error("expected pin on unknown id to fail")
	}
}

func TestSearchEchoesGraphAndContradictionFields(t *testing.T) {
	h := newHarness(t)
	h.call("mnemos_save", map[string]any{
		"title":   "clean search observation",
		"content": "observation content for search verification",
		"type":    "pattern",
	})
	_, searchText := h.call("mnemos_search", map[string]any{"query": "search observation"})
	var searchOut map[string]any
	if err := json.Unmarshal([]byte(searchText), &searchOut); err != nil {
		t.Fatalf("unmarshal search: %v", err)
	}
	results := searchOut["results"].([]any)
	if len(results) == 0 {
		t.Fatal("expected at least one search result")
	}
	first := results[0].(map[string]any)
	for _, field := range []string{"pinned", "expanded_from", "contradicted_by", "graph_factor", "contradiction_factor"} {
		if _, exists := first[field]; !exists {
			t.Errorf("search result missing field %q: %+v", field, first)
		}
	}
}

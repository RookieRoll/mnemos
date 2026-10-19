package mcp_test

import (
	"encoding/json"
	"testing"

	"github.com/polyxmedia/mnemos/internal/mcp"
)

func TestDefaultProjectKeepsExplicitMemoryEvolution(t *testing.T) {
	h := newHarnessWith(t, func(c *mcp.Config) { c.DefaultProject = "taken" })
	res, text := h.call("mnemos_save", map[string]any{"title": "transaction rule", "content": "use a transaction for multiple writes", "type": "convention", "topic_key": "database/transactions", "pinned": true, "evidence": []map[string]any{{"locator": "docs/transactions.md", "relation": "supports"}}})
	if res.IsError {
		t.Fatal(text)
	}
	var saved struct {
		ID       string                         `json:"id"`
		TopicKey string                         `json:"topic_key"`
		Evidence []struct{ Key, Status string } `json:"evidence"`
	}
	if err := json.Unmarshal([]byte(text), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" || saved.TopicKey != "database/transactions" || len(saved.Evidence) != 1 || saved.Evidence[0].Status != "unverified" {
		t.Fatalf("default-scoped save lost metadata: %s", text)
	}
	res, text = h.call("mnemos_save", map[string]any{"title": "revised transaction rule", "content": "use explicit transaction boundaries for writes", "type": "convention", "replaces_id": saved.ID})
	if res.IsError {
		t.Fatal(text)
	}
	var next struct {
		ID       string `json:"id"`
		TopicKey string `json:"topic_key"`
		Pinned   bool   `json:"pinned"`
	}
	if err := json.Unmarshal([]byte(text), &next); err != nil {
		t.Fatal(err)
	}
	if next.ID == "" || next.ID == saved.ID || next.TopicKey != saved.TopicKey || !next.Pinned {
		t.Fatalf("default scope bypassed explicit replacement safeguards: %s", text)
	}
	res, text = h.call("mnemos_get_many", map[string]any{"project": "taken", "ids": []string{next.ID}})
	if res.IsError {
		t.Fatal(text)
	}
	var details struct {
		Items []struct {
			Status      string `json:"status"`
			Observation struct {
				Project  string
				TopicKey string `json:"topic_key"`
			} `json:"observation"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(text), &details); err != nil {
		t.Fatal(err)
	}
	if len(details.Items) != 1 || details.Items[0].Status != "ok" || details.Items[0].Observation.Project != "taken" || details.Items[0].Observation.TopicKey != saved.TopicKey {
		t.Fatalf("scoped detail lost default project or metadata: %s", text)
	}
}

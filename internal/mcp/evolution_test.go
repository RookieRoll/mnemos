package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/polyxmedia/mnemos/internal/api"
	"github.com/polyxmedia/mnemos/internal/mcp"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
	"github.com/polyxmedia/mnemos/pkg/client"
)

func TestEvolutionTransportParityAndConflicts(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "evolution.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := memory.NewService(memory.Config{Store: db.Observations(), Clock: func() time.Time { return now }})
	srv := mcp.NewServer(mcp.Config{Profile: "maintenance", Memory: svc})
	left, right := mcpsdk.NewInMemoryTransports()
	ss, err := srv.SDK().Connect(ctx, left, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "evolution-test", Version: "1"}, nil).Connect(ctx, right, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	h := &harness{t: t, client: cs, server: ss}
	ts := httptest.NewServer(api.NewServer(api.Config{Memory: svc}).Handler())
	defer ts.Close()
	c := client.New(ts.URL)
	var sourceRequests atomic.Int64
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sourceRequests.Add(1); w.WriteHeader(500) }))
	defer source.Close()
	res, text := h.call("mnemos_save", map[string]any{"project": "p", "title": "transaction rule", "content": "transaction writes require a transaction", "type": "convention", "topic_key": "database/transactions", "pinned": true, "evidence": []map[string]any{{"locator": source.URL + "/source", "relation": "supports", "revision": "abc", "selector": "lines1-3", "fingerprint": strings.Repeat("a", 64)}}})
	if res.IsError {
		t.Fatal(text)
	}
	var saved struct {
		ID       string                     `json:"id"`
		TopicKey string                     `json:"topic_key"`
		Evidence []client.EvidenceReference `json:"evidence"`
	}
	if err := json.Unmarshal([]byte(text), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.TopicKey != "database/transactions" || len(saved.Evidence) != 1 || saved.Evidence[0].Status != "unverified" || saved.Evidence[0].Key == "" {
		t.Fatalf("save metadata lost: %s", text)
	}
	checked, err := c.CheckEvidence(ctx, client.EvidenceCheckInput{ObservationID: saved.ID, EvidenceKey: saved.Evidence[0].Key, Status: "changed"})
	if err != nil {
		t.Fatal(err)
	}
	if checked.Status != "changed" || checked.CheckedAt == nil || !checked.CheckedAt.Equal(now) {
		t.Fatalf("check metadata lost: %+v", checked)
	}
	res, text = h.call("mnemos_evidence_check", map[string]any{"observation_id": saved.ID, "evidence_key": saved.Evidence[0].Key, "status": "matched"})
	if res.IsError {
		t.Fatal(text)
	}
	var mChecked client.EvidenceReference
	_ = json.Unmarshal([]byte(text), &mChecked)
	if mChecked.Key != checked.Key || mChecked.Status != "matched" || mChecked.CheckedAt == nil || !mChecked.CheckedAt.Equal(now) {
		t.Fatalf("MCP check differs: %s", text)
	}
	got, err := c.GetMany(ctx, client.GetManyInput{Project: "p", IDs: []string{saved.ID}})
	if err != nil {
		t.Fatal(err)
	}
	o := got.Items[0].Observation
	if o == nil || !o.Pinned || o.TrustTier != "curated" || o.TopicKey != saved.TopicKey || o.Evidence[0].Status != "matched" {
		t.Fatalf("detail metadata lost: %+v", got)
	}
	hits, err := c.Search(ctx, client.SearchInput{Project: "p", Query: "transaction", TopicKey: saved.TopicKey})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Observation.TopicKey != saved.TopicKey || hits[0].Observation.Evidence[0].Status != "matched" {
		t.Fatal("client topic search metadata lost")
	}
	_, text = h.call("mnemos_search", map[string]any{"project": "p", "query": "transaction", "topic_key": saved.TopicKey})
	var search struct {
		Results []struct {
			ID       string                     `json:"id"`
			TopicKey string                     `json:"topic_key"`
			Evidence []client.EvidenceReference `json:"evidence"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(text), &search); err != nil {
		t.Fatal(err)
	}
	if len(search.Results) != 1 || search.Results[0].ID != saved.ID || search.Results[0].Evidence[0].Status != "matched" {
		t.Fatalf("MCP search mismatch: %s", text)
	}
	empty, err := c.Search(ctx, client.SearchInput{Project: "p", Query: "transaction", TopicKey: "different"})
	if err != nil || len(empty) != 0 {
		t.Fatal("exact topic filter ignored")
	}
	replacement := client.SaveInput{Project: "p", Title: "new transaction rule", Content: "transaction writes use explicit transaction boundaries", Type: "convention", ReplacesID: saved.ID}
	next, err := c.Save(ctx, replacement)
	if err != nil {
		t.Fatal(err)
	}
	if !next.Observation.Pinned || next.Observation.TopicKey != saved.TopicKey || len(next.Observation.Evidence) != 0 {
		t.Fatal("replacement inherited stale evidence or lost pin/topic")
	}
	again, err := c.Save(ctx, replacement)
	if err != nil || again.Observation.ID != next.Observation.ID {
		t.Fatalf("replacement retry failed: %+v %v", again, err)
	}
	replacement.Content = "different revision"
	if _, err := c.Save(ctx, replacement); !client.IsConflict(err) {
		t.Fatalf("want typed HTTP409, got %v", err)
	}
	res, text = h.call("mnemos_save", map[string]any{"project": "p", "title": "different", "content": "another replacement", "type": "convention", "replaces_id": saved.ID})
	var conflict map[string]any
	_ = json.Unmarshal([]byte(text), &conflict)
	if !res.IsError || conflict["code"] != "conflict" {
		t.Fatalf("MCP conflict not structured: %v %s", res, text)
	}
	unpinned, err := c.Save(ctx, client.SaveInput{Project: "p", Title: "explicit unpinned revision", Content: "a new explicit revision", Type: "convention", ReplacesID: next.Observation.ID, UnpinReplacement: true})
	if err != nil || unpinned.Observation.Pinned {
		t.Fatalf("unpin acknowledgement did not round-trip: %+v %v", unpinned, err)
	}
	minimal, minimalText := h.call("mnemos_save", map[string]any{"project": "p", "title": "exception", "content": "read only transaction exception", "type": "convention", "topic_key": saved.TopicKey, "evidence": []map[string]any{{"locator": "docs/exception.md", "relation": "supports"}}})
	if minimal.IsError {
		t.Fatalf("identity-only evidence rejected by SDK: %s", minimalText)
	}
	var minimalSaved struct {
		ID       string                     `json:"id"`
		Evidence []client.EvidenceReference `json:"evidence"`
	}
	if err := json.Unmarshal([]byte(minimalText), &minimalSaved); err != nil {
		t.Fatal(err)
	}
	if minimalSaved.Evidence[0].Status != "unverified" {
		t.Fatal("new reference not unverified")
	}
	if _, err := c.CheckEvidence(ctx, client.EvidenceCheckInput{ObservationID: minimalSaved.ID, EvidenceKey: minimalSaved.Evidence[0].Key, Status: "matched"}); err == nil {
		t.Fatal("matched without fingerprint accepted")
	}
	if sourceRequests.Load() != 0 {
		t.Fatal("saving/reading/checking evidence fetched source")
	}
}

type conflictingMCPEvidenceStore struct {
	memory.Store
	memory.EvolutionStore
}

func (s conflictingMCPEvidenceStore) CheckEvidenceState(context.Context, memory.EvidenceCheckInput, time.Time) (*memory.EvidenceReference, error) {
	return nil, memory.ErrConflict
}

func TestEvidenceCheckMCPConflictIsStructured(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "check-conflict.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := memory.NewService(memory.Config{Store: conflictingMCPEvidenceStore{Store: db.Observations()}})
	server := mcp.NewServer(mcp.Config{Profile: "maintenance", Memory: svc})
	left, right := mcpsdk.NewInMemoryTransports()
	ss, err := server.SDK().Connect(ctx, left, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "conflict", Version: "1"}, nil).Connect(ctx, right, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	h := &harness{t: t, client: cs, server: ss}
	result, text := h.call("mnemos_evidence_check", map[string]any{"observation_id": "id", "evidence_key": strings.Repeat("a", 64), "status": "changed"})
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if !result.IsError || out["code"] != "conflict" || result.StructuredContent == nil {
		t.Fatalf("typed structured conflict lost: %+v %s", result, text)
	}
}

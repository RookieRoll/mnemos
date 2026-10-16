package mcp_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/polyxmedia/mnemos/internal/api"
	"github.com/polyxmedia/mnemos/internal/mcp"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/storage"
	"github.com/polyxmedia/mnemos/pkg/client"
)

func TestScopedReadTransportParity(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "reads.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := memory.NewService(memory.Config{Store: db.Observations(), Clock: func() time.Time { return now }})
	if err := db.Sessions().Insert(ctx, &session.Session{ID: "session", AgentID: "default", Project: "p", StartedAt: now.Add(-48 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	expired := now.Add(-time.Hour)
	for _, o := range []memory.Observation{
		{ID: "a", Title: "rule", Content: "write transactions"},
		{ID: "b", Title: "exception", Content: "read only exception"},
		{ID: "raw", Title: "raw secret", Content: "unreviewed", TrustTier: memory.TrustRaw},
		{ID: "stale", Title: "stale secret", Content: "obsolete", ValidUntil: &expired},
		{ID: "other", Title: "other secret", Content: "hidden", Project: "other"},
	} {
		if o.Project == "" {
			o.Project = "p"
		}
		o.AgentID = "default"
		o.SessionID = "session"
		o.CreatedAt = now.Add(-24 * time.Hour)
		o.ValidFrom = o.CreatedAt
		o.Importance = 5
		o.Type = memory.TypeConvention
		if o.TrustTier == "" {
			o.TrustTier = memory.TrustCurated
		}
		if err := db.Observations().Insert(ctx, &o); err != nil {
			t.Fatal(err)
		}
	}
	srv := mcp.NewServer(mcp.Config{Memory: svc})
	left, right := mcpsdk.NewInMemoryTransports()
	ss, err := srv.SDK().Connect(ctx, left, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "read-test", Version: "1"}, nil).Connect(ctx, right, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	h := &harness{t: t, client: cs, server: ss}
	ts := httptest.NewServer(api.NewServer(api.Config{Memory: svc}).Handler())
	defer ts.Close()
	c := client.New(ts.URL)
	for _, raw := range []bool{false, true} {
		args := map[string]any{"project": "p", "ids": []string{"b", "b", "a", "missing", "other", "raw", "stale"}, "include_raw": raw, "include_stale": raw, "as_of": now.Format(time.RFC3339), "max_bytes": 32768}
		result, text := h.call("mnemos_get_many", args)
		if result.IsError {
			t.Fatal(text)
		}
		var m memory.GetManyResult
		if err := json.Unmarshal([]byte(text), &m); err != nil {
			t.Fatal(err)
		}
		out, err := c.GetMany(ctx, client.GetManyInput{Project: "p", IDs: args["ids"].([]string), IncludeRaw: raw, IncludeStale: raw, AsOf: now, MaxBytes: 32768})
		if err != nil {
			t.Fatal(err)
		}
		if len(m.Items) != 6 || len(out.Items) != 6 {
			t.Fatalf("dedup not preserved: %v %v", m, out)
		}
		for i, item := range m.Items {
			if item.ID != out.Items[i].ID || string(item.Status) != string(out.Items[i].Status) {
				t.Fatalf("transport status mismatch: %v %v", m, out)
			}
			if item.Observation != nil {
				other := out.Items[i].Observation
				if other == nil || string(item.Observation.TrustTier) != other.TrustTier || item.Observation.ValidFrom != other.ValidFrom {
					t.Fatal("detail visibility metadata lost")
				}
			}
		}
		zero := 0
		nArgs := map[string]any{"project": "p", "id": "a", "before": zero, "after": 10, "include_raw": raw, "include_stale": raw, "as_of": now.Format(time.RFC3339)}
		result, text = h.call("mnemos_neighborhood", nArgs)
		if result.IsError {
			t.Fatal(text)
		}
		var nm client.NeighborhoodResult
		if err := json.Unmarshal([]byte(text), &nm); err != nil {
			t.Fatal(err)
		}
		ten := 10
		nh, err := c.Neighborhood(ctx, client.NeighborhoodInput{Project: "p", ID: "a", Before: &zero, After: &ten, IncludeRaw: raw, IncludeStale: raw, AsOf: now})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(nm, *nh) {
			t.Fatalf("neighborhood mismatch MCP=%+v HTTP=%+v", nm, *nh)
		}
	}
	oversized := &memory.Observation{ID: "metadata", AgentID: "default", Project: "p", Title: strings.Repeat("title", 500), Content: "small", Tags: []string{strings.Repeat("tag", 1000)}, Structured: strings.Repeat("metadata", 500), Type: memory.TypeContext, Importance: 5, CreatedAt: now, ValidFrom: now, TrustTier: memory.TrustCurated}
	if err := db.Observations().Insert(ctx, oversized); err != nil {
		t.Fatal(err)
	}
	largeResult, largeText := h.call("mnemos_get_many", map[string]any{"project": "p", "ids": []string{"metadata"}, "max_bytes": 1024})
	if largeResult.IsError {
		t.Fatal(largeText)
	}
	var large memory.GetManyResult
	if err := json.Unmarshal([]byte(largeText), &large); err != nil {
		t.Fatal(err)
	}
	if large.Items[0].Status != memory.ReadOmittedForSize || large.Items[0].Observation != nil {
		t.Fatal("variable metadata bypassed byte budget")
	}
	httpLarge, err := c.GetMany(ctx, client.GetManyInput{Project: "p", IDs: []string{"metadata"}, MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if httpLarge.Items[0].Status != client.ReadOmittedForSize {
		t.Fatal("HTTP metadata budget differs")
	}
	res, text := h.call("mnemos_get_many", map[string]any{"project": "p", "ids": []string{"a"}, "max_bytes": 1})
	if res.IsError {
		t.Fatal(text)
	}
	var small memory.GetManyResult
	_ = json.Unmarshal([]byte(text), &small)
	if small.Items[0].Status != memory.ReadOmittedForSize || small.Items[0].Observation != nil || !small.ContentLimited {
		t.Fatal("size omission lost")
	}
	for _, tool := range []string{"mnemos_get_many", "mnemos_neighborhood"} {
		args := map[string]any{"ids": []string{"a"}, "id": "a"}
		r, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: tool, Arguments: args})
		if err == nil && !r.IsError {
			t.Fatalf("%s accepted missing project", tool)
		}
	}
}

func TestScopedReadDocumentExamples(t *testing.T) {
	h := newHarness(t)
	for _, path := range []string{"../../docs/API.md", "../../docs/MCP_TOOLS.md"} {
		tested := 0
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, block := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "```json\n")[1:] {
			snippet := strings.SplitN(block, "```", 2)[0]
			var args map[string]any
			if err := json.Unmarshal([]byte(snippet), &args); err != nil {
				t.Fatal(err)
			}
			if args["project"] == nil || (args["ids"] == nil && args["id"] == nil) {
				continue
			}
			tested++
			tool := "mnemos_neighborhood"
			if args["ids"] != nil {
				tool = "mnemos_get_many"
			}
			res, text := h.call(tool, args)
			if res.IsError {
				t.Fatalf("%s documented example: %s", path, text)
			}
		}
		if tested == 0 {
			t.Fatalf("no scoped-read examples found in %s", path)
		}
		t.Logf("executed %d scoped-read JSON examples from %s", tested, path)
	}
}

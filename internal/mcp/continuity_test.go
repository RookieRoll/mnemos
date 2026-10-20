package mcp_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/polyxmedia/mnemos/internal/continuity"
	"github.com/polyxmedia/mnemos/internal/continuityflow"
	"github.com/polyxmedia/mnemos/internal/mcp"
	"github.com/polyxmedia/mnemos/internal/prewarm"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func TestCheckpointSchemaExposesOnlyAgentWritablePayload(t *testing.T) {
	h := newHarness(t)
	listed, err := h.client.ListTools(context.Background(), &mcpsdk.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Name != "mnemos_checkpoint" {
			continue
		}
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties map[string]struct {
				Properties           map[string]json.RawMessage `json:"properties"`
				AdditionalProperties any                        `json:"additionalProperties"`
			} `json:"properties"`
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatal(err)
		}
		payload := schema.Properties["payload"]
		if _, exposed := payload.Properties["workspace"]; exposed {
			t.Fatal("checkpoint input schema exposes server-sampled payload.workspace")
		}
		for _, field := range []string{"task", "state", "summary", "next_steps", "blockers", "references"} {
			if payload.Properties[field] == nil {
				t.Errorf("agent-writable payload field %q missing", field)
			}
		}
		if payload.AdditionalProperties != false {
			t.Errorf("checkpoint payload must retain strict unknown-field validation: %s", encoded)
		}
		requiredRevision := false
		for _, field := range schema.Required {
			requiredRevision = requiredRevision || field == "expected_revision"
		}
		if !requiredRevision {
			t.Fatal("checkpoint schema lost required CAS revision")
		}
		return
	}
	t.Fatal("mnemos_checkpoint was not registered")
}

type checkpointResolver struct {
	root    string
	samples atomic.Int32
}

func (r *checkpointResolver) Resolve(context.Context, string, string) (continuity.Workspace, error) {
	return continuity.Workspace{Key: "checkpoint-workspace", Root: r.root}, nil
}

func (r *checkpointResolver) Sample(context.Context, string) continuity.WorkspaceState {
	r.samples.Add(1)
	return continuity.WorkspaceState{Branch: "server-branch", HEAD: "server-head", Known: true}
}

func checkpointObject(t *testing.T, text string) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatalf("invalid continuity receipt: %v %s", err, text)
	}
	return value
}

func TestCheckpointWritablePayloadMapsWithoutLosingSampledReadMetadata(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(root, "continuity.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	resolver := &checkpointResolver{root: root}
	core := continuity.New(continuity.Config{Store: db.Continuity(), Resolver: resolver, Now: func() time.Time {
		return time.Date(2026, 10, 20, 1, 0, 0, 0, time.UTC)
	}})
	flow := continuityflow.New(continuityflow.Config{Core: core, Composer: prewarm.NewService(prewarm.Config{
		Observations: db.Observations(), Sessions: db.Sessions(), Skills: db.Skills(), Touches: db.Touches(),
	})})
	h := newHarnessWith(t, func(cfg *mcp.Config) { cfg.Continuity = flow })
	opened, text := h.call("mnemos_session_start", map[string]any{"continuity": map[string]any{
		"protocol_version": 1, "agent_id": "checkpoint-owner", "cwd": root, "workspace_root": root,
		"harness_kind": "test", "start_key": "checkpoint-write-schema",
	}})
	if opened.IsError {
		t.Fatal(text)
	}
	id := checkpointObject(t, text)["session_id"].(string)
	payload := func() map[string]any {
		return map[string]any{"task": "migration", "state": "blocked", "summary": "migration drafted; verify current files",
			"next_steps": []string{"run migration tests"}, "blockers": []string{"pending review"},
			"references": []map[string]string{{"kind": "file", "id": "schema.sql"}}}
	}
	input := func() map[string]any {
		return map[string]any{"protocol_version": 1, "agent_id": "checkpoint-owner", "cwd": root, "workspace_root": root,
			"session_id": id, "action": "save", "expected_revision": 0, "request_id": "first-checkpoint",
			"visibility": "workspace", "payload": payload()}
	}
	beforeSave := resolver.samples.Load()
	firstResult, text := h.call("mnemos_checkpoint", input())
	first := checkpointObject(t, text)
	if firstResult.IsError || first["status"] != "applied" || first["revision"] != float64(1) {
		t.Fatalf("writable payload failed: %s", text)
	}
	if resolver.samples.Load() != beforeSave+1 {
		t.Fatalf("checkpoint did not sample workspace exactly once: %d", resolver.samples.Load())
	}
	read := map[string]any{"protocol_version": 1, "agent_id": "checkpoint-owner", "cwd": root,
		"workspace_root": root, "session_id": id, "source_session_id": id}
	result, text := h.call("mnemos_resume_context", read)
	contextResult := checkpointObject(t, text)
	if result.IsError || contextResult["status"] != "selected" {
		t.Fatal(text)
	}
	source := contextResult["source"].(map[string]any)
	if source["visibility"] != "workspace" {
		t.Fatalf("DTO lost visibility: %s", text)
	}
	expectedPayload := payload()
	expectedPayload["workspace"] = map[string]any{"branch": "server-branch", "head": "server-head", "known": true}
	encodedExpected, _ := json.Marshal(expectedPayload)
	if !reflect.DeepEqual(source["payload"], checkpointObject(t, string(encodedExpected))) {
		t.Fatalf("read output lost writable fields or server workspace: %s", text)
	}
	beforeRejectedWrites := resolver.samples.Load()
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"forged workspace", func(in map[string]any) {
			in["payload"].(map[string]any)["workspace"] = map[string]any{"known": true, "branch": "forged", "head": "forged"}
		}},
		{"empty workspace still readonly", func(in map[string]any) { in["payload"].(map[string]any)["workspace"] = map[string]any{} }},
		{"unknown payload field", func(in map[string]any) { in["payload"].(map[string]any)["unexpected"] = true }},
		{"missing revision", func(in map[string]any) { delete(in, "expected_revision") }},
		{"wrong summary type", func(in map[string]any) { in["payload"].(map[string]any)["summary"] = 42 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := input()
			tc.edit(in)
			result, text := h.call("mnemos_checkpoint", in)
			if !result.IsError || checkpointObject(t, text)["code"] != "validation" {
				t.Fatalf("invalid writable input accepted or untyped: %s", text)
			}
		})
	}
	base := input()
	delete(base, "payload")
	baseJSON, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	payloadJSON, err := json.Marshal(payload())
	if err != nil {
		t.Fatal(err)
	}
	// Keep duplicate members in the actual SDK wire request. Schema validation
	// can normalize objects before typed decoding; the handler must still
	// reject a server-only field in any original payload occurrence.
	for _, tc := range []struct{ name, suffix string }{
		{"earlier duplicate payload has workspace", `,"payload":{"workspace":{"branch":"forged-secret"}},"payload":` + string(payloadJSON) + `}`},
		{"duplicate workspace with known field duplicates", `,"payload":{"task":"migration","state":"blocked","summary":"first","workspace":{"branch":"forged-secret"},"workspace":{},"summary":"last"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := json.RawMessage(string(baseJSON[:len(baseJSON)-1]) + tc.suffix)
			result, text := h.call("mnemos_checkpoint", raw)
			if !result.IsError || checkpointObject(t, text)["code"] != "validation" || strings.Contains(text, "forged-secret") {
				t.Fatalf("raw duplicate object laundered server metadata or leaked input: %s", text)
			}
		})
	}
	conflicting := input()
	conflicting["request_id"] = "different-stale-request"
	result, text = h.call("mnemos_checkpoint", conflicting)
	if !result.IsError || checkpointObject(t, text)["code"] != "conflict" {
		t.Fatalf("DTO weakened CAS: %s", text)
	}
	result, text = h.call("mnemos_checkpoint", input())
	replay := checkpointObject(t, text)
	if result.IsError || replay["status"] != "replayed" || replay["revision"] != first["revision"] || replay["updated_at"] != first["updated_at"] || resolver.samples.Load() != beforeRejectedWrites {
		t.Fatalf("DTO changed exact retry identity or resampled workspace: %s", text)
	}
	result, text = h.call("mnemos_resume_context", read)
	if result.IsError || !reflect.DeepEqual(contextResult, checkpointObject(t, text)) {
		t.Fatalf("rejected writes changed saved state: %s", text)
	}
	clear := input()
	delete(clear, "payload")
	delete(clear, "visibility")
	clear["action"], clear["expected_revision"], clear["request_id"] = "clear", 1, "clear-checkpoint"
	beforeClear := resolver.samples.Load()
	result, text = h.call("mnemos_checkpoint", clear)
	if result.IsError || checkpointObject(t, text)["revision"] != float64(2) || resolver.samples.Load() != beforeClear {
		t.Fatalf("nil payload clear semantics changed: %s", text)
	}
}

package main

import (
	"context"
	"encoding/json"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/polyxmedia/mnemos/internal/mcp"
)

func TestForcedPrewarmCanCloseThroughMCP(t *testing.T) {
	homeWithConfig(t, "[embedding]\nprovider = \"none\"\n")
	ctx := context.Background()
	open := func() map[string]any {
		var raw string
		withStdin(t, "", func() {
			raw = captureStdout(t, func() {
				if err := runPrewarm(ctx, []string{"--project", "p", "--open-session", "--format", "json"}); err != nil {
					t.Fatal(err)
				}
			})
		})
		var receipt map[string]any
		if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
			t.Fatal(err)
		}
		if id, ok := receipt["session_id"].(string); !ok || id == "" {
			t.Fatalf("missing session: %s", raw)
		}
		if id, ok := receipt["host_session_id"].(string); !ok || id == "" {
			t.Fatalf("missing host: %s", raw)
		}
		return receipt
	}
	first, second := open(), open()
	if first["session_id"] == second["session_id"] || first["host_session_id"] == second["host_session_id"] {
		t.Fatal("unhosted invocations shared an identity")
	}
	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	srv := mcp.NewServer(mcp.Config{Memory: d.mem, Sessions: d.sess, Skills: d.skl, DefaultProject: "p"})
	a, b := mcpsdk.NewInMemoryTransports()
	ss, err := srv.SDK().Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "test"}, nil)
	cs, err := client.Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	for _, tc := range []struct {
		id     any
		denied bool
	}{{second["session_id"], true}, {first["session_id"], false}} {
		args := map[string]any{"session_id": tc.id, "host_session_id": first["host_session_id"], "summary": "done"}
		res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "mnemos_session_end", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError != tc.denied {
			encoded, _ := json.Marshal(res)
			t.Fatalf("unexpected close result: %s", encoded)
		}
	}
	closed, err := d.sess.Get(ctx, first["session_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	other, err := d.sess.Get(ctx, second["session_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if closed.EndedAt == nil || closed.Summary != "done" || other.EndedAt != nil {
		t.Fatal("close did not preserve exact target semantics")
	}
}

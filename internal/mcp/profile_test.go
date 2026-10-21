package mcp_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/polyxmedia/mnemos/internal/mcp"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/rumination"
	"github.com/polyxmedia/mnemos/internal/skills"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func listedToolNames(t *testing.T, h *harness) map[string]struct{} {
	t.Helper()
	listed, err := h.client.ListTools(context.Background(), &mcpsdk.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]struct{}, len(listed.Tools))
	for _, tool := range listed.Tools {
		if _, exists := names[tool.Name]; exists {
			t.Fatalf("duplicate tool %q", tool.Name)
		}
		names[tool.Name] = struct{}{}
	}
	return names
}

func TestCoreProfileListsExactlyTaskTools(t *testing.T) {
	h := newHarnessWith(t, func(cfg *mcp.Config) { cfg.Profile = "core" })
	want := []string{
		"mnemos_session_start", "mnemos_session_end", "mnemos_search", "mnemos_get_many",
		"mnemos_save", "mnemos_correct", "mnemos_convention", "mnemos_checkpoint",
		"mnemos_resume_context",
	}
	first := listedToolNames(t, h)
	if len(first) != len(want) {
		t.Fatalf("core profile has %d tools, want exactly %d: %v", len(first), len(want), first)
	}
	for _, name := range want {
		if _, ok := first[name]; !ok {
			t.Errorf("core profile missing %q", name)
		}
	}
	second := listedToolNames(t, h)
	if len(second) != len(first) {
		t.Fatalf("tools/list changed during server lifetime: first=%v second=%v", first, second)
	}
	for name := range first {
		if _, ok := second[name]; !ok {
			t.Errorf("tool %q disappeared during server lifetime", name)
		}
	}
}

func TestEmptyProfileDefaultsToCore(t *testing.T) {
	h := newHarnessWith(t, func(cfg *mcp.Config) { cfg.Profile = "" })
	names := listedToolNames(t, h)
	if len(names) != 9 {
		t.Fatalf("empty MCP profile registered %d tools, want default core's 9", len(names))
	}
	for _, name := range []string{
		"mnemos_session_start", "mnemos_session_end", "mnemos_search", "mnemos_get_many",
		"mnemos_save", "mnemos_correct", "mnemos_convention", "mnemos_checkpoint", "mnemos_resume_context",
	} {
		if _, ok := names[name]; !ok {
			t.Errorf("default core profile missing %q", name)
		}
	}
}

func TestUnknownProfileRegistersNoTools(t *testing.T) {
	h := newHarnessWith(t, func(cfg *mcp.Config) { cfg.Profile = "unknown" })
	if names := listedToolNames(t, h); len(names) != 0 {
		t.Fatalf("unknown profile exposed tools instead of failing closed: %v", names)
	}
}

func TestMaintenanceProfileRetainsExistingToolSet(t *testing.T) {
	h := newHarnessWith(t, func(cfg *mcp.Config) { cfg.Profile = "maintenance" })
	names := listedToolNames(t, h)
	if len(names) != 23 {
		t.Fatalf("maintenance profile has %d tools without rumination service, want existing 23", len(names))
	}
	want := []string{
		"mnemos_save", "mnemos_search", "mnemos_get", "mnemos_delete", "mnemos_link",
		"mnemos_get_many", "mnemos_evidence_check", "mnemos_neighborhood",
		"mnemos_session_start", "mnemos_session_end", "mnemos_checkpoint", "mnemos_resume_context",
		"mnemos_context", "mnemos_premortem", "mnemos_correct", "mnemos_convention", "mnemos_touch",
		"mnemos_skill_match", "mnemos_skill_save", "mnemos_skill_score", "mnemos_stats",
		"mnemos_promote", "mnemos_pin",
	}
	for _, name := range want {
		if _, ok := names[name]; !ok {
			t.Errorf("maintenance profile missing existing tool %q", name)
		}
	}
	if _, ok := names["mnemos_ruminate_list"]; ok {
		t.Fatal("rumination tools should remain optional when the service is absent")
	}
}

func TestMaintenanceProfileAddsOptionalRuminationTools(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "profile.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	skl := skills.NewService(skills.Config{Store: db.Skills()})
	rum := rumination.NewService(rumination.Config{
		Monitors: []rumination.Monitor{&rumination.SkillEffectivenessMonitor{Skills: skl}},
		Skills:   skl, Memory: db.Observations(), Store: db.Rumination(),
	})
	srv := mcp.NewServer(mcp.Config{
		Profile: "maintenance", Memory: memory.NewService(memory.Config{Store: db.Observations()}),
		Skills: skl, Rumination: rum, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := srv.SDK().Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "profile-test", Version: "0"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		_ = serverSession.Close()
		t.Fatal(err)
	}
	h := &harness{t: t, client: clientSession, server: serverSession}
	t.Cleanup(h.close)

	names := listedToolNames(t, h)
	if len(names) != 27 {
		t.Fatalf("maintenance profile has %d tools with rumination enabled, want existing 23 plus 4 optional", len(names))
	}
	for _, name := range []string{
		"mnemos_ruminate_list", "mnemos_ruminate_pack", "mnemos_ruminate_resolve", "mnemos_ruminate_dismiss",
	} {
		if _, ok := names[name]; !ok {
			t.Errorf("maintenance profile missing optional tool %q", name)
		}
	}
}

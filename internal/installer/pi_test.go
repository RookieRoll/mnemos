package installer_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/polyxmedia/mnemos/internal/installer"
)

// setupPiHome builds an isolated HOME with a pi agent dir and returns
// (home, agentDir). PI_CODING_AGENT_DIR relocates the target the same way
// CLAUDE_CONFIG_DIR relocates Claude Code's, so nothing touches the
// developer's real pi configuration.
func setupPiHome(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	agentDir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	// Keep the Claude Code target out of the picture.
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return home, agentDir
}

func findTarget(t *testing.T, name string) (installer.Target, bool) {
	t.Helper()
	for _, tg := range installer.DetectTargets() {
		if tg.Name == name {
			return tg, true
		}
	}
	return installer.Target{}, false
}

func TestDetectTargetsIncludesPiWhenAgentDirExists(t *testing.T) {
	_, agentDir := setupPiHome(t)

	tg, ok := findTarget(t, "pi")
	if !ok {
		t.Fatal("pi must be detected when its agent dir exists")
	}
	want := filepath.Join(agentDir, "mcp.json")
	if tg.Path != want {
		t.Errorf("path = %q, want %q", tg.Path, want)
	}
	if tg.Key != "mnemos" {
		t.Errorf("server key = %q, want %q", tg.Key, "mnemos")
	}
}

func TestDetectTargetsSkipsPiWhenAgentDirMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "nope"))

	if _, ok := findTarget(t, "pi"); ok {
		t.Error("an absent pi agent dir must not produce a target")
	}
}

func TestDetectTargetsHonoursPiAgentDirOverride(t *testing.T) {
	home := t.TempDir()
	custom := filepath.Join(home, "custom-agent")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_CODING_AGENT_DIR", custom)

	tg, ok := findTarget(t, "pi")
	if !ok {
		t.Fatal("the override dir must still yield a target")
	}
	if want := filepath.Join(custom, "mcp.json"); tg.Path != want {
		t.Errorf("path = %q, want %q", tg.Path, want)
	}
}

func TestInstallIntoPiConfigPreservesOtherServers(t *testing.T) {
	_, agentDir := setupPiHome(t)
	path := filepath.Join(agentDir, "mcp.json")
	initial := `{
  "settings": { "idleTimeout": 10 },
  "mcpServers": {
    "other": { "command": "other-server", "args": ["run"] }
  }
}`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	tg, ok := findTarget(t, "pi")
	if !ok {
		t.Fatal("pi target missing")
	}
	changed, err := installer.Install(tg, installer.ServerEntry{Command: "/usr/local/bin/mnemos", Args: []string{"serve"}})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first install must report a change")
	}

	data, _ := os.ReadFile(path)
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if settings, ok := cfg["settings"].(map[string]any); !ok || settings["idleTimeout"] == nil {
		t.Error("unrelated top-level keys must survive")
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Error("an unrelated MCP server must survive")
	}
	entry, ok := servers["mnemos"].(map[string]any)
	if !ok {
		t.Fatal("mnemos entry must be added")
	}
	if entry["command"] != "/usr/local/bin/mnemos" {
		t.Errorf("wrong command: %v", entry["command"])
	}
}

func TestInstallIntoPiIsIdempotent(t *testing.T) {
	_, agentDir := setupPiHome(t)
	tg, _ := findTarget(t, "pi")
	entry := installer.ServerEntry{Command: "mnemos", Args: []string{"serve"}}
	if _, err := installer.Install(tg, entry); err != nil {
		t.Fatal(err)
	}
	changed, err := installer.Install(tg, entry)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("a second identical install must report no change")
	}
	_ = agentDir
}

// TestInstallIntoPiLeavesAdapterOnlyFieldsAlone is the case that matters
// in practice: a user who added directTools or a lifecycle setting to the
// mnemos entry must not have it stripped by a later `mnemos init`.
//
// The current Install compares only the fields mnemos owns, so an extra
// adapter-only key on the entry survives. If that ever stops holding, this
// test names the regression instead of leaving it to a user noticing that
// their tool visibility silently reverted.
func TestInstallIntoPiLeavesAdapterOnlyFieldsAlone(t *testing.T) {
	_, agentDir := setupPiHome(t)
	path := filepath.Join(agentDir, "mcp.json")
	initial := `{
  "mcpServers": {
    "mnemos": {
      "command": "mnemos",
      "args": ["serve"],
      "directTools": true,
      "toolPrefix": "mcp",
      "lifecycle": "lazy-keep-alive"
    }
  }
}`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	tg, _ := findTarget(t, "pi")
	if !installer.IsInstalled(tg) {
		t.Fatal("an entry with extra adapter fields is still installed")
	}
	if _, err := installer.Install(tg, installer.ServerEntry{Command: "mnemos", Args: []string{"serve"}}); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	entry := cfg["mcpServers"].(map[string]any)["mnemos"].(map[string]any)
	if entry["directTools"] != true {
		t.Errorf("directTools must survive init, got %v", entry["directTools"])
	}
	if entry["toolPrefix"] != "mcp" {
		t.Errorf("toolPrefix must survive init, got %v", entry["toolPrefix"])
	}
}

func TestPiPackageInstalledReadsBothScopes(t *testing.T) {
	_, agentDir := setupPiHome(t)
	settings := filepath.Join(agentDir, "settings.json")

	installed, path := installer.PiPackageInstalled()
	if installed {
		t.Fatalf("nothing installed yet, got %q", path)
	}

	body := `{"packages": ["npm:pi-mcp-adapter", "git:github.com/polyxmedia/mnemos@v1"]}`
	if err := os.WriteFile(settings, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	installed, path = installer.PiPackageInstalled()
	if !installed {
		t.Fatal("a git package referencing mnemos must be recognized")
	}
	if path != settings {
		t.Errorf("path = %q, want %q", path, settings)
	}
}

func TestPiPackageInstalledAcceptsObjectEntries(t *testing.T) {
	_, agentDir := setupPiHome(t)
	body := `{"packages": [{"source": "npm:mnemos-pi"}]}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if installed, _ := installer.PiPackageInstalled(); !installed {
		t.Error("the object package form must be recognized")
	}
}

func TestPiPackageInstalledIgnoresUnrelatedPackages(t *testing.T) {
	_, agentDir := setupPiHome(t)
	body := `{"packages": ["npm:pi-mcp-adapter", "npm:openspec"]}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if installed, path := installer.PiPackageInstalled(); installed {
		t.Errorf("unrelated packages must not count, matched %q", path)
	}
}

func TestPiPackageInstalledToleratesMalformedSettings(t *testing.T) {
	_, agentDir := setupPiHome(t)
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if installed, _ := installer.PiPackageInstalled(); installed {
		t.Error("malformed settings must not report an install")
	}
}

// TestInstallDropsStaleOwnedFields pins the one case where merging is not
// just "keep everything": if a previous install wrote args and this one
// does not, the stale args must go. Leaving them would keep pointing the
// client at an argument list mnemos no longer intends, and the entry would
// look up to date while doing the wrong thing.
//
// Asserted through Install, because that is the only caller of the merge
// and the merge is not part of the package's surface.
func TestInstallDropsStaleOwnedFields(t *testing.T) {
	_, agentDir := setupPiHome(t)
	path := filepath.Join(agentDir, "mcp.json")
	initial := `{
  "mcpServers": {
    "mnemos": {
      "command": "old",
      "args": ["serve", "--stale"],
      "env": { "OLD": "1" },
      "directTools": true
    }
  }
}`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	tg, _ := findTarget(t, "pi")
	if _, err := installer.Install(tg, installer.ServerEntry{Command: "new"}); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	entry := cfg["mcpServers"].(map[string]any)["mnemos"].(map[string]any)
	if entry["command"] != "new" {
		t.Errorf("owned field must be overwritten, got %v", entry["command"])
	}
	for _, stale := range []string{"args", "env"} {
		if _, present := entry[stale]; present {
			t.Errorf("%s is owned but no longer desired and must be dropped, got %v", stale, entry[stale])
		}
	}
	if entry["directTools"] != true {
		t.Errorf("unowned field must survive, got %v", entry["directTools"])
	}
}

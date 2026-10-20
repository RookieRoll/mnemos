package installer_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/polyxmedia/mnemos/internal/installer"
)

// setupPiHome builds an isolated HOME with a pi agent dir and returns
// (home, agentDir), keeping installer tests away from the developer's files.
func setupPiHome(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	agentDir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home, agentDir
}

func TestPiPackageMCPInstalledVerifiesManifest(t *testing.T) {
	for _, tc := range []struct {
		name, manifest, config string
		object, relative, want bool
	}{
		{"local package", `{"name":"kls-mnemos-pi","pi":{"mcp":"./mcp.json"}}`, `{"mcpServers":{"mnemos":{"command":"mnemos","args":["serve"]}}}`, false, false, true},
		{"relative object source and paths array", `{"name":"memory","pi":{"mcp":["./mcp.json"]}}`, `{"mcpServers":{"memory":{"command":"mnemos.exe","args":["serve"],"directTools":true}}}`, true, true, true},
		{"missing manifest", "", `{"mcpServers":{"mnemos":{"command":"mnemos","args":["serve"]}}}`, false, false, false},
		{"bad manifest", `{`, `{}`, false, false, false},
		{"missing MCP declaration", `{"name":"mnemos"}`, `{}`, false, false, false},
		{"missing package name", `{"pi":{"mcp":"./mcp.json"}}`, `{}`, false, false, false},
		{"wrong command", `{"name":"mnemos","pi":{"mcp":"./mcp.json"}}`, `{"mcpServers":{"mnemos":{"command":"other","args":["serve"]}}}`, false, false, false},
		{"missing executable", `{"name":"mnemos","pi":{"mcp":"./mcp.json"}}`, `{"mcpServers":{"mnemos":{"command":"/definitely/missing/mnemos","args":["serve"]}}}`, false, false, false},
		{"disabled server", `{"name":"mnemos","pi":{"mcp":"./mcp.json"}}`, `{"mcpServers":{"mnemos":{"command":"mnemos","args":["serve"],"disabled":true}}}`, false, false, false},
		{"wrong args", `{"name":"mnemos","pi":{"mcp":"./mcp.json"}}`, `{"mcpServers":{"mnemos":{"command":"mnemos","args":["config"]}}}`, false, false, false},
		{"bad config", `{"name":"mnemos","pi":{"mcp":"./mcp.json"}}`, `{`, false, false, false},
		{"package escape", `{"name":"mnemos","pi":{"mcp":"../mcp.json"}}`, `{}`, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, agentDir := setupPiHome(t)
			// Command resolution is checked without executing it; keep fixtures
			// independent of the developer's or CI runner's installed binaries.
			commandName := "mnemos"
			if runtime.GOOS == "windows" {
				commandName += ".exe"
			} else if tc.name == "relative object source and paths array" {
				commandName += ".exe"
			}
			if err := os.WriteFile(filepath.Join(agentDir, commandName), []byte("fixture"), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", agentDir)
			root := filepath.Join(agentDir, "local-package")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.manifest != "" {
				if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(tc.manifest), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, "mcp.json"), []byte(tc.config), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.name == "package escape" {
				// An otherwise valid, readable config outside the package must
				// fail containment, rather than merely failing a missing-file check.
				outside := `{"mcpServers":{"mnemos":{"command":"mnemos","args":["serve"]}}}`
				if err := os.WriteFile(filepath.Join(agentDir, "mcp.json"), []byte(outside), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			source := root
			if tc.relative {
				source = "./local-package"
			}
			var entry any = source
			if tc.object {
				entry = map[string]any{"source": source}
			}
			data, _ := json.Marshal(map[string]any{"packages": []any{entry}})
			settings := filepath.Join(agentDir, "settings.json")
			if err := os.WriteFile(settings, data, 0o600); err != nil {
				t.Fatal(err)
			}
			got, path := installer.PiPackageMCPInstalled()
			if got != tc.want {
				t.Fatalf("installed=%v want=%v", got, tc.want)
			}
			if got && path != settings {
				t.Fatalf("path=%q want=%q", path, settings)
			}
			if _, err := os.Stat(filepath.Join(agentDir, "mcp-adapter.json")); !os.IsNotExist(err) {
				t.Fatalf("read-only check created adapter config: %v", err)
			}
		})
	}
}

func TestPiPackageMCPInstalledDoesNotTrustRemoteName(t *testing.T) {
	_, agentDir := setupPiHome(t)
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"packages":["npm:mnemos-pi","git:github.com/polyxmedia/mnemos"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, _ := installer.PiPackageMCPInstalled(); ok {
		t.Fatal("unresolved registration is not installation evidence")
	}
}

func findTarget(t *testing.T, name string, options ...installer.Options) (installer.Target, bool) {
	t.Helper()
	for _, tg := range installer.DetectTargets(options...) {
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
	want := filepath.Join(agentDir, "mcp-adapter.json")
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
	tg, ok := findTarget(t, "pi", installer.Options{PiAgentDir: custom})
	if !ok {
		t.Fatal("the override dir must still yield a target")
	}
	if want := filepath.Join(custom, "mcp-adapter.json"); tg.Path != want {
		t.Errorf("path = %q, want %q", tg.Path, want)
	}
}

func TestInstallIntoPiConfigPreservesOtherServers(t *testing.T) {
	_, agentDir := setupPiHome(t)
	path := filepath.Join(agentDir, "mcp-adapter.json")
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
	path := filepath.Join(agentDir, "mcp-adapter.json")
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
	if entry["lifecycle"] != "lazy-keep-alive" {
		t.Errorf("adapter lifecycle must survive init, got %v", entry["lifecycle"])
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
	path := filepath.Join(agentDir, "mcp-adapter.json")
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

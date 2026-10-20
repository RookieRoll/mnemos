package installer_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/polyxmedia/mnemos/internal/installer"
)

func writePlanJSON(t *testing.T, path string, value any) []byte {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return data
}

func planPi(t *testing.T) installer.TargetPlan {
	t.Helper()
	for _, plan := range installer.PlanTargets() {
		if plan.Target.Name == "pi" || plan.Target.Name == "pi (local package)" {
			return plan
		}
	}
	t.Fatal("pi plan missing")
	return installer.TargetPlan{}
}

func TestPlanPiPackageRoutePreservesConfiguration(t *testing.T) {
	for _, object := range []bool{false, true} {
		t.Run(map[bool]string{false: "string", true: "relative object"}[object], func(t *testing.T) {
			home, agent := setupPiHome(t)
			t.Chdir(home)
			name := "mnemos"
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			command := filepath.Join(agent, name)
			if err := os.WriteFile(command, []byte("not executed"), 0o700); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(agent, "package")
			manifest := writePlanJSON(t, filepath.Join(root, "package.json"), map[string]any{"name": "local-memory", "pi": map[string]any{"mcp": "./mcp.json"}})
			original := map[string]any{"mcpServers": map[string]any{
				"custom-memory": map[string]any{"command": command, "args": []string{"serve"}, "directTools": true, "toolPrefix": "custom", "lifecycle": map[string]any{"keep": true}},
				"other":         map[string]any{"command": "do-not-touch", "args": []string{"user"}},
			}, "extra": "preserve"}
			mcpPath := filepath.Join(root, "mcp.json")
			writePlanJSON(t, mcpPath, original)
			var registration any = root
			if object {
				registration = map[string]any{"source": "./package", "extensions": []string{"+index.ts"}}
			}
			settings := writePlanJSON(t, filepath.Join(agent, "settings.json"), map[string]any{"packages": []any{registration}, "custom": "preserve"})
			// A global config for a different server must remain byte-identical.
			global := writePlanJSON(t, filepath.Join(agent, "mcp-adapter.json"), map[string]any{"mcpServers": map[string]any{"other": map[string]any{"command": "other"}}})
			plan := planPi(t)
			plannedInfo, plannedErr := os.Stat(plan.Target.Path)
			originalInfo, originalErr := os.Stat(mcpPath)
			if plan.Err != nil || plannedErr != nil || originalErr != nil || !os.SameFile(plannedInfo, originalInfo) || plan.Target.Key != "custom-memory" {
				t.Fatalf("bad plan: %+v", plan)
			}
			changed, err := installer.Install(plan.Target, installer.ServerEntry{Command: command, Args: []string{"serve"}})
			if err != nil || changed {
				t.Fatalf("already correct route rewritten: %v %v", changed, err)
			}
			// Actual owned-field update uses another valid local executable.
			updatedCommand := filepath.Join(home, "new", name)
			if err := os.MkdirAll(filepath.Dir(updatedCommand), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(updatedCommand, []byte("not executed"), 0o700); err != nil {
				t.Fatal(err)
			}
			changed, err = installer.Install(plan.Target, installer.ServerEntry{Command: updatedCommand, Args: []string{"serve"}})
			if err != nil || !changed {
				t.Fatalf("route update failed: %v %v", changed, err)
			}
			var actual map[string]any
			data, _ := os.ReadFile(mcpPath)
			if err := json.Unmarshal(data, &actual); err != nil {
				t.Fatal(err)
			}
			original["mcpServers"].(map[string]any)["custom-memory"].(map[string]any)["command"] = updatedCommand
			wantJSON, _ := json.Marshal(original)
			var want map[string]any
			_ = json.Unmarshal(wantJSON, &want)
			if !reflect.DeepEqual(actual, want) {
				t.Fatalf("adapter/unrelated fields changed: %s", data)
			}
			for path, want := range map[string][]byte{filepath.Join(root, "package.json"): manifest, filepath.Join(agent, "settings.json"): settings, filepath.Join(agent, "mcp-adapter.json"): global} {
				actual, _ := os.ReadFile(path)
				if string(actual) != string(want) {
					t.Fatalf("unexpected change: %s", path)
				}
			}
			repeat := planPi(t)
			if repeat.Err != nil {
				t.Fatal(repeat.Err)
			}
			changed, err = installer.Install(repeat.Target, installer.ServerEntry{Command: updatedCommand, Args: []string{"serve"}})
			if err != nil || changed {
				t.Fatalf("repeat changed route: %v %v", changed, err)
			}
		})
	}
}

func TestPlanPiConflictsAreReadOnly(t *testing.T) {
	for _, conflict := range []string{"two packages", "package and global", "custom global key", "custom global current executable"} {
		t.Run(conflict, func(t *testing.T) {
			home, agent := setupPiHome(t)
			t.Chdir(home)
			name := "mnemos"
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			command := filepath.Join(agent, name)
			if err := os.WriteFile(command, []byte("fixture"), 0o700); err != nil {
				t.Fatal(err)
			}
			server := map[string]any{"command": command, "args": []string{"serve"}}
			var packages []any
			files := map[string][]byte{}
			count := 1
			if conflict == "two packages" {
				count = 2
			}
			for i := 0; i < count; i++ {
				root := filepath.Join(agent, []string{"package-a", "package-b"}[i])
				packages = append(packages, root)
				files[filepath.Join(root, "package.json")] = writePlanJSON(t, filepath.Join(root, "package.json"), map[string]any{"name": "fixture", "pi": map[string]any{"mcp": "mcp.json"}})
				files[filepath.Join(root, "mcp.json")] = writePlanJSON(t, filepath.Join(root, "mcp.json"), map[string]any{"mcpServers": map[string]any{"memory": server}})
			}
			files[filepath.Join(agent, "settings.json")] = writePlanJSON(t, filepath.Join(agent, "settings.json"), map[string]any{"packages": packages})
			global := filepath.Join(agent, "mcp-adapter.json")
			if conflict != "two packages" {
				key := "mnemos"
				if conflict == "custom global key" || conflict == "custom global current executable" {
					key = "alias"
				}
				globalServer := server
				if conflict == "custom global current executable" {
					self, err := os.Executable()
					if err != nil {
						t.Fatal(err)
					}
					globalServer = map[string]any{"command": self, "args": []string{"serve"}}
				}
				files[global] = writePlanJSON(t, global, map[string]any{"mcpServers": map[string]any{key: globalServer}})
			}
			if plan := planPi(t); plan.Err == nil {
				t.Fatalf("ambiguous plan accepted: %+v", plan)
			}
			for path, want := range files {
				actual, _ := os.ReadFile(path)
				if string(actual) != string(want) {
					t.Fatalf("plan mutated %s", path)
				}
			}
			if conflict == "two packages" {
				if _, err := os.Stat(global); !os.IsNotExist(err) {
					t.Fatal("plan created global duplicate")
				}
			}
		})
	}
}

func TestPlanPiDoesNotAuthorizeInvalidPackage(t *testing.T) {
	for _, kind := range []string{"disabled", "wrong args", "missing executable", "escape"} {
		t.Run(kind, func(t *testing.T) {
			home, agent := setupPiHome(t)
			t.Chdir(home)
			name := "mnemos"
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			command := filepath.Join(agent, name)
			if err := os.WriteFile(command, []byte("fixture"), 0o700); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(agent, "package")
			mcp := map[string]any{"command": command, "args": []string{"serve"}}
			switch kind {
			case "disabled":
				mcp["disabled"] = true
			case "wrong args":
				mcp["args"] = []string{"config"}
			case "missing executable":
				mcp["command"] = filepath.Join(agent, "missing", name)
			}
			path := "mcp.json"
			if kind == "escape" {
				path = "../outside.json"
			}
			writePlanJSON(t, filepath.Join(root, "package.json"), map[string]any{"name": "fixture", "pi": map[string]any{"mcp": path}})
			writePlanJSON(t, filepath.Join(root, path), map[string]any{"mcpServers": map[string]any{"mnemos": mcp}})
			writePlanJSON(t, filepath.Join(agent, "settings.json"), map[string]any{"packages": []string{root}})
			plan := planPi(t)
			if plan.Err != nil || plan.Target.Path != filepath.Join(agent, "mcp-adapter.json") {
				t.Fatalf("invalid package authorized: %+v", plan)
			}
			if _, err := os.Stat(plan.Target.Path); !os.IsNotExist(err) {
				t.Fatal("readonly plan created global file")
			}
		})
	}
}

func TestPlanPiFindsProjectPackageWithoutGlobalAgentDirectory(t *testing.T) {
	home, agent := setupPiHome(t)
	if err := os.Remove(agent); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	name := "mnemos"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	command := filepath.Join(home, name)
	if err := os.WriteFile(command, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, "package")
	writePlanJSON(t, filepath.Join(root, "package.json"), map[string]any{"name": "fixture", "pi": map[string]any{"mcp": "mcp.json"}})
	writePlanJSON(t, filepath.Join(root, "mcp.json"), map[string]any{"mcpServers": map[string]any{"memory": map[string]any{"command": command, "args": []string{"serve"}}}})
	writePlanJSON(t, filepath.Join(home, ".pi", "settings.json"), map[string]any{"packages": []string{root}})
	plan := planPi(t)
	if plan.Err != nil || plan.Target.Key != "memory" {
		t.Fatalf("project package ignored: %+v", plan)
	}
	if _, err := os.Stat(agent); !os.IsNotExist(err) {
		t.Fatal("planning created global agent directory")
	}
}

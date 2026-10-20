package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func initRuntimeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for key, value := range map[string]string{"HOME": home, "USERPROFILE": home, "APPDATA": filepath.Join(home, "appdata")} {
		t.Setenv(key, value)
	}
	t.Chdir(home)
	return home
}

func initTree(t *testing.T, home string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(home, path)
		if entry.IsDir() {
			out[rel] = "directory"
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func initWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInitArgumentsHaveNoInstallationSideEffects(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      []string
		wantError bool
	}{
		{"long help", []string{"--help"}, false}, {"short help", []string{"-h"}, false},
		{"unknown flag", []string{"--unknown"}, true}, {"positional", []string{"surprise"}, true},
		{"garbage after help", []string{"--help", "--unknown"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := initRuntimeHome(t)
			initWrite(t, filepath.Join(home, ".claude.json"), `{"preserve":true}`)
			initWrite(t, filepath.Join(home, ".pi", "agent", "settings.json"), `{}`)
			before := initTree(t, home)
			var err error
			output := captureStdout(t, func() { err = runInit(context.Background(), tc.args) })
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.wantError && !strings.Contains(output, "usage: mnemos init") {
				t.Fatal("help missing usage")
			}
			if !reflect.DeepEqual(before, initTree(t, home)) {
				t.Fatal("argument validation wrote user state")
			}
		})
	}
}

func TestInitRetainsIndependentSuccessAndJoinsAllFailures(t *testing.T) {
	home := initRuntimeHome(t)
	initWrite(t, filepath.Join(home, ".claude.json"), `{}`)
	initWrite(t, filepath.Join(home, ".claude", "settings.json"), `{invalid hooks`)
	initWrite(t, filepath.Join(home, ".cursor", "mcp.json"), `{invalid cursor`)
	initWrite(t, filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"), `{invalid windsurf`)
	initWrite(t, filepath.Join(home, ".pi", "agent", "settings.json"), `{}`)
	var err error
	output := captureStdout(t, func() { err = runInit(context.Background(), nil) })
	if err == nil {
		t.Fatal("partial installation returned success")
	}
	for _, part := range []string{"Cursor", "Windsurf", "remove three-tool", "Claude Code UserPromptSubmit hook"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("joined error omitted %q: %v", part, err)
		}
	}
	if strings.Contains(output, "restart your agent.") || !strings.Contains(output, "installation incomplete") {
		t.Fatalf("misleading status: %s", output)
	}
	data, readErr := os.ReadFile(filepath.Join(home, ".pi", "agent", "mcp-adapter.json"))
	if readErr != nil || !strings.Contains(string(data), "mnemos") {
		t.Fatalf("independent pi success discarded: %v %s", readErr, data)
	}
	data, readErr = os.ReadFile(filepath.Join(home, ".claude.json"))
	if readErr != nil || !strings.Contains(string(data), "mnemos") {
		t.Fatalf("independent Claude MCP success discarded: %v %s", readErr, data)
	}
	if data, _ = os.ReadFile(filepath.Join(home, ".claude", "settings.json")); string(data) != `{invalid hooks` {
		t.Fatal("failed hook file changed")
	}
}

func TestInitOwnedGuardMigrationPreservesSiblingHooksAndFields(t *testing.T) {
	home := initRuntimeHome(t)
	initWrite(t, filepath.Join(home, ".claude.json"), `{}`)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	old := "mcp__mnemos__mnemos_save|mcp__mnemos__mnemos_correct|mcp__mnemos__mnemos_convention"
	command := self + " hook pre-tool"
	user := map[string]any{"type": "command", "command": "user-hook --keep", "timeout": float64(27), "userField": "preserve"}
	otherExe := map[string]any{"type": "command", "command": "other-mnemos hook pre-tool"}
	cfg := map[string]any{"userSetting": true, "hooks": map[string]any{"PreToolUse": []any{
		map[string]any{"matcher": old, "customGroup": "preserve", "hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 5}, user}},
		map[string]any{"matcher": old, "hooks": []any{otherExe}},
		map[string]any{"matcher": old, "onlyOwnedExtra": "keep-empty-group", "hooks": []any{map[string]any{"type": "command", "command": command}}},
	}}}
	encoded, _ := json.Marshal(cfg)
	initWrite(t, filepath.Join(home, ".claude", "settings.json"), string(encoded))
	captureStdout(t, func() {
		if err := runInit(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	})
	first, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var actual map[string]any
	if err := json.Unmarshal(first, &actual); err != nil {
		t.Fatal(err)
	}
	groups := actual["hooks"].(map[string]any)["PreToolUse"].([]any)
	ownedOld, newOwned, userCount, otherCount, extra := 0, 0, 0, 0, 0
	for _, raw := range groups {
		group := raw.(map[string]any)
		matcher, _ := group["matcher"].(string)
		if group["onlyOwnedExtra"] == "keep-empty-group" {
			extra++
			if len(group["hooks"].([]any)) != 0 {
				t.Fatal("old owned command remains")
			}
		}
		for _, rawHook := range group["hooks"].([]any) {
			hook := rawHook.(map[string]any)
			switch hook["command"] {
			case command:
				if matcher == old {
					ownedOld++
				}
			case "user-hook --keep":
				userCount++
				if group["customGroup"] != "preserve" || !reflect.DeepEqual(hook, user) {
					t.Fatal("user hook/group fields changed")
				}
			case "other-mnemos hook pre-tool":
				otherCount++
				if matcher != old {
					t.Fatal("other executable migrated")
				}
			default:
				if matcher == old+"|mcp__mnemos__mnemos_checkpoint" && strings.HasSuffix(hook["command"].(string), "hook pre-tool") {
					newOwned++
				}
			}
		}
	}
	if ownedOld != 0 || newOwned != 1 || userCount != 1 || otherCount != 1 || extra != 1 || actual["userSetting"] != true {
		t.Fatalf("unexpected migration: %s", first)
	}
	captureStdout(t, func() {
		if err := runInit(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	})
	second, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if string(first) != string(second) {
		t.Fatal("repeat init rewrote hooks")
	}
}

func TestInitReusesPackageRouteWithoutGlobalDuplicate(t *testing.T) {
	home := initRuntimeHome(t)
	agent := filepath.Join(home, ".pi", "agent")
	name := "mnemos"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	oldCommand := filepath.Join(home, name)
	initWrite(t, oldCommand, "fixture")
	if err := os.Chmod(oldCommand, 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(agent, "package")
	manifest := `{"name":"memory","pi":{"mcp":"./mcp.json"}}`
	initWrite(t, filepath.Join(root, "package.json"), manifest)
	settings, _ := json.Marshal(map[string]any{"packages": []string{root}, "preserve": true})
	initWrite(t, filepath.Join(agent, "settings.json"), string(settings))
	mcp, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"custom-key": map[string]any{"command": oldCommand, "args": []string{"serve"}, "directTools": true, "toolPrefix": "mine"}}})
	initWrite(t, filepath.Join(root, "mcp.json"), string(mcp))
	captureStdout(t, func() {
		if err := runInit(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := os.Stat(filepath.Join(agent, "mcp-adapter.json")); !os.IsNotExist(err) {
		t.Fatal("init created a duplicate global route")
	}
	actual, _ := os.ReadFile(filepath.Join(root, "mcp.json"))
	var cfg map[string]any
	if err := json.Unmarshal(actual, &cfg); err != nil {
		t.Fatal(err)
	}
	server := cfg["mcpServers"].(map[string]any)["custom-key"].(map[string]any)
	self, _ := os.Executable()
	if server["command"] != self || server["directTools"] != true || server["toolPrefix"] != "mine" {
		t.Fatalf("incorrect route update: %s", actual)
	}
	for path, want := range map[string]string{filepath.Join(agent, "settings.json"): string(settings), filepath.Join(root, "package.json"): manifest} {
		actual, _ := os.ReadFile(path)
		if string(actual) != want {
			t.Fatalf("unexpected change: %s", path)
		}
	}
	before := initTree(t, home)
	captureStdout(t, func() {
		if err := runInit(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	})
	if !reflect.DeepEqual(before, initTree(t, home)) {
		t.Fatal("repeat init changed package state or created a duplicate")
	}
}

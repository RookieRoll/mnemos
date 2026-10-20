package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunDoctorAcceptsVerifiedLocalPiPackageWithoutGlobalEntry(t *testing.T) {
	agentDir, ctx := setupPiHomeForInit(t)
	root := filepath.Join(agentDir, "local-package")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(agentDir, "mnemos")
	if runtime.GOOS == "windows" {
		command += ".exe"
	}
	if err := os.WriteFile(command, []byte("fixture, never executed"), 0o700); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"mnemos": map[string]any{"command": command, "args": []string{"serve"}}}})
	settings, _ := json.Marshal(map[string]any{"packages": []string{root}})
	for path, data := range map[string][]byte{
		filepath.Join(root, "package.json"):      []byte(`{"name":"kls-mnemos-pi","pi":{"mcp":"./mcp.json"}}`),
		filepath.Join(root, "mcp.json"):          config,
		filepath.Join(agentDir, "settings.json"): settings,
	} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var doctorErr error
	out := captureStdout(t, func() { doctorErr = runDoctor(ctx, nil) })
	if doctorErr != nil {
		t.Fatalf("package-only doctor: %v\n%s", doctorErr, out)
	}
	if !strings.Contains(out, "✓ pi package MCP") || strings.Contains(out, "✗ pi") {
		t.Fatalf("package-only diagnostic: %s", out)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "mcp-adapter.json")); !os.IsNotExist(err) {
		t.Fatalf("doctor created a global adapter entry: %v", err)
	}
}

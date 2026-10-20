package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polyxmedia/mnemos/internal/config"
	"github.com/polyxmedia/mnemos/internal/workspacediagnostics"
)

func TestDoctorWorkspaceModeFlagRouting(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		handled bool
		wantErr bool
	}{
		{name: "ordinary doctor falls through", handled: false},
		{name: "json requires workspace mode", args: []string{"--json"}, handled: true, wantErr: true},
		{name: "unknown option", args: []string{"--workspaces", "--typo"}, handled: true, wantErr: true},
		{name: "positional argument", args: []string{"--workspaces", "extra"}, handled: true, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var output strings.Builder
			handled, err := doctorWorkspaceMode(context.Background(), tc.args, &output)
			if handled != tc.handled || (err != nil) != tc.wantErr {
				t.Fatalf("doctorWorkspaceMode() = (%v, %v), want (%v, err=%v)", handled, err, tc.handled, tc.wantErr)
			}
			if output.Len() != 0 {
				t.Fatalf("malformed or ordinary invocation wrote output: %q", output.String())
			}
		})
	}
}

func TestDoctorWorkspaceModeUsesReadOnlyConfigAndMissingStore(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "not-created", "mnemos.db")
	configPath := filepath.Join(dir, "config.toml")
	contents := "[storage]\npath = " + strconvQuote(storePath) + "\n[diagnostics.workspaces]\nmax_roots = 5\nmax_candidates = 3\ntimeout = \"1s\"\n"
	if err := os.WriteFile(configPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := config.ContextWithPath(context.Background(), configPath)
	var output strings.Builder
	handled, err := doctorWorkspaceMode(ctx, []string{"--workspaces", "--json"}, &output)
	if err != nil || !handled {
		t.Fatalf("doctorWorkspaceMode() = (%v, %v)", handled, err)
	}
	var report workspacediagnostics.Report
	if err := json.Unmarshal([]byte(output.String()), &report); err != nil {
		t.Fatalf("invalid JSON report: %v\n%s", err, output.String())
	}
	if report.Status != "no_history" || !report.ReadOnly || report.MaxRoots != 5 || report.MaxCandidates != 3 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if _, err := os.Stat(filepath.Dir(storePath)); !os.IsNotExist(err) {
		t.Fatalf("diagnostic created store directory or stat failed unexpectedly: %v", err)
	}
}

func strconvQuote(s string) string {
	encoded, _ := json.Marshal(s)
	return string(encoded)
}

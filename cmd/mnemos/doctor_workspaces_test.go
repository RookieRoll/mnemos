package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/config"
	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
	"github.com/polyxmedia/mnemos/internal/workspacediagnostics"
)

func TestDoctorWorkspaceModeFlagRouting(t *testing.T) {
	// Ordinary doctor now scans the default data directory; keep the scan on
	// an empty isolated home so the assertion sees no output.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("MNEMOS_CONFIG", "")
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

// createObservationDB creates (or opens) a real mnemos store at path and saves
// count observations into it. Zero observations leaves a migrated empty store.
func createObservationDB(t *testing.T, path string, count int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close %s: %v", path, err)
		}
	}()
	mem := memory.NewService(memory.Config{Store: db.Observations()})
	for i := 0; i < count; i++ {
		if _, err := mem.Save(ctx, memory.SaveInput{
			Title:   fmt.Sprintf("observation %d", i),
			Content: fmt.Sprintf("body %d", i),
			Type:    memory.TypePattern,
		}); err != nil {
			t.Fatalf("save observation %d: %v", i, err)
		}
	}
}

func TestScanStaleDatabases(t *testing.T) {
	cases := []struct {
		name           string
		setup          func(t *testing.T) (roots []string, configured string)
		wantStale      []string
		wantUnreadable []string
	}{
		{
			name: "populated candidate is reported",
			setup: func(t *testing.T) ([]string, string) {
				dir := t.TempDir()
				legacy := filepath.Join(dir, "legacy")
				current := filepath.Join(dir, "current")
				createObservationDB(t, filepath.Join(legacy, "mnemos.db"), 1)
				createObservationDB(t, filepath.Join(current, "mnemos.db"), 1)
				return []string{legacy, current}, filepath.Join(current, "mnemos.db")
			},
			wantStale: []string{"mnemos.db"},
		},
		{
			name: "configured store is not reported",
			setup: func(t *testing.T) ([]string, string) {
				dir := t.TempDir()
				configured := filepath.Join(dir, "mnemos.db")
				createObservationDB(t, configured, 2)
				return []string{dir}, configured
			},
		},
		{
			name: "empty migrated candidate is silent",
			setup: func(t *testing.T) ([]string, string) {
				dir := t.TempDir()
				createObservationDB(t, filepath.Join(dir, "empty.db"), 0)
				return []string{dir}, filepath.Join(dir, "configured.db")
			},
		},
		{
			name: "missing directories are silent",
			setup: func(t *testing.T) ([]string, string) {
				dir := t.TempDir()
				return []string{filepath.Join(dir, "absent"), filepath.Join(dir, "also-absent")}, filepath.Join(dir, "configured.db")
			},
		},
		{
			name: "overlapping roots report once",
			setup: func(t *testing.T) ([]string, string) {
				dir := t.TempDir()
				createObservationDB(t, filepath.Join(dir, "legacy.db"), 1)
				return []string{dir, dir, filepath.Join(dir, ".")}, filepath.Join(dir, "configured.db")
			},
			wantStale: []string{"legacy.db"},
		},
		{
			name: "unreadable candidate is reported and skipped",
			setup: func(t *testing.T) ([]string, string) {
				dir := t.TempDir()
				createObservationDB(t, filepath.Join(dir, "legacy.db"), 1)
				if err := os.WriteFile(filepath.Join(dir, "broken.db"), []byte("this is not sqlite"), 0o644); err != nil {
					t.Fatal(err)
				}
				return []string{dir}, filepath.Join(dir, "configured.db")
			},
			wantStale:      []string{"legacy.db"},
			wantUnreadable: []string{"broken.db"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			roots, configured := tc.setup(t)
			scan := scanStaleDatabases(context.Background(), configured, roots)
			var gotStale []string
			for _, finding := range scan.Stale {
				gotStale = append(gotStale, filepath.Base(finding.Path))
				if finding.LastWrite.IsZero() {
					t.Errorf("stale finding without a last-write time: %+v", finding)
				}
			}
			var gotUnreadable []string
			for _, finding := range scan.Unreadable {
				gotUnreadable = append(gotUnreadable, filepath.Base(finding.Path))
				if finding.Err == "" {
					t.Errorf("unreadable finding without an error: %+v", finding)
				}
			}
			if !reflect.DeepEqual(gotStale, tc.wantStale) {
				t.Errorf("stale = %v, want %v", gotStale, tc.wantStale)
			}
			if !reflect.DeepEqual(gotUnreadable, tc.wantUnreadable) {
				t.Errorf("unreadable = %v, want %v", gotUnreadable, tc.wantUnreadable)
			}
		})
	}
}

func TestScanStaleDatabaseLastWriteUsesWAL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")
	createObservationDB(t, path, 1)
	walPath := path + "-wal"
	if err := os.WriteFile(walPath, []byte("wal content"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	if err := os.Chtimes(walPath, future, future); err != nil {
		t.Fatal(err)
	}
	scan := scanStaleDatabases(context.Background(), filepath.Join(dir, "configured.db"), []string{dir})
	if len(scan.Stale) != 1 {
		t.Fatalf("stale findings = %d, want 1: %+v", len(scan.Stale), scan)
	}
	if got := scan.Stale[0].LastWrite.UTC(); !got.Equal(future) {
		t.Errorf("last write = %v, want the later -wal mtime %v", got, future)
	}
}

// snapshotTree records every top-level entry's size and modification time so a
// scan can be proven not to create, truncate or rewrite files.
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		out[entry.Name()] = fmt.Sprintf("%d/%d", info.Size(), info.ModTime().UnixNano())
	}
	return out
}

func TestScanStaleDatabasesLeavesFilesUntouched(t *testing.T) {
	dir := t.TempDir()
	createObservationDB(t, filepath.Join(dir, "populated.db"), 1)
	createObservationDB(t, filepath.Join(dir, "empty.db"), 0)
	if err := os.WriteFile(filepath.Join(dir, "broken.db"), []byte("not sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, dir)
	scan := scanStaleDatabases(context.Background(), filepath.Join(dir, "configured.db"), []string{dir})
	after := snapshotTree(t, dir)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("scan changed the directory:\nbefore=%v\nafter=%v", before, after)
	}
	if len(scan.Stale) != 1 || scan.Stale[0].Path != filepath.Join(dir, "populated.db") {
		t.Errorf("unexpected stale findings: %+v", scan.Stale)
	}
	if len(scan.Unreadable) != 1 || scan.Unreadable[0].Path != filepath.Join(dir, "broken.db") {
		t.Errorf("unexpected unreadable findings: %+v", scan.Unreadable)
	}
}

func TestRenderStaleDatabaseScan(t *testing.T) {
	when := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	scan := staleDatabaseScan{
		Stale:      []staleDatabaseFinding{{Path: filepath.Join(t.TempDir(), "a.db"), LastWrite: when}},
		Unreadable: []unreadableDatabaseFinding{{Path: "broken.db", Err: "file is not a database"}},
	}
	var output strings.Builder
	if err := renderStaleDatabaseScan(&output, scan); err != nil {
		t.Fatal(err)
	}
	rendered := output.String()
	for _, want := range []string{
		"stale DB at ",
		"a.db",
		"(last write 2026-10-26T12:00:00Z)",
		"unreadable DB at broken.db (file is not a database)",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered scan missing %q:\n%s", want, rendered)
		}
	}
}

func TestDoctorWorkspaceModeReportsStaleDatabase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("MNEMOS_CONFIG", "")
	configDir := filepath.Join(home, ".mnemos")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(home, "data", "main.db")
	createObservationDB(t, storePath, 1)
	createObservationDB(t, filepath.Join(configDir, "mnemos.db"), 1)
	contents := "[storage]\npath = " + strconvQuote(storePath) + "\n"
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	var output strings.Builder
	handled, err := doctorWorkspaceMode(context.Background(), nil, &output)
	if handled {
		t.Fatalf("ordinary doctor was routed as a special mode")
	}
	if err != nil {
		t.Fatalf("stale database changed doctor's error status: %v", err)
	}
	rendered := output.String()
	if !strings.Contains(rendered, "stale DB at ") || !strings.Contains(rendered, "mnemos.db") {
		t.Fatalf("stale database not reported:\n%s", rendered)
	}
	if strings.Contains(rendered, "main.db") {
		t.Fatalf("configured store was reported as stale:\n%s", rendered)
	}
}

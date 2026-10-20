package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/workspacediagnostics"
)

func TestWorkspaceDiagnosticsDoesNotCreateMissingStore(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "absent", "mnemos.db")
	snapshot, err := NewWorkspaceDiagnosticsReader(dbPath).Snapshot(context.Background(), 10)
	if err != nil || snapshot.Exists || snapshot.TotalRoots != 0 {
		t.Fatalf("Snapshot() = %+v, %v", snapshot, err)
	}
	if _, err := os.Stat(filepath.Dir(dbPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("parent directory was created or stat failed unexpectedly: %v", err)
	}
}

func TestWorkspaceDiagnosticsRejectsMissingWALSidecarsWithoutCreatingFiles(t *testing.T) {
	for _, missing := range []string{"-wal", "-shm"} {
		t.Run(missing, func(t *testing.T) {
			dir := t.TempDir()
			dbPath := filepath.Join(dir, "mnemos.db")
			cmd := exec.Command(os.Args[0], "-test.run=^TestWorkspaceDiagnosticsCrashFixture$")
			cmd.Env = append(os.Environ(), "MNEMOS_WORKSPACE_WAL_FIXTURE="+dbPath)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("create committed WAL fixture: %v\n%s", err, output)
			}
			for _, suffix := range []string{"", "-wal", "-shm"} {
				if _, err := os.Stat(dbPath + suffix); err != nil {
					t.Fatalf("expected fixture file %q: %v", suffix, err)
				}
			}
			if err := os.Remove(dbPath + missing); err != nil {
				t.Fatalf("remove test sidecar %s: %v", missing, err)
			}

			before := fileSnapshot(t, dir)
			_, err := NewWorkspaceDiagnosticsReader(dbPath).Snapshot(context.Background(), 10)
			if err == nil || !strings.Contains(err.Error(), "WAL read-only limitation") || !strings.Contains(err.Error(), missing) {
				t.Fatalf("Snapshot() error = %v, want explicit missing %s limitation", err, missing)
			}
			if got := fileSnapshot(t, dir); !sameFileSnapshot(before, got) {
				t.Fatalf("diagnostic changed files after refusing missing %s: before=%v after=%v", missing, before, got)
			}
			if _, err := os.Stat(dbPath + missing); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("diagnostic created missing sidecar %s: %v", missing, err)
			}
		})
	}
}

// TestWorkspaceDiagnosticsCrashFixture leaves a committed WAL in place by
// exiting the child process without closing SQLite's open connection.
func TestWorkspaceDiagnosticsCrashFixture(t *testing.T) {
	dbPath := os.Getenv("MNEMOS_WORKSPACE_WAL_FIXTURE")
	if dbPath == "" {
		return
	}
	db, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open fixture store: %v", err)
	}
	if _, err := db.SQL().Exec(`PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatalf("disable fixture autocheckpoint: %v", err)
	}
	if _, err := db.SQL().Exec(`INSERT INTO sessions(id,agent_id,project,goal,status,started_at) VALUES('fixture-session','agent','project','private-goal','ok',?)`, time.Now().UTC()); err != nil {
		t.Fatalf("insert fixture session: %v", err)
	}
	if _, err := db.SQL().Exec(`INSERT INTO session_continuity_bindings(session_id,agent_id,workspace_key,workspace_root,harness_kind,start_key,start_request_hash) VALUES('fixture-session','agent','key','/fixture/project','harness','private-retry-key','private-hash')`); err != nil {
		t.Fatalf("insert fixture binding: %v", err)
	}
	// Deliberately bypass database/sql cleanup to simulate process termination.
	os.Exit(0)
}

func TestWorkspaceDiagnosticsReadsCommittedWALWithoutChangingFiles(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mnemos.db")
	db, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL().Exec(`PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`INSERT INTO sessions(id,agent_id,project,goal,status,started_at) VALUES('s1','a','p','private-goal','ok',?)`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`INSERT INTO session_continuity_bindings(session_id,agent_id,workspace_key,workspace_root,harness_kind,start_key,start_request_hash) VALUES('s1','a','key','/missing/project','h','secret-retry-key','secret-hash')`); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(dbPath + suffix); err != nil {
			t.Fatalf("expected %s before diagnostic: %v", suffix, err)
		}
	}
	beforeCounters := readDiagnosticCounters(t, db.SQL())
	beforeFiles := fileSnapshot(t, dir)
	snapshot, err := NewWorkspaceDiagnosticsReader(dbPath).Snapshot(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.TotalRoots != 1 || len(snapshot.Roots) != 1 || snapshot.Roots[0].Counts.Sessions != 1 {
		t.Fatalf("committed WAL metadata not visible: %+v", snapshot)
	}
	if got := fileSnapshot(t, dir); !sameFileSnapshot(beforeFiles, got) {
		t.Fatalf("database/WAL/SHM bytes or directory entries changed:\nbefore=%v\nafter=%v", beforeFiles, got)
	}
	if after := readDiagnosticCounters(t, db.SQL()); beforeCounters != after {
		t.Fatalf("business counters changed: before=%v after=%v", beforeCounters, after)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-goal", "secret-retry-key", "secret-hash", "secret"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("diagnostic output leaked %q", secret)
		}
	}
}

func TestWorkspaceDiagnosticsAggregatesRetryAndVisibilityWithoutPrivateFields(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mnemos.db")
	db, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.SQL().Exec(`PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, session := range []struct {
		id, root, key, goal, start string
		ended                      any
		visibility                 string
		payload                    any
	}{
		{id: "s1", root: "/projects/a", key: "workspace-a", goal: "private-goal-one", start: "private-retry-key", visibility: "workspace", payload: `{"summary":"private-checkpoint-one"}`},
		{id: "s2", root: "/projects/b", key: "workspace-b", goal: "private-goal-two", start: "private-retry-key", ended: now, visibility: "owner", payload: nil},
	} {
		if _, err := db.SQL().Exec(`INSERT INTO sessions(id,agent_id,project,goal,status,started_at,ended_at) VALUES(?, 'agent', 'project', ?, 'ok', ?, ?)`, session.id, session.goal, now, session.ended); err != nil {
			t.Fatal(err)
		}
		if _, err := db.SQL().Exec(`INSERT INTO session_continuity_bindings(session_id,agent_id,workspace_key,workspace_root,harness_kind,start_key,start_request_hash) VALUES(?, 'agent', ?, ?, 'harness', ?, 'private-retry-hash')`, session.id, session.key, session.root, session.start); err != nil {
			t.Fatal(err)
		}
		if _, err := db.SQL().Exec(`INSERT INTO session_checkpoints(session_id,revision,payload,visibility,updated_at,last_request_id,last_request_hash) VALUES(?,1,?,?,?,'private-request-id','private-request-hash')`, session.id, session.payload, session.visibility, now); err != nil {
			t.Fatal(err)
		}
	}
	beforeCounters := readDiagnosticCounters(t, db.SQL())
	beforeFiles := fileSnapshot(t, dir)
	snapshot, err := NewWorkspaceDiagnosticsReader(dbPath).Snapshot(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.TotalRoots != 2 || len(snapshot.Roots) != 2 || len(snapshot.RetryOverlap) != 1 || snapshot.RetryOverlap[0].Count != 1 {
		t.Fatalf("unexpected aggregate snapshot: %+v", snapshot)
	}
	if snapshot.Roots[0].Counts.OpenSessions != 1 || snapshot.Roots[0].Counts.WorkspaceCheckpoints != 1 || snapshot.Roots[1].Counts.ClearMarkers != 1 || snapshot.Roots[1].Counts.OwnerCheckpoints != 0 {
		t.Fatalf("unexpected per-root counts: %+v", snapshot.Roots)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-goal", "private-checkpoint", "private-retry", "private-request"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("diagnostic output leaked %q: %s", secret, encoded)
		}
	}
	if got := fileSnapshot(t, dir); !sameFileSnapshot(beforeFiles, got) {
		t.Fatalf("diagnostic changed DB/WAL or sidecar presence/size: before=%v after=%v", beforeFiles, got)
	}
	if after := readDiagnosticCounters(t, db.SQL()); beforeCounters != after {
		t.Fatalf("business counters changed: before=%v after=%v", beforeCounters, after)
	}
}

func TestWorkspaceDiagnosticsRejectsOldSchemaWithoutMigration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations(version) VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before := fileSnapshot(t, filepath.Dir(dbPath))
	_, err = NewWorkspaceDiagnosticsReader(dbPath).Snapshot(context.Background(), 10)
	var unsupported *workspacediagnostics.UnsupportedSchemaError
	if !errors.As(err, &unsupported) {
		t.Fatalf("Snapshot() error = %v, want UnsupportedSchemaError", err)
	}
	if got := fileSnapshot(t, filepath.Dir(dbPath)); !sameFileSnapshot(before, got) {
		t.Fatalf("old schema was changed or migrated: before=%v after=%v", before, got)
	}
}

type diagnosticFile struct {
	name string
	size int64
	hash [32]byte
}

func fileSnapshot(t *testing.T, dir string) []diagnosticFile {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]diagnosticFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("unexpected subdirectory %q", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		file := diagnosticFile{name: entry.Name(), size: int64(len(data))}
		if !strings.HasSuffix(entry.Name(), "-shm") {
			file.hash = sha256.Sum256(data)
		}
		out = append(out, file)
	}
	return out
}

func sameFileSnapshot(a, b []diagnosticFile) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].name != b[i].name || a[i].size != b[i].size || a[i].hash != b[i].hash {
			return false
		}
	}
	return true
}

func readDiagnosticCounters(t *testing.T, db *sql.DB) [3]int64 {
	t.Helper()
	var out [3]int64
	if err := db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM sessions),
		(SELECT COUNT(*) FROM session_continuity_bindings),
		(SELECT COUNT(*) FROM session_checkpoints)`).Scan(&out[0], &out[1], &out[2]); err != nil {
		t.Fatal(err)
	}
	return out
}

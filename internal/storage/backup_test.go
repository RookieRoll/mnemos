package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestBackup_CreatesConsistentSnapshot keeps writing through the live database
// while a snapshot is taken. VACUUM INTO must observe one point in time, so the
// snapshot opens cleanly, keeps every row committed before the backup started,
// and adds none committed after it finished.
func TestBackup_CreatesConsistentSnapshot(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	live, err := Open(ctx, filepath.Join(dir, "live.db"))
	if err != nil {
		t.Fatalf("open live database: %v", err)
	}
	defer live.Close()

	const seeded = 32
	for i := 0; i < seeded; i++ {
		insertObservation(t, live, fmt.Sprintf("seed-%02d", i))
	}
	before := observationCount(t, live)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var writeErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := live.Observations().Insert(ctx, observationFixture(fmt.Sprintf("live-%03d", i))); err != nil {
				writeErr = err
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	dest := filepath.Join(dir, "snapshot.db")
	size, err := live.Backup(ctx, dest)
	close(stop)
	wg.Wait()
	if writeErr != nil {
		t.Fatalf("concurrent write during backup: %v", writeErr)
	}
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if size <= 0 {
		t.Fatalf("backup reported size %d, want > 0", size)
	}
	after := observationCount(t, live)

	snapshot, err := Open(ctx, dest)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer snapshot.Close()

	snapshotRows := observationCount(t, snapshot)
	if snapshotRows < before {
		t.Fatalf("snapshot holds %d observations, fewer than the %d committed before the backup", snapshotRows, before)
	}
	if snapshotRows > after {
		t.Fatalf("snapshot holds %d observations, more than the live database's %d", snapshotRows, after)
	}
	var integrity string
	if err := snapshot.SQL().QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		t.Fatalf("snapshot integrity_check: %v", err)
	}
	if integrity != "ok" {
		t.Fatalf("snapshot integrity_check = %q, want ok", integrity)
	}
	var kept int
	if err := snapshot.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM observations WHERE id LIKE 'seed-%'`).Scan(&kept); err != nil {
		t.Fatalf("count seeded rows in snapshot: %v", err)
	}
	if kept != seeded {
		t.Fatalf("snapshot holds %d of the %d seeded observations", kept, seeded)
	}
}

// TestBackup_TargetExists pins the refusal contract: an occupied destination is
// reported with the documented message, nothing is written over it, and no
// snapshot is produced.
func TestBackup_TargetExists(t *testing.T) {
	ctx := context.Background()
	live, err := Open(ctx, filepath.Join(t.TempDir(), "live.db"))
	if err != nil {
		t.Fatalf("open live database: %v", err)
	}
	defer live.Close()

	const original = "not a mnemos database"
	cases := []struct {
		name    string
		setup   func(t *testing.T, dest string)
		content string
	}{
		{
			name: "existing file",
			setup: func(t *testing.T, dest string) {
				if err := os.WriteFile(dest, []byte(original), 0o644); err != nil {
					t.Fatalf("seed target: %v", err)
				}
			},
			content: original,
		},
		{
			name: "existing directory",
			setup: func(t *testing.T, dest string) {
				if err := os.Mkdir(dest, 0o755); err != nil {
					t.Fatalf("seed target: %v", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "occupied.db")
			tc.setup(t, dest)

			size, err := live.Backup(ctx, dest)
			if err == nil {
				t.Fatalf("backup over an existing target must fail")
			}
			if size != 0 {
				t.Fatalf("refused backup reported %d bytes", size)
			}
			if !errors.Is(err, ErrBackupTargetExists) {
				t.Fatalf("error %v is not ErrBackupTargetExists", err)
			}
			if want := "storage: backup target already exists: " + dest; err.Error() != want {
				t.Fatalf("error = %q, want %q", err.Error(), want)
			}
			if tc.content == "" {
				return
			}
			got, readErr := os.ReadFile(dest)
			if readErr != nil {
				t.Fatalf("read target after refusal: %v", readErr)
			}
			if string(got) != tc.content {
				t.Fatalf("refused backup modified the existing file: %q", got)
			}
		})
	}
}

// observationCount returns the number of observation rows in db.
func observationCount(t *testing.T, db *DB) int64 {
	t.Helper()
	var n int64
	if err := db.SQL().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM observations`).Scan(&n); err != nil {
		t.Fatalf("count observations: %v", err)
	}
	return n
}

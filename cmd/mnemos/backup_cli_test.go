package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func TestBackupCreatesConsistentSnapshot(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()

	d, err := loadDeps(ctx)
	if err != nil {
		t.Fatalf("loadDeps: %v", err)
	}
	for i, title := range []string{"one", "two", "three"} {
		if _, err := d.mem.Save(ctx, memory.SaveInput{
			Title: title, Content: "snapshot fixture " + title,
			Type: memory.TypeContext, Project: "mnemos", Importance: 5 + i,
		}); err != nil {
			t.Fatalf("save %s: %v", title, err)
		}
	}
	d.close()

	dest := filepath.Join(t.TempDir(), "snapshot.db")
	out := captureStdout(t, func() {
		if err := runBackup(ctx, []string{dest}); err != nil {
			t.Fatalf("backup: %v", err)
		}
	})
	if !strings.Contains(out, "wrote a consistent snapshot") || !strings.Contains(out, dest) {
		t.Fatalf("backup output must name the destination: %q", out)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("snapshot missing: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("snapshot is empty")
	}

	// The snapshot opens as a normal store and holds the same rows.
	snap, err := storage.Open(ctx, dest)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer snap.Close()
	st, err := snap.Observations().Stats(ctx)
	if err != nil {
		t.Fatalf("snapshot stats: %v", err)
	}
	if st.Observations != 3 {
		t.Fatalf("snapshot observations = %d, want 3", st.Observations)
	}
}

func TestBackupRefusesExistingTarget(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()
	dest := filepath.Join(t.TempDir(), "exists.db")
	if err := os.WriteFile(dest, []byte("do not touch"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runBackup(ctx, []string{dest})
	if err == nil {
		t.Fatal("backup over an existing file must fail")
	}
	if !errors.Is(err, storage.ErrBackupTargetExists) {
		t.Fatalf("error = %v, want ErrBackupTargetExists", err)
	}
	if !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), dest) {
		t.Fatalf("error must name the target and the reason: %v", err)
	}
	body, readErr := os.ReadFile(dest)
	if readErr != nil || string(body) != "do not touch" {
		t.Fatalf("existing target was modified: %q %v", body, readErr)
	}
}

func TestBackupUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no path", []string{"backup"}},
		{"two paths", []string{"backup", "a.db", "b.db"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homeWithConfig(t, "")
			var stdout, stderr bytes.Buffer
			if code := dispatch(context.Background(), tc.args, &stdout, &stderr); code != exitUsage {
				t.Fatalf("exit = %d, want %d (stderr %q)", code, exitUsage, stderr.String())
			}
		})
	}
}

func TestStatsOpenBacklogLine(t *testing.T) {
	homeWithConfig(t, "")
	ctx := context.Background()

	empty := captureStdout(t, func() {
		if err := runStats(ctx, nil); err != nil {
			t.Fatalf("stats: %v", err)
		}
	})
	if !strings.Contains(empty, "open sessions: 0") {
		t.Fatalf("empty store must report a zero backlog: %q", empty)
	}

	now := time.Now().UTC()
	restoreSession(t, ctx, "backlog-old", "mnemos", now.Add(-48*time.Hour), nil, session.StatusOK)
	restoreSession(t, ctx, "backlog-new-1", "mnemos", now, nil, session.StatusOK)
	restoreSession(t, ctx, "backlog-new-2", "other", now, nil, session.StatusOK)

	out := captureStdout(t, func() {
		if err := runStats(ctx, nil); err != nil {
			t.Fatalf("stats: %v", err)
		}
	})
	if !strings.Contains(out, "open sessions: 3 (oldest 2d)") {
		t.Fatalf("backlog line missing or wrong: %q", out)
	}
}

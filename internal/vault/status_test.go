package vault_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
	"github.com/polyxmedia/mnemos/internal/vault"
)

// fakeCounter is the status projection's store probe without a database.
type fakeCounter struct {
	count int
	err   error
}

type saveDuringExport struct {
	memory.Store
	afterRead func()
}

func (s saveDuringExport) ListByProject(ctx context.Context, agent, project string, typ memory.ObsType, limit int) ([]memory.Observation, error) {
	rows, err := s.Store.ListByProject(ctx, agent, project, typ, limit)
	if err == nil {
		s.afterRead()
	}
	return rows, err
}

func TestExportKeepsConcurrentSavePending(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "m.db")
	db, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mem := memory.NewService(memory.Config{Store: db.Observations()})
	root := t.TempDir()
	obs := saveDuringExport{Store: db.Observations(), afterRead: func() {
		if _, err := mem.Save(ctx, memory.SaveInput{Title: "new", Content: "arrived after observation read", Type: memory.TypeContext}); err != nil {
			t.Fatal(err)
		}
	}}
	ex := vault.NewExporter(vault.Config{Root: root, StorePath: path, Obs: obs, Sessions: db.Sessions(), Skills: db.Skills()})
	stats, err := ex.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Observations != 0 {
		t.Fatal("test observation unexpectedly entered export snapshot")
	}
	status := vault.InspectStatus(ctx, vault.StatusOptions{Root: root, StorePath: path})
	if !status.PendingKnown || status.Pending != 1 {
		t.Fatalf("pending=%d known=%v: %s", status.Pending, status.PendingKnown, status.PendingError)
	}
}

func (f fakeCounter) CountObservationsSince(context.Context, time.Time) (int, error) {
	return f.count, f.err
}

func TestExportManifestRoundTripAndAtomicReplace(t *testing.T) {
	root := t.TempDir()
	at := time.Date(2026, 10, 26, 12, 0, 0, 123456789, time.UTC)
	first := vault.ExportManifest{ExportedAt: at, Observations: 7, Store: filepath.Join(root, "mnemos.db")}
	if err := vault.WriteExportManifest(root, first); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	got, seen, err := vault.ReadExportManifest(root)
	if err != nil || !seen {
		t.Fatalf("read manifest: seen=%v err=%v", seen, err)
	}
	if !got.ExportedAt.Equal(at) || got.Observations != 7 || got.Store != first.Store {
		t.Fatalf("manifest round-trip mismatch: %+v", got)
	}
	// The file is plain JSON with the documented keys.
	raw, err := os.ReadFile(filepath.Join(root, vault.ExportManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}
	for _, key := range []string{"exported_at", "observations", "store"} {
		if _, ok := object[key]; !ok {
			t.Errorf("manifest missing key %q: %s", key, raw)
		}
	}
	// A second write replaces the first atomically and leaves no temp files.
	second := vault.ExportManifest{ExportedAt: at.Add(time.Hour), Observations: 9, Store: "other.db"}
	if err := vault.WriteExportManifest(root, second); err != nil {
		t.Fatalf("replace manifest: %v", err)
	}
	got, seen, err = vault.ReadExportManifest(root)
	if err != nil || !seen || !got.ExportedAt.Equal(second.ExportedAt) || got.Observations != 9 {
		t.Fatalf("replaced manifest mismatch: %+v seen=%v err=%v", got, seen, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Errorf("temp artifact left behind: %s", entry.Name())
		}
	}
}

func TestFailedExportKeepsPreviousManifest(t *testing.T) {
	root := t.TempDir()
	previous := vault.ExportManifest{
		ExportedAt:   time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC),
		Observations: 3,
		Store:        "previous.db",
	}
	if err := vault.WriteExportManifest(root, previous); err != nil {
		t.Fatal(err)
	}
	// A regular file where the observations directory belongs makes the
	// export fail before any file is written, so the manifest must survive.
	if err := os.WriteFile(filepath.Join(root, "observations"), []byte("blocked"), 0o644); err != nil {
		t.Fatal(err)
	}
	ex := vault.NewExporter(vault.Config{Root: root})
	if _, err := ex.ExportAll(context.Background()); err == nil {
		t.Fatal("export unexpectedly succeeded")
	}
	got, seen, err := vault.ReadExportManifest(root)
	if err != nil || !seen {
		t.Fatalf("read manifest: seen=%v err=%v", seen, err)
	}
	if !got.ExportedAt.Equal(previous.ExportedAt) || got.Observations != previous.Observations {
		t.Fatalf("failed export changed the manifest: %+v", got)
	}
}

func TestExportAllWritesManifest(t *testing.T) {
	ex, mem, _, _, root := newVault(t)
	ctx := context.Background()
	if _, err := mem.Save(ctx, memory.SaveInput{
		Title:   "export me",
		Content: "vault provenance",
		Type:    memory.TypePattern,
	}); err != nil {
		t.Fatal(err)
	}
	stats, err := ex.ExportAll(ctx)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	manifest, seen, err := vault.ReadExportManifest(root)
	if err != nil || !seen {
		t.Fatalf("manifest: seen=%v err=%v", seen, err)
	}
	if manifest.Observations != stats.Observations {
		t.Errorf("manifest observations = %d, want %d", manifest.Observations, stats.Observations)
	}
	if manifest.Store == "" {
		t.Error("manifest store is empty")
	}
	if manifest.ExportedAt.IsZero() {
		t.Error("manifest exported_at is zero")
	}
}

func TestInspectStatusNeverExported(t *testing.T) {
	cases := []struct {
		name      string
		missing   bool
		counter   vault.ObservationCounter
		wantDir   bool
		wantKnown bool
		wantCount int
	}{
		{
			name:      "missing directory reports all observations pending",
			missing:   true,
			counter:   fakeCounter{count: 4},
			wantDir:   false,
			wantKnown: true,
			wantCount: 4,
		},
		{
			name:      "existing empty vault reports all observations pending",
			counter:   fakeCounter{count: 2},
			wantDir:   true,
			wantKnown: true,
			wantCount: 2,
		},
		{
			name:      "unreadable store degrades to unknown",
			counter:   fakeCounter{err: fmt.Errorf("disk unreadable")},
			wantDir:   true,
			wantKnown: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "vault")
			if !tc.missing {
				if err := os.MkdirAll(root, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			status := vault.InspectStatus(context.Background(), vault.StatusOptions{
				Root: root, StorePath: "unused.db", Counter: tc.counter,
				Now: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC),
			})
			if status.DirExists != tc.wantDir {
				t.Errorf("DirExists = %v, want %v", status.DirExists, tc.wantDir)
			}
			if status.ManifestSeen || !status.LastExportedAt.IsZero() {
				t.Errorf("never-exported vault reports a manifest: %+v", status)
			}
			if status.PendingKnown != tc.wantKnown || status.Pending != tc.wantCount {
				t.Errorf("pending = (%d, known=%v), want (%d, known=%v)", status.Pending, status.PendingKnown, tc.wantCount, tc.wantKnown)
			}
			if status.WatcherRunning {
				t.Error("watcher reported running without a heartbeat")
			}
		})
	}
}

func TestInspectStatusWatcherFreshness(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		heartbeat  *vault.WatchHeartbeat
		interval   time.Duration
		wantSeen   bool
		wantActive bool
	}{
		{
			name:     "no heartbeat",
			interval: 10 * time.Second,
		},
		{
			name:       "fresh heartbeat inside the floor",
			heartbeat:  &vault.WatchHeartbeat{PID: 7, HeartbeatAt: now.Add(-5 * time.Second)},
			interval:   10 * time.Second,
			wantSeen:   true,
			wantActive: true,
		},
		{
			name:       "heartbeat older than the thirty-second floor",
			heartbeat:  &vault.WatchHeartbeat{PID: 7, HeartbeatAt: now.Add(-31 * time.Second)},
			interval:   10 * time.Second,
			wantSeen:   true,
			wantActive: false,
		},
		{
			name:       "heartbeat inside three configured ticks",
			heartbeat:  &vault.WatchHeartbeat{PID: 7, HeartbeatAt: now.Add(-2 * time.Minute)},
			interval:   time.Minute,
			wantSeen:   true,
			wantActive: true,
		},
		{
			name:       "heartbeat just past three configured ticks",
			heartbeat:  &vault.WatchHeartbeat{PID: 7, HeartbeatAt: now.Add(-3*time.Minute - time.Second)},
			interval:   time.Minute,
			wantSeen:   true,
			wantActive: false,
		},
		{
			name:       "future heartbeat is still running",
			heartbeat:  &vault.WatchHeartbeat{PID: 7, HeartbeatAt: now.Add(time.Minute)},
			interval:   time.Minute,
			wantSeen:   true,
			wantActive: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.heartbeat != nil {
				beat := *tc.heartbeat
				beat.StartedAt = now.Add(-time.Hour)
				if err := vault.WriteWatchHeartbeat(root, beat); err != nil {
					t.Fatal(err)
				}
			}
			status := vault.InspectStatus(context.Background(), vault.StatusOptions{
				Root: root, Interval: tc.interval, Now: now,
				Counter: fakeCounter{count: 0},
			})
			if status.HeartbeatSeen != tc.wantSeen {
				t.Errorf("HeartbeatSeen = %v, want %v", status.HeartbeatSeen, tc.wantSeen)
			}
			if status.WatcherRunning != tc.wantActive {
				t.Errorf("WatcherRunning = %v, want %v", status.WatcherRunning, tc.wantActive)
			}
			if tc.wantSeen && !status.HeartbeatAt.Equal(tc.heartbeat.HeartbeatAt) {
				t.Errorf("HeartbeatAt = %v, want %v", status.HeartbeatAt, tc.heartbeat.HeartbeatAt)
			}
		})
	}
}

func TestInspectStatusExportedAndCorruptArtifacts(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	exportedAt := now.Add(-time.Hour)
	root := t.TempDir()
	if err := vault.WriteExportManifest(root, vault.ExportManifest{
		ExportedAt: exportedAt, Observations: 5, Store: "store.db",
	}); err != nil {
		t.Fatal(err)
	}
	status := vault.InspectStatus(context.Background(), vault.StatusOptions{
		Root: root, Interval: time.Minute, Now: now,
		Counter: fakeCounter{count: 0},
	})
	if !status.ManifestSeen || !status.LastExportedAt.Equal(exportedAt) || status.ExportedObservations != 5 || status.ExportedStore != "store.db" {
		t.Fatalf("export provenance mismatch: %+v", status)
	}
	if !status.PendingKnown || status.Pending != 0 {
		t.Fatalf("pending = (%d, known=%v), want (0, true)", status.Pending, status.PendingKnown)
	}

	// A corrupt manifest is exposed as an error and suppresses the watermark,
	// so pending cannot silently fall back to "all observations".
	if err := os.WriteFile(filepath.Join(root, vault.ExportManifestFile), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	status = vault.InspectStatus(context.Background(), vault.StatusOptions{
		Root: root, Interval: time.Minute, Now: now,
		Counter: fakeCounter{count: 2},
	})
	if status.ManifestError == "" || status.PendingKnown {
		t.Fatalf("corrupt manifest should be unknown: %+v", status)
	}

	// A corrupt heartbeat is visible but never running.
	if err := os.WriteFile(filepath.Join(root, vault.WatchHeartbeatFile), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	status = vault.InspectStatus(context.Background(), vault.StatusOptions{
		Root: root, Interval: time.Minute, Now: now,
		Counter: fakeCounter{count: 2},
	})
	if status.HeartbeatError == "" || status.WatcherRunning {
		t.Fatalf("corrupt heartbeat should be stopped with an error: %+v", status)
	}
}

func TestInspectStatusCreatesNothing(t *testing.T) {
	root := t.TempDir()
	before, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	vault.InspectStatus(context.Background(), vault.StatusOptions{
		Root: root, StorePath: filepath.Join(root, "absent.db"),
		Interval: time.Minute, Counter: fakeCounter{count: 1},
	})
	after, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("status created files: before=%v after=%v", before, after)
	}
	missing := filepath.Join(root, "missing")
	vault.InspectStatus(context.Background(), vault.StatusOptions{Root: missing, Counter: fakeCounter{count: 1}})
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("status created the missing vault directory: %v", err)
	}
}

func TestStoreCounterPendingBoundary(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	storePath := filepath.Join(dir, "mnemos.db")
	db, err := storage.Open(ctx, storePath)
	if err != nil {
		t.Fatal(err)
	}
	// The store's Invalidate path stamps invalidated_at from the real clock
	// (it deliberately preserves sub-second precision), so the seeded save
	// times sit in the past and the test clock never outruns real time.
	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	clock := base
	mem := memory.NewService(memory.Config{
		Store: db.Observations(),
		Clock: func() time.Time { return clock },
	})
	first, err := mem.Save(ctx, memory.SaveInput{Title: "first", Content: "first body", Type: memory.TypePattern})
	if err != nil {
		t.Fatal(err)
	}
	clock = base.Add(time.Minute)
	if _, err := mem.Save(ctx, memory.SaveInput{Title: "second", Content: "second body", Type: memory.TypePattern}); err != nil {
		t.Fatal(err)
	}
	// The watermark between the second save and the invalidation isolates the
	// invalidated-but-not-created contribution to pending.
	gap := time.Now().UTC().Add(-time.Second)
	if err := mem.Invalidate(ctx, first.Observation.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		since time.Time
		want  int
	}{
		{name: "never exported counts every row once", since: time.Time{}, want: 2},
		{name: "before both created rows", since: base.Add(-time.Hour), want: 2},
		{name: "after creation but before invalidation", since: gap, want: 1},
		{name: "after every write", since: time.Now().UTC().Add(time.Minute), want: 0},
	}
	counter := vault.StoreCounter{Path: storePath}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := counter.CountObservationsSince(ctx, tc.since)
			if err != nil {
				t.Fatalf("count: %v", err)
			}
			if got != tc.want {
				t.Errorf("CountObservationsSince(%v) = %d, want %d", tc.since, got, tc.want)
			}
		})
	}

	// The full projection pairs the manifest watermark with the store count.
	root := t.TempDir()
	if err := vault.WriteExportManifest(root, vault.ExportManifest{
		ExportedAt: gap, Observations: 1, Store: storePath,
	}); err != nil {
		t.Fatal(err)
	}
	status := vault.InspectStatus(ctx, vault.StatusOptions{
		Root: root, StorePath: storePath, Interval: time.Minute,
		Now: time.Now().UTC().Add(time.Minute),
	})
	if !status.PendingKnown || status.Pending != 1 {
		t.Fatalf("status pending = (%d, known=%v), want (1, true) [%s]", status.Pending, status.PendingKnown, status.PendingError)
	}
}

func TestStoreCounterUnreadableStore(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, "broken.db")
	if err := os.WriteFile(broken, []byte("this is not sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (vault.StoreCounter{Path: broken}).CountObservationsSince(context.Background(), time.Time{}); err == nil {
		t.Fatal("expected an error for a corrupt store")
	}
	status := vault.InspectStatus(context.Background(), vault.StatusOptions{
		Root: t.TempDir(), StorePath: filepath.Join(dir, "absent.db"),
	})
	if status.PendingKnown {
		t.Fatalf("missing store reported a known pending count: %+v", status)
	}
}

func TestStoreCounterCountsOneNewObservation(t *testing.T) {
	ctx := context.Background()
	storePath := filepath.Join(t.TempDir(), "mnemos.db")
	db, err := storage.Open(ctx, storePath)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	clock := base
	mem := memory.NewService(memory.Config{
		Store: db.Observations(),
		Clock: func() time.Time { return clock },
	})
	if _, err := mem.Save(ctx, memory.SaveInput{Title: "before export", Content: "before", Type: memory.TypePattern}); err != nil {
		t.Fatal(err)
	}
	exportedAt := base.Add(30 * time.Second)
	clock = base.Add(time.Minute)
	if _, err := mem.Save(ctx, memory.SaveInput{Title: "after export", Content: "after", Type: memory.TypePattern}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	count, err := (vault.StoreCounter{Path: storePath}).CountObservationsSince(ctx, exportedAt)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("pending = %d, want the one observation saved after the export", count)
	}
}

func TestInspectStatusKeepsProvenanceWhenStoreUnreadable(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	if err := vault.WriteExportManifest(root, vault.ExportManifest{
		ExportedAt: now.Add(-time.Hour), Observations: 4, Store: "store.db",
	}); err != nil {
		t.Fatal(err)
	}
	if err := vault.WriteWatchHeartbeat(root, vault.WatchHeartbeat{
		PID: 11, StartedAt: now.Add(-time.Hour), HeartbeatAt: now.Add(-time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	status := vault.InspectStatus(context.Background(), vault.StatusOptions{
		Root: root, StorePath: filepath.Join(root, "absent.db"), Interval: time.Minute, Now: now,
	})
	if status.PendingKnown {
		t.Fatalf("unreadable store should make pending unknown: %+v", status)
	}
	if !status.ManifestSeen || !status.LastExportedAt.Equal(now.Add(-time.Hour)) || status.ExportedObservations != 4 {
		t.Fatalf("export state lost when the store is unreadable: %+v", status)
	}
	if !status.WatcherRunning || status.HeartbeatPID != 11 {
		t.Fatalf("watcher state lost when the store is unreadable: %+v", status)
	}
}

func TestWatchHeartbeatLatestWriterWins(t *testing.T) {
	root := t.TempDir()
	started := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	if err := vault.WriteWatchHeartbeat(root, vault.WatchHeartbeat{PID: 1, StartedAt: started, HeartbeatAt: started}); err != nil {
		t.Fatal(err)
	}
	if err := vault.WriteWatchHeartbeat(root, vault.WatchHeartbeat{PID: 2, StartedAt: started, HeartbeatAt: started.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	heartbeat, seen, err := vault.ReadWatchHeartbeat(root)
	if err != nil || !seen {
		t.Fatalf("read heartbeat: seen=%v err=%v", seen, err)
	}
	if heartbeat.PID != 2 || !heartbeat.HeartbeatAt.Equal(started.Add(time.Second)) {
		t.Fatalf("heartbeat does not reflect the latest writer: %+v", heartbeat)
	}
	// The first watcher exiting must not erase the second watcher's record.
	if err := vault.RemoveWatchHeartbeat(root, 1); err != nil {
		t.Fatal(err)
	}
	if _, seen, err := vault.ReadWatchHeartbeat(root); err != nil || !seen {
		t.Fatalf("non-owner removal erased the heartbeat: seen=%v err=%v", seen, err)
	}
	if err := vault.RemoveWatchHeartbeat(root, 2); err != nil {
		t.Fatal(err)
	}
	if _, seen, err := vault.ReadWatchHeartbeat(root); err != nil || seen {
		t.Fatalf("owner removal left the heartbeat: seen=%v err=%v", seen, err)
	}
}
func TestWatchPublishesAndRemovesHeartbeat(t *testing.T) {
	ex, _, _, _, root := newVault(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ex.Watch(ctx, 10*time.Millisecond) }()

	heartbeatPath := filepath.Join(root, vault.WatchHeartbeatFile)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(heartbeatPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("watch did not publish a heartbeat")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watch returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not stop after cancel")
	}
	if _, err := os.Stat(heartbeatPath); !os.IsNotExist(err) {
		t.Fatalf("clean shutdown left the heartbeat behind: %v", err)
	}
}

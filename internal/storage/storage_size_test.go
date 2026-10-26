package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// TestStats_StorageBytes covers the storage-runtime requirement that statistics
// report on-disk size: a fresh database is non-zero, the number never shrinks
// after a write, the write-ahead log is included (the shared-memory file is
// not), and a database with no file reports zero instead of failing the call.
func TestStats_StorageBytes(t *testing.T) {
	t.Run("fresh database reports a non-zero size", func(t *testing.T) {
		db, path := openSizedDB(t)
		st := mustStats(t, db)
		if st.StorageBytes <= 0 {
			t.Fatalf("fresh database reported %d bytes, want > 0", st.StorageBytes)
		}
		if main := mustRegularFileSize(t, path); st.StorageBytes < main {
			t.Fatalf("reported %d bytes, below the %d-byte main file", st.StorageBytes, main)
		}
	})

	t.Run("size never shrinks after a write", func(t *testing.T) {
		db, _ := openSizedDB(t)
		before := mustStats(t, db).StorageBytes
		insertObservation(t, db, "monotonic-1")
		after := mustStats(t, db).StorageBytes
		if after < before {
			t.Fatalf("storage size shrank after a write: %d -> %d", before, after)
		}
		if after <= 0 {
			t.Fatalf("storage size after a write is %d, want > 0", after)
		}
	})

	t.Run("write-ahead log is counted and shared memory is not", func(t *testing.T) {
		db, path := openSizedDB(t)
		insertObservation(t, db, "wal-1")

		st := mustStats(t, db)
		mainSize := mustRegularFileSize(t, path)
		walSize := mustRegularFileSize(t, path+"-wal")
		if walSize <= 0 {
			t.Fatal("-wal sidecar is missing or empty, so the test cannot prove it is counted")
		}
		if want := mainSize + walSize; st.StorageBytes != want {
			t.Fatalf("reported %d bytes, want main %d + wal %d = %d", st.StorageBytes, mainSize, walSize, want)
		}
		if shmSize := regularFileSize(path + "-shm"); shmSize == 0 {
			t.Log("shared-memory file absent; its exclusion is not exercised by this run")
		}
	})

	t.Run("missing files report zero without failing stats", func(t *testing.T) {
		cases := []struct {
			name string
			path string
		}{
			{name: "absent database", path: filepath.Join(t.TempDir(), "absent.db")},
			{name: "empty path", path: ""},
			{name: "directory", path: t.TempDir()},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := databaseStorageBytes(tc.path); got != 0 {
					t.Fatalf("databaseStorageBytes(%q) = %d, want 0", tc.path, got)
				}
			})
		}

		// An in-memory database has no file of its own; Stats must still answer.
		db, err := Open(context.Background(), ":memory:")
		if err != nil {
			t.Fatalf("open in-memory database: %v", err)
		}
		defer db.Close()
		st := mustStats(t, db)
		if st.StorageBytes != 0 {
			t.Fatalf("in-memory database reported %d bytes, want 0", st.StorageBytes)
		}
		if st.Observations != 0 || st.LiveObservations != 0 {
			t.Fatalf("counts were lost with the size: %+v", st)
		}
	})
}

// openSizedDB opens a database backed by a real file and returns it with that
// path.
func openSizedDB(t *testing.T) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mnemos.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, path
}

// mustStats reads the statistics value, failing the test on error.
func mustStats(t *testing.T, db *DB) memory.Stats {
	t.Helper()
	st, err := db.Observations().Stats(context.Background())
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	return st
}

// mustRegularFileSize fails the test unless path is an existing regular file.
func mustRegularFileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file", path)
	}
	return info.Size()
}

// insertObservation writes one minimal live observation whose id doubles as its
// title and content.
func insertObservation(t *testing.T, db *DB, id string) {
	t.Helper()
	o := observationFixture(id)
	if err := db.Observations().Insert(context.Background(), o); err != nil {
		t.Fatalf("insert %s: %v", id, err)
	}
}

// observationFixture builds one minimal observation with the given id.
func observationFixture(id string) *memory.Observation {
	now := time.Now().UTC()
	return &memory.Observation{
		ID:         id,
		AgentID:    "default",
		Project:    "mnemos",
		Title:      id,
		Content:    id,
		Type:       memory.TypeSemantic,
		Importance: 5,
		CreatedAt:  now,
		ValidFrom:  now,
	}
}

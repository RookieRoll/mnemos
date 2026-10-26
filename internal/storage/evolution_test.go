package storage_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

func TestEvolutionMigrationFromVersionSix(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)"); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("migrations/000[1-6]_*.sql")
	if err != nil || len(files) != 6 {
		t.Fatalf("old migration fixtures=%v %v", files, err)
	}
	for i, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := old.Exec(string(data)); err != nil {
			t.Fatalf("old migration %s: %v", file, err)
		}
		if _, err := old.Exec("INSERT INTO schema_migrations(version) VALUES(?)", i+1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := old.Exec("INSERT INTO observations(id,agent_id,title,content,obs_type) VALUES('legacy','default','legacy','content','pattern')"); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Observations().Export(ctx)
	if err != nil || len(rows) != 1 || rows[0].TopicKey != "" || len(rows[0].Evidence) != 0 {
		t.Fatalf("old row changed during additive migration: %+v %v", rows, err)
	}
	var version int
	if err := db.SQL().QueryRow("SELECT max(version) FROM schema_migrations").Scan(&version); err != nil || version != 12 {
		t.Fatalf("migration version=%d err=%v", version, err)
	}
	for _, name := range []string{"topics", "observation_versions", "evidence_checks"} {
		var n int
		if err := db.SQL().QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&n); err != nil || n != 0 {
			t.Fatalf("unplanned durable entity %s", name)
		}
	}
}

func TestCorruptEvidenceFailsReadRatherThanDisappearing(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	svc := memory.NewService(memory.Config{Store: db.Observations()})
	saved, err := svc.Save(ctx, memory.SaveInput{Title: "owner", Content: "text", Type: memory.TypePattern, Evidence: []memory.EvidenceReference{{Locator: "repo/source.go", Relation: "supports"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec("UPDATE observation_evidence SET checked_at='invalid timestamp' WHERE observation_id=?", saved.Observation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, saved.Observation.ID); err == nil || !strings.Contains(err.Error(), "evidence") {
		t.Fatalf("corrupt metadata silently ignored: %v", err)
	}
	if _, err := db.Observations().Export(ctx); err == nil {
		t.Fatal("corrupt metadata silently lost in export")
	}
}

func TestEvolutionEvidenceDatabaseBounds(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := db.Observations().Insert(ctx, &memory.Observation{ID: "owner", AgentID: "default", Title: "owner", Content: "text", Type: memory.TypePattern, Importance: 5, CreatedAt: now, ValidFrom: now}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 16; i++ {
		key := fmt.Sprintf("%064x", i)
		if _, err := db.SQL().Exec("INSERT INTO observation_evidence(observation_id,evidence_key,locator,relation) VALUES('owner',?,?,'supports')", key, fmt.Sprint("repo/", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.SQL().Exec("INSERT INTO observation_evidence(observation_id,evidence_key,locator,relation) VALUES('owner',?,?,'supports')", strings.Repeat("f", 64), "repo/extra"); err == nil {
		t.Fatal("database accepted unbounded reference count")
	}
	if err := db.Observations().Delete(ctx, "owner"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.SQL().QueryRow("SELECT count(*) FROM observation_evidence").Scan(&count); err != nil || count != 0 {
		t.Fatal("evidence did not cascade")
	}
}

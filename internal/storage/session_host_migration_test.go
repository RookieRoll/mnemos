package storage_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/session"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// Both deployed version-six lineages must converge without reusing ledger IDs.
func TestSessionHostMigrationBothLineagesAndFresh(t *testing.T) {
	for _, lineage := range []string{"fresh", "local-six", "local-seven", "upstream-six"} {
		t.Run(lineage, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "memory.db")
			if lineage != "fresh" {
				seedSessionHostMigrationLineage(t, path, lineage)
			}
			for startup := 0; startup < 2; startup++ {
				db, err := storage.Open(ctx, path)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer db.Close()
					var count, latest int
					if err := db.SQL().QueryRow("SELECT count(*),max(version) FROM schema_migrations").Scan(&count, &latest); err != nil || count != 12 || latest != 12 {
						t.Fatalf("migration count=%d latest=%d err=%v", count, latest, err)
					}
					if lineage != "fresh" {
						old, err := db.Sessions().Get(ctx, "legacy-session")
						wantHost := ""
						if lineage == "upstream-six" {
							wantHost = "preserved-host"
						}
						if err != nil || old.HostSessionID != wantHost || old.Goal != "retain goal" {
							t.Fatalf("legacy session changed: %+v %v", old, err)
						}
						owners, err := db.Observations().Export(ctx)
						if err != nil || len(owners) != 1 || owners[0].Content != "retained content" {
							t.Fatalf("legacy observation changed: %+v %v", owners, err)
						}
						if owners[0].Pinned != (lineage != "upstream-six") {
							t.Fatal("existing pin lost or legacy row unexpectedly pinned")
						}
						if lineage == "local-seven" && (owners[0].TopicKey != "retained/topic" || len(owners[0].Evidence) != 1 || owners[0].Evidence[0].Status != "changed") {
							t.Fatalf("evolution metadata changed: %+v", owners)
						}
					}
					if startup == 0 {
						sess := &session.Session{ID: "mapped", AgentID: "default", Project: "project", HostSessionID: "host-conversation", StartedAt: time.Now().UTC()}
						if err := db.Sessions().Insert(ctx, sess); err != nil {
							t.Fatal(err)
						}
					}
					matches, err := db.Sessions().ListOpenByHost(ctx, "host-conversation")
					if err != nil || len(matches) != 1 || matches[0].ID != "mapped" {
						t.Fatalf("host mapping unavailable: %+v %v", matches, err)
					}
					var indexes int
					if err := db.SQL().QueryRow("SELECT count(*) FROM sqlite_master WHERE type='index' AND name IN ('idx_obs_pinned','idx_sessions_host')").Scan(&indexes); err != nil || indexes != 2 {
						t.Fatalf("compatibility indexes=%d err=%v", indexes, err)
					}
				}()
			}
		})
	}
}

func seedSessionHostMigrationLineage(t *testing.T, path, lineage string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)"); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("migrations/000[1-7]_*.sql")
	if err != nil || len(files) != 7 {
		t.Fatalf("historical migration fixtures=%v err=%v", files, err)
	}
	for i, file := range files {
		version := i + 1
		if version > 6 && lineage != "local-seven" {
			break
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if version == 6 && lineage == "upstream-six" {
			// The actual upstream version-six migration had no pinned column.
			data = []byte("ALTER TABLE sessions ADD COLUMN host_session_id TEXT; CREATE INDEX idx_sessions_host ON sessions(host_session_id) WHERE host_session_id IS NOT NULL;")
		}
		if _, err := db.Exec(string(data)); err != nil {
			t.Fatalf("apply %s: %v", file, err)
		}
		if _, err := db.Exec("INSERT INTO schema_migrations(version) VALUES(?)", version); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		"INSERT INTO sessions(id,agent_id,project,goal) VALUES('legacy-session','default','project','retain goal')",
		"INSERT INTO observations(id,session_id,agent_id,project,title,content,obs_type) VALUES('legacy','legacy-session','default','project','legacy','retained content','pattern')",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if lineage == "upstream-six" {
		if _, err := db.Exec("UPDATE sessions SET host_session_id='preserved-host' WHERE id='legacy-session'"); err != nil {
			t.Fatal(err)
		}
	} else if _, err := db.Exec("UPDATE observations SET pinned=1 WHERE id='legacy'"); err != nil {
		t.Fatal(err)
	}
	if lineage == "local-seven" {
		if _, err := db.Exec("UPDATE observations SET topic_key='retained/topic' WHERE id='legacy'"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO observation_evidence(observation_id,evidence_key,locator,relation,status) VALUES('legacy',?,'repo/source.go','supports','changed')", strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
	}
}

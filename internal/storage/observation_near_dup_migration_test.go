package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// TestNearDupMigrationAppliesToPreviousSchema is the additive-migration proof:
// a database built by the schema lineage that shipped before 0012 upgrades in
// place through the same machinery production uses, the new column arrives as
// NULL on existing rows, and those rows are never backfilled — so a formatting
// variant of a legacy body inserts instead of merging.
func TestNearDupMigrationAppliesToPreviousSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "previous-schema.db")
	raw := runtimeSQL(t, path)
	if _, err := raw.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)"); err != nil {
		t.Fatal(err)
	}
	all, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 || all[len(all)-1].version != 12 {
		t.Fatalf("latest embedded migration = %+v, want 0012", all)
	}
	// Apply the historical files exactly as they shipped, through the production
	// path: historical migrations stay immutable, so the upgrade under test is
	// the real one. applyMigration also runs the version-eight fork reconcile
	// that a hand-rolled fixture would have to imitate.
	for _, m := range all {
		if m.version >= 12 {
			continue
		}
		if err := applyMigration(ctx, raw, m); err != nil {
			t.Fatalf("apply pre-0012 migration %04d_%s: %v", m.version, m.name, err)
		}
	}
	const legacyBody = "Enable WAL for concurrent readers"
	if _, err := raw.Exec(
		"INSERT INTO observations(id,agent_id,project,title,content,obs_type,content_hash) VALUES('legacy','default','proj','legacy note',?,'pattern','legacy-exact-hash')",
		legacyBody); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("upgrade previous schema: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var version int
	if err := db.SQL().QueryRow("SELECT max(version) FROM schema_migrations").Scan(&version); err != nil || version != 12 {
		t.Fatalf("upgraded schema version = %d (%v), want 12", version, err)
	}
	var content, contentHash string
	var normalized *string
	if err := db.SQL().QueryRow(
		"SELECT content, COALESCE(content_hash, ''), content_hash_normalized FROM observations WHERE id = 'legacy'").
		Scan(&content, &contentHash, &normalized); err != nil {
		t.Fatalf("read legacy row: %v", err)
	}
	if content != legacyBody || contentHash != "legacy-exact-hash" {
		t.Fatalf("migration rewrote a legacy row: content=%q hash=%q", content, contentHash)
	}
	if normalized != nil {
		t.Fatalf("legacy row was backfilled with a derived key %q", *normalized)
	}

	// The no-backfill consequence: a formatting variant of the legacy body has no
	// near-duplicate key to match, so it inserts rather than folding.
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	svc := memory.NewService(memory.Config{Store: db.Observations(), Clock: func() time.Time { return now }})
	saved, err := svc.Save(ctx, memory.SaveInput{
		Title: "new note", Content: "Enable WAL, for concurrent readers!", Type: memory.TypePattern, Project: "proj",
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.NearDup != nil || saved.Deduped {
		t.Fatalf("a pre-existing row was used as a near-duplicate match: %+v", saved)
	}
	if saved.NearDupHint != "" {
		t.Fatalf("a NULL-keyed legacy row produced a hint %q", saved.NearDupHint)
	}
	var rows int
	if err := db.SQL().QueryRow("SELECT COUNT(*) FROM observations").Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("live rows = %d (%v), want 2", rows, err)
	}
}

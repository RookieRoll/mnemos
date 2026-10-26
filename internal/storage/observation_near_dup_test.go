package storage_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// nearDupService opens a fixture database and a clock-pinned memory service.
func nearDupService(t *testing.T, now time.Time) (*storage.DB, *memory.Service) {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "near-dup.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := memory.NewService(memory.Config{
		Store: db.Observations(),
		Clock: func() time.Time { return now },
	})
	return db, svc
}

// TestNearDupProvenanceRetainsBodyByteForByte covers the reversibility
// contract: the merged-away content is stored verbatim — whitespace, unicode
// punctuation, newlines and all — so an operator can restore it by re-saving
// that text, and the recorded hashes identify exactly which save was folded.
func TestNearDupProvenanceRetainsBodyByteForByte(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	db, svc := nearDupService(t, now)
	ctx := context.Background()
	if _, err := db.SQL().Exec(
		"INSERT INTO sessions(id, agent_id, project, goal) VALUES('sess-verbatim','default','proj','verbatim')"); err != nil {
		t.Fatal(err)
	}
	first, err := svc.Save(ctx, memory.SaveInput{
		SessionID: "sess-verbatim", Title: "release notes", Type: memory.TypeContext,
		Project: "proj", Content: "first pass at the release notes",
	})
	if err != nil {
		t.Fatal(err)
	}
	const incoming = "  第二版：Release\tnotes\n\n— with unicode punctuation…  "
	second, err := svc.Save(ctx, memory.SaveInput{
		SessionID: "sess-verbatim", Title: "release notes", Type: memory.TypeContext,
		Project: "proj", Content: incoming,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.NearDup == nil || second.NearDup.Reason != memory.NearDupReasonSessionTitle {
		t.Fatalf("expected a session/title merge, got %+v", second)
	}

	var stored, storedIncomingHash, storedNormalizedHash string
	if err := db.SQL().QueryRow(
		"SELECT incoming_content, incoming_hash, normalized_hash FROM observation_near_dups WHERE survivor_id = ?",
		first.Observation.ID).Scan(&stored, &storedIncomingHash, &storedNormalizedHash); err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	if stored != incoming {
		t.Fatalf("provenance body = %q, want the verbatim incoming body %q", stored, incoming)
	}
	if storedIncomingHash != second.NearDup.IncomingHash || storedIncomingHash == "" {
		t.Fatalf("provenance incoming hash = %q, want %q", storedIncomingHash, second.NearDup.IncomingHash)
	}
	if storedNormalizedHash != second.NearDup.NormalizedHash || len(storedNormalizedHash) != 64 {
		t.Fatalf("provenance normalized hash = %q, want the derived key %q", storedNormalizedHash, second.NearDup.NormalizedHash)
	}
	// The survivor is untouched: a fold is an access signal plus a record.
	var survivorContent, survivorHash string
	if err := db.SQL().QueryRow(
		"SELECT content, COALESCE(content_hash, '') FROM observations WHERE id = ?", first.Observation.ID).
		Scan(&survivorContent, &survivorHash); err != nil {
		t.Fatalf("read survivor: %v", err)
	}
	if survivorContent != first.Observation.Content || survivorHash != first.Observation.ContentHash {
		t.Fatalf("fold edited the survivor: content=%q hash=%q", survivorContent, survivorHash)
	}
}

// TestNearDupProvenanceCascadesWithSurvivor keeps the provenance table from
// outliving the observation it describes: the foreign key carries ON DELETE
// CASCADE, and this pins it so a later schema edit cannot silently orphan it.
func TestNearDupProvenanceCascadesWithSurvivor(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	db, svc := nearDupService(t, now)
	ctx := context.Background()

	first, err := svc.Save(ctx, memory.SaveInput{
		Title: "deploy note", Content: "Enable WAL for concurrent readers", Type: memory.TypePattern, Project: "proj",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Save(ctx, memory.SaveInput{
		Title: "deploy note", Content: "Enable WAL, for concurrent readers!", Type: memory.TypePattern, Project: "proj",
	})
	if err != nil || second.NearDup == nil {
		t.Fatalf("expected a near-duplicate merge: %+v %v", second, err)
	}
	if err := db.Observations().Delete(ctx, first.Observation.ID); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := db.SQL().QueryRow("SELECT COUNT(*) FROM observation_near_dups").Scan(&rows); err != nil {
		t.Fatalf("count provenance: %v", err)
	}
	if rows != 0 {
		t.Fatalf("deleting the survivor left %d orphaned provenance rows", rows)
	}
}

// TestNearDupSchemaShape pins the additive schema surface: the column, both
// lookup indexes and the provenance table exist with the column set the design
// fixes, so a later migration cannot quietly drop the reversibility fields.
func TestNearDupSchemaShape(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	db, svc := nearDupService(t, now)
	ctx := context.Background()

	var columnName string
	if err := db.SQL().QueryRow(
		"SELECT name FROM pragma_table_info('observations') WHERE name = 'content_hash_normalized'").
		Scan(&columnName); err != nil || columnName != "content_hash_normalized" {
		t.Fatalf("content_hash_normalized column = %q (%v)", columnName, err)
	}
	for _, index := range []string{"idx_obs_agent_project_normalized", "idx_obs_session_title", "idx_near_dups_survivor"} {
		var found int
		if err := db.SQL().QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?", index).Scan(&found); err != nil || found != 1 {
			t.Fatalf("index %s missing (%v)", index, err)
		}
	}
	for _, name := range []string{"survivor_id", "incoming_content", "incoming_hash", "normalized_hash", "reason", "agent_id", "project", "session_id", "title", "created_at"} {
		var found int
		if err := db.SQL().QueryRow(
			"SELECT COUNT(*) FROM pragma_table_info('observation_near_dups') WHERE name = ?", name).Scan(&found); err != nil || found != 1 {
			t.Fatalf("observation_near_dups.%s missing (%v)", name, err)
		}
	}

	// A normal save writes the derived key; the legacy hash keeps its own value.
	saved, err := svc.Save(ctx, memory.SaveInput{
		Title: "keys", Content: "Use WAL, Mode!", Type: memory.TypePattern, Project: "proj",
	})
	if err != nil {
		t.Fatal(err)
	}
	var normalized string
	if err := db.SQL().QueryRow(
		"SELECT COALESCE(content_hash_normalized, '') FROM observations WHERE id = ?", saved.Observation.ID).
		Scan(&normalized); err != nil {
		t.Fatalf("read derived key: %v", err)
	}
	if normalized != saved.Observation.ContentHashNormalized || len(normalized) != 64 {
		t.Fatalf("stored derived key = %q, want %q", normalized, saved.Observation.ContentHashNormalized)
	}
	if saved.Observation.ContentHash == normalized {
		t.Fatal("the derived key must not equal the legacy content hash")
	}
}

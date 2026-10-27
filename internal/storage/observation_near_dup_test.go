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
// punctuation, newlines and all — so an operator can inspect it for recovery
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

// TestNearDupSessionTitleSkipsLegacyRows pins the no-backfill guarantee on the
// weaker rule. The derived key arrives without a backfill, so a row written
// before this behaviour existed carries NULL, and no near-duplicate rule may use
// it as a survivor — including session/title, which never reads the key. A row
// saved by this build still folds normally.
func TestNearDupSessionTitleSkipsLegacyRows(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	db, svc := nearDupService(t, now)
	ctx := context.Background()
	if _, err := db.SQL().Exec(
		"INSERT INTO sessions(id, agent_id, project, goal) VALUES('sess-legacy','default','proj','legacy')"); err != nil {
		t.Fatal(err)
	}
	// Pre-0012 row: same session, title, project and trust tier inside the
	// window, but content_hash_normalized is NULL because 0012 never backfills.
	if _, err := db.SQL().Exec(`
		INSERT INTO observations
		       (id, session_id, agent_id, project, title, content, obs_type, importance,
		        created_at, valid_from, content_hash, trust_tier, source_kind)
		VALUES ('legacy-row', 'sess-legacy', 'default', 'proj', 'rollout plan',
		        'phase zero body', 'context', 5, ?, ?, 'legacy-hash', 'curated', 'user')`,
		now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	fresh, err := svc.Save(ctx, memory.SaveInput{
		SessionID: "sess-legacy", Title: "rollout plan", Type: memory.TypeContext,
		Project: "proj", Content: "phase one covers the staging cluster",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.NearDup != nil || fresh.Deduped {
		t.Fatalf("a pre-0012 row must never be a survivor: %+v", fresh)
	}
	if fresh.Observation.ID == "legacy-row" {
		t.Fatal("the save folded into a row written before the rule existed")
	}

	merged, err := svc.Save(ctx, memory.SaveInput{
		SessionID: "sess-legacy", Title: "rollout plan", Type: memory.TypeContext,
		Project: "proj", Content: "phase one shipped to staging on friday",
	})
	if err != nil {
		t.Fatal(err)
	}
	if merged.NearDup == nil || merged.NearDup.Reason != memory.NearDupReasonSessionTitle {
		t.Fatalf("a row carrying the derived key must still fold: %+v", merged)
	}
	if merged.Observation.ID != fresh.Observation.ID {
		t.Fatalf("merge returned %s, want %s", merged.Observation.ID, fresh.Observation.ID)
	}
	var rows int
	if err := db.SQL().QueryRow("SELECT COUNT(*) FROM observations").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("rows = %d, want the legacy row plus one live survivor", rows)
	}
}

// TestNearDupLivePredicateRequiresEffectiveSurvivor pins valid_from in the
// shared live predicate: a row whose fact time has not started is not a survivor
// for either rule, while the effective instant itself is inclusive. Folding into
// a future-effective row would bury the incoming save behind an observation
// retrieval cannot see yet.
func TestNearDupLivePredicateRequiresEffectiveSurvivor(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		offset    time.Duration
		wantMerge bool
	}{
		{name: "past-effective survivor folds", offset: -time.Hour, wantMerge: true},
		{name: "survivor effective exactly now folds", offset: 0, wantMerge: true},
		{name: "future-effective survivor never folds", offset: time.Hour, wantMerge: false},
	}
	for _, tc := range cases {
		t.Run("content hash: "+tc.name, func(t *testing.T) {
			db, svc := nearDupService(t, now)
			ctx := context.Background()
			validFrom := now.Add(tc.offset)
			first, err := svc.Save(ctx, memory.SaveInput{
				Title: "deploy note", Type: memory.TypePattern, Project: "proj",
				Content: "Enable WAL for concurrent readers", ValidFrom: &validFrom,
			})
			if err != nil {
				t.Fatal(err)
			}
			second, err := svc.Save(ctx, memory.SaveInput{
				Title: "deploy note", Type: memory.TypePattern, Project: "proj",
				Content: "Enable WAL, for concurrent readers!",
			})
			if err != nil {
				t.Fatal(err)
			}
			assertNearDupSurvivorDecision(t, db, first, second, tc.wantMerge)
		})
		t.Run("session title: "+tc.name, func(t *testing.T) {
			db, svc := nearDupService(t, now)
			ctx := context.Background()
			if _, err := db.SQL().Exec(
				"INSERT INTO sessions(id, agent_id, project, goal) VALUES('sess-effective','default','proj','effective')"); err != nil {
				t.Fatal(err)
			}
			validFrom := now.Add(tc.offset)
			first, err := svc.Save(ctx, memory.SaveInput{
				SessionID: "sess-effective", Title: "rollout plan", Type: memory.TypeContext,
				Project: "proj", Content: "phase one covers the staging cluster", ValidFrom: &validFrom,
			})
			if err != nil {
				t.Fatal(err)
			}
			second, err := svc.Save(ctx, memory.SaveInput{
				SessionID: "sess-effective", Title: "rollout plan", Type: memory.TypeContext,
				Project: "proj", Content: "phase one shipped to staging on friday",
			})
			if err != nil {
				t.Fatal(err)
			}
			assertNearDupSurvivorDecision(t, db, first, second, tc.wantMerge)
		})
	}
}

// assertNearDupSurvivorDecision checks one fold decision and, on the miss path,
// that a not-yet-effective row is not even reported as an out-of-window hint.
func assertNearDupSurvivorDecision(t *testing.T, db *storage.DB, first, second *memory.SaveResult, wantMerge bool) {
	t.Helper()
	var rows int
	if err := db.SQL().QueryRow("SELECT COUNT(*) FROM observations").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if wantMerge {
		if second.NearDup == nil || second.Observation.ID != first.Observation.ID {
			t.Fatalf("expected a fold into %s, got %+v", first.Observation.ID, second)
		}
		if rows != 1 {
			t.Fatalf("merged save left %d rows, want 1", rows)
		}
		return
	}
	if second.NearDup != nil || second.Observation.ID == first.Observation.ID {
		t.Fatalf("a not-yet-effective survivor must not be folded into: %+v", second)
	}
	if second.NearDupHint != "" {
		t.Fatalf("a not-yet-effective row must not be reported as a hint either: %q", second.NearDupHint)
	}
	if rows != 2 {
		t.Fatalf("rows = %d, want 2", rows)
	}
}

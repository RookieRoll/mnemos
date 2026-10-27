package memory_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
	"github.com/polyxmedia/mnemos/internal/storage"
)

// nearDupFixture pins the clock and the window so every window assertion is
// deterministic: the service's clock drives created_at and the near-duplicate
// cutoff, so advancing it is exactly "time passed".
type nearDupFixture struct {
	svc  *memory.Service
	db   *storage.DB
	now  time.Time
	zero time.Duration
}

func newNearDupFixture(t *testing.T, window *time.Duration) *nearDupFixture {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "near-dup.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := &nearDupFixture{db: db, now: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)}
	f.svc = memory.NewService(memory.Config{
		Store:         db.Observations(),
		Clock:         func() time.Time { return f.now },
		NearDupWindow: window,
	})
	return f
}

func (f *nearDupFixture) advance(d time.Duration) { f.now = f.now.Add(d) }

func (f *nearDupFixture) save(t *testing.T, in memory.SaveInput) *memory.SaveResult {
	t.Helper()
	res, err := f.svc.Save(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (f *nearDupFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.SQL().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// createSession materialises a session row: observations carry a foreign key to
// sessions, so a save that names a session id needs the session to exist.
func (f *nearDupFixture) createSession(t *testing.T, id string) {
	t.Helper()
	if _, err := f.db.SQL().Exec(
		`INSERT INTO sessions(id, agent_id, project, goal) VALUES(?, 'default', 'proj', 'near-dup fixture')`, id); err != nil {
		t.Fatalf("create session %q: %v", id, err)
	}
}

// nearDupNote is the default shape under test: one project-scoped agent note.
func nearDupNote(content string) memory.SaveInput {
	return memory.SaveInput{
		Title:   "deploy note",
		Content: content,
		Type:    memory.TypePattern,
		Project: "proj",
	}
}

func TestNearDuplicatePreservesTypeAndPin(t *testing.T) {
	for _, sameSession := range []bool{false, true} {
		for _, intent := range []string{"convention", "pin"} {
			t.Run(fmt.Sprintf("%s/session=%v", intent, sameSession), func(t *testing.T) {
				f := newNearDupFixture(t, nil)
				in := nearDupNote("Use WAL.")
				if sameSession {
					f.createSession(t, "same-session")
					in.SessionID = "same-session"
				}
				first := f.save(t, in)
				in.Content = "Use WAL!"
				if intent == "convention" {
					in.Type = memory.TypeConvention
				} else {
					in.Pinned = true
				}
				second := f.save(t, in)
				if second.Observation.ID == first.Observation.ID || second.Observation.Type != in.Type || second.Observation.Pinned != in.Pinned {
					t.Fatal("near-duplicate fold discarded the new write intent")
				}
			})
		}
	}
}

func TestNearDuplicatePreservesScopeAndTime(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*nearDupFixture, *memory.SaveInput)
	}{
		{"agent", func(_ *nearDupFixture, in *memory.SaveInput) { in.AgentID = "other" }},
		{"project", func(_ *nearDupFixture, in *memory.SaveInput) { in.Project = "other" }},
		{"topic", func(_ *nearDupFixture, in *memory.SaveInput) { in.TopicKey = "other" }},
		{"future", func(f *nearDupFixture, in *memory.SaveInput) { future := f.now.Add(time.Hour); in.ValidFrom = &future }},
		{"past", func(f *nearDupFixture, in *memory.SaveInput) { past := f.now.Add(-time.Hour); in.ValidFrom = &past }},
		{"valid until", func(f *nearDupFixture, in *memory.SaveInput) { until := f.now.Add(time.Hour); in.ValidUntil = &until }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newNearDupFixture(t, nil)
			f.createSession(t, "same-session")
			in := memory.SaveInput{SessionID: "same-session", Title: "same title", Content: "first body", Type: memory.TypeContext, Project: "proj"}
			first := f.save(t, in)
			in.Content = "different body"
			tc.change(f, &in)
			second := f.save(t, in)
			if first.Observation.ID == second.Observation.ID {
				t.Fatal("distinct scope or fact time was merged")
			}
		})
	}
}

func TestNearDuplicateNumericAndExistingKey(t *testing.T) {
	f := newNearDupFixture(t, nil)
	first := f.save(t, nearDupNote("limit 1.5 seconds"))
	// Simulate a derived key written by the old rule that removed decimal points.
	secondInput := nearDupNote("limit 15 seconds")
	other := f.save(t, secondInput)
	if first.Observation.ID == other.Observation.ID {
		t.Fatal("decimal was folded")
	}
	if _, err := f.db.SQL().Exec(`UPDATE observations SET content_hash_normalized = (SELECT content_hash_normalized FROM observations WHERE id = ?) WHERE id = ?`, other.Observation.ID, first.Observation.ID); err != nil {
		t.Fatal(err)
	}
	// Retire the actual matching row, leaving only the stale-key candidate.
	if _, err := f.db.SQL().Exec(`UPDATE observations SET invalidated_at = ? WHERE id = ?`, f.now, other.Observation.ID); err != nil {
		t.Fatal(err)
	}
	third := f.save(t, secondInput)
	if third.Observation.ID == first.Observation.ID {
		t.Fatal("stale normalized key caused incorrect fold")
	}
}

func TestNearDuplicateSessionTitleKeepsNumericChanges(t *testing.T) {
	f := newNearDupFixture(t, nil)
	f.createSession(t, "numeric-session")
	in := memory.SaveInput{SessionID: "numeric-session", Project: "proj", Title: "limit", Content: "limit 1.5 seconds", Type: memory.TypeContext}
	first := f.save(t, in)
	in.Content = "limit 15 seconds"
	second := f.save(t, in)
	if first.Observation.ID == second.Observation.ID {
		t.Fatal("numeric change folded through session/title")
	}
}

func TestExactDuplicateFutureStart(t *testing.T) {
	f := newNearDupFixture(t, nil)
	in := nearDupNote("same body")
	first := f.save(t, in)
	future := f.now.Add(time.Hour)
	in.ValidFrom = &future
	second := f.save(t, in)
	if first.Observation.ID == second.Observation.ID || !second.Observation.ValidFrom.Equal(future) {
		t.Fatal("future validity lost to exact dedup")
	}
}

func TestRepeatedExplicitFactTimeDeduplicates(t *testing.T) {
	for _, kind := range []string{"past", "future", "until", "interval"} {
		t.Run(kind, func(t *testing.T) {
			f := newNearDupFixture(t, nil)
			in := nearDupNote("same temporal fact")
			past, future, until := f.now.Add(-time.Hour), f.now.Add(time.Hour), f.now.Add(2*time.Hour)
			switch kind {
			case "past":
				in.ValidFrom = &past
			case "future":
				in.ValidFrom = &future
			case "until":
				in.ValidUntil = &until
			case "interval":
				in.ValidFrom, in.ValidUntil = &future, &until
			}
			first := f.save(t, in)
			f.advance(time.Minute)
			second := f.save(t, in)
			if first.Observation.ID != second.Observation.ID || !second.Deduped {
				t.Fatal("identical fact interval inserted twice")
			}
			changed := until.Add(time.Hour)
			in.ValidUntil = &changed
			if third := f.save(t, in); third.Observation.ID == first.Observation.ID {
				t.Fatal("different fact interval was discarded")
			}
		})
	}
}

func TestLeadingDecimalsDoNotMerge(t *testing.T) {
	for _, pair := range [][2]string{{"limit .5 seconds", "limit 5 seconds"}, {"limit -.5 seconds", "limit -5 seconds"}} {
		for _, sameSession := range []bool{false, true} {
			name := pair[0]
			if sameSession {
				name += "/same session"
			}
			t.Run(name, func(t *testing.T) {
				f := newNearDupFixture(t, nil)
				in := nearDupNote(pair[0])
				if sameSession {
					f.createSession(t, "decimal")
					in.SessionID = "decimal"
				}
				first := f.save(t, in)
				in.Content = pair[1]
				if second := f.save(t, in); second.Observation.ID == first.Observation.ID {
					t.Fatal("different decimal values merged")
				}
			})
		}
	}
}

func TestNearDuplicateDoesNotLoseSurvivorEnd(t *testing.T) {
	f := newNearDupFixture(t, nil)
	until := f.now.Add(time.Hour)
	in := nearDupNote("same body")
	in.ValidUntil = &until
	first := f.save(t, in)
	in.ValidUntil = nil
	second := f.save(t, in)
	if first.Observation.ID == second.Observation.ID {
		t.Fatal("unbounded input was folded into a finite survivor")
	}
}

// TestSaveNearDuplicateDecision is the table for the whole save-path order:
// exact hash, normalized hash, session and title, and the out-of-window hint.
// Every case runs against the real SQLite store, so the transaction that
// resolves the layers is the one production uses.
func TestSaveNearDuplicateDecision(t *testing.T) {
	window := memory.DefaultNearDupWindow
	disabled := time.Duration(0)
	sessionNote := func(session, title, content string) memory.SaveInput {
		return memory.SaveInput{
			SessionID: session,
			Title:     title,
			Content:   content,
			Type:      memory.TypeContext,
			Project:   "proj",
		}
	}
	rawNote := func(content string) memory.SaveInput {
		return memory.SaveInput{
			Title:   "deploy note",
			Content: content,
			Type:    memory.TypePattern,
			// Quarantine clamp puts this row in the raw tier, so a curated save
			// must not fold into it.
			SourceKind: memory.SourceTool,
			Project:    "proj",
		}
	}

	cases := []struct {
		name       string
		window     *time.Duration
		gap        time.Duration
		sessions   []string
		first      memory.SaveInput
		second     memory.SaveInput
		wantMerged bool
		wantReason memory.NearDupReason
		// wantExact marks the unchanged layer: identical content hashes dedup
		// without any near-duplicate record.
		wantExact bool
		wantHint  bool
		wantRows  int
	}{
		{
			name: "trailing prose punctuation merges", window: &window, gap: time.Hour,
			first:      nearDupNote("Enable WAL for concurrent readers"),
			second:     nearDupNote("Enable WAL for concurrent readers!"),
			wantMerged: true, wantReason: memory.NearDupReasonContentHash, wantRows: 1,
		},
		{
			name: "case only merges", window: &window, gap: time.Hour,
			first:      nearDupNote("Enable WAL for concurrent readers"),
			second:     nearDupNote("enable wal FOR concurrent READERS"),
			wantMerged: true, wantReason: memory.NearDupReasonContentHash, wantRows: 1,
		},
		{
			name: "whitespace only merges", window: &window, gap: time.Hour,
			first:      nearDupNote("Enable WAL for concurrent readers"),
			second:     nearDupNote("Enable   WAL\nfor\tconcurrent   readers "),
			wantMerged: true, wantReason: memory.NearDupReasonContentHash, wantRows: 1,
		},
		{
			name: "one changed letter does not merge", window: &window, gap: time.Hour,
			first:    nearDupNote("Enable WAL for concurrent readers"),
			second:   nearDupNote("Enable WAL for concurrent reader"),
			wantRows: 2,
		},
		{
			name: "lost word boundary does not merge", window: &window, gap: time.Hour,
			first:    nearDupNote("ab c"),
			second:   nearDupNote("a bc"),
			wantRows: 2,
		},
		{
			name: "stripped minus sign does not merge", window: &window, gap: time.Hour,
			first:    nearDupNote("x=-1"),
			second:   nearDupNote("x=1"),
			wantRows: 2,
		},
		{
			name: "stripped combining mark does not merge", window: &window, gap: time.Hour,
			first:    nearDupNote("cafe\u0301"),
			second:   nearDupNote("cafe"),
			wantRows: 2,
		},
		{
			name: "cjk punctuation variant still merges", window: &window, gap: time.Hour,
			first:      nearDupNote("过期，"),
			second:     nearDupNote("过期："),
			wantMerged: true, wantReason: memory.NearDupReasonContentHash, wantRows: 1,
		},
		{
			name: "different project does not merge", window: &window, gap: time.Hour,
			first:    nearDupNote("Enable WAL for concurrent readers"),
			second:   memory.SaveInput{Title: "deploy note", Content: "Enable WAL for concurrent readers!", Type: memory.TypePattern, Project: "other"},
			wantRows: 2,
		},
		{
			name: "exactly at the window boundary still merges", window: &window, gap: memory.DefaultNearDupWindow,
			first:      nearDupNote("Enable WAL for concurrent readers"),
			second:     nearDupNote("Enable WAL for concurrent readers!"),
			wantMerged: true, wantReason: memory.NearDupReasonContentHash, wantRows: 1,
		},
		{
			name: "outside the window inserts and hints", window: &window, gap: memory.DefaultNearDupWindow + time.Second,
			first:    nearDupNote("Enable WAL for concurrent readers"),
			second:   nearDupNote("Enable WAL for concurrent readers!"),
			wantHint: true,
			wantRows: 2,
		},
		{
			name: "far outside the window still hints only", window: &window, gap: 30 * 24 * time.Hour,
			first:    nearDupNote("Enable WAL for concurrent readers"),
			second:   nearDupNote("Enable WAL for concurrent readers!"),
			wantHint: true,
			wantRows: 2,
		},
		{
			name: "disabled window merges nothing", window: &disabled, gap: time.Hour,
			first:    nearDupNote("Enable WAL for concurrent readers"),
			second:   nearDupNote("Enable WAL for concurrent readers!"),
			wantRows: 2,
		},
		{
			name: "exact duplicate still dedups with the window disabled", window: &disabled, gap: time.Hour,
			first:     nearDupNote("Enable WAL for concurrent readers"),
			second:    nearDupNote("Enable WAL for concurrent readers"),
			wantExact: true,
			wantRows:  1,
		},
		{
			name: "exact duplicate still dedups by default", window: &window, gap: time.Hour,
			first:     nearDupNote("Enable WAL for concurrent readers"),
			second:    nearDupNote("Enable WAL for concurrent readers"),
			wantExact: true,
			wantRows:  1,
		},
		{
			name: "same session and title inside the window merges", window: &window, gap: 2 * time.Hour,
			sessions:   []string{"sess-1"},
			first:      sessionNote("sess-1", "rollout plan", "phase one covers the staging cluster"),
			second:     sessionNote("sess-1", "rollout plan", "phase one shipped to staging on friday"),
			wantMerged: true,
			wantReason: memory.NearDupReasonSessionTitle,
			wantRows:   1,
		},
		{
			name: "same session and title outside the window does not merge", window: &window, gap: 48 * time.Hour,
			sessions: []string{"sess-2"},
			first:    sessionNote("sess-2", "rollout plan", "phase one covers the staging cluster"),
			second:   sessionNote("sess-2", "rollout plan", "phase one shipped to staging on friday"),
			wantRows: 2,
		},
		{
			name: "same session different title does not merge", window: &window, gap: time.Hour,
			sessions: []string{"sess-3"},
			first:    sessionNote("sess-3", "rollout plan", "phase one covers the staging cluster"),
			second:   sessionNote("sess-3", "rollback plan", "phase one shipped to staging on friday"),
			wantRows: 2,
		},
		{
			name: "same title without a session does not merge", window: &window, gap: time.Hour,
			first:    sessionNote("", "rollout plan", "phase one covers the staging cluster"),
			second:   sessionNote("", "rollout plan", "phase one shipped to staging on friday"),
			wantRows: 2,
		},
		{
			name: "raw tier is never folded into a curated survivor", window: &window, gap: time.Minute,
			first:    nearDupNote("Enable WAL for concurrent readers"),
			second:   rawNote("Enable WAL for concurrent readers!"),
			wantRows: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newNearDupFixture(t, tc.window)
			for _, id := range tc.sessions {
				f.createSession(t, id)
			}
			first := f.save(t, tc.first)
			f.advance(tc.gap)
			second := f.save(t, tc.second)

			if got := f.count(t, `SELECT COUNT(*) FROM observations`); got != tc.wantRows {
				t.Fatalf("live rows = %d, want %d (first=%s second=%s)", got, tc.wantRows, first.Observation.ID, second.Observation.ID)
			}
			if second.NearDup != nil && second.NearDupHint != "" {
				t.Fatalf("a merged save must not also report a hint: %+v hint=%q", second.NearDup, second.NearDupHint)
			}
			switch {
			case tc.wantExact:
				if !second.Deduped || second.NearDup != nil {
					t.Fatalf("exact duplicate must dedup on the legacy hash alone: deduped=%v near=%+v", second.Deduped, second.NearDup)
				}
				if second.Observation.ID != first.Observation.ID {
					t.Fatalf("exact dedup returned %s, want the survivor %s", second.Observation.ID, first.Observation.ID)
				}
				if got := f.count(t, `SELECT COUNT(*) FROM observation_near_dups`); got != 0 {
					t.Fatalf("exact dedup wrote %d provenance rows, want 0", got)
				}
			case tc.wantMerged:
				if second.NearDup == nil {
					t.Fatal("expected a near-duplicate merge, got a fresh insert")
				}
				if second.NearDup.Reason != tc.wantReason {
					t.Fatalf("merge reason = %q, want %q", second.NearDup.Reason, tc.wantReason)
				}
				if !second.Deduped {
					t.Fatal("a merged save must report Deduped")
				}
				if second.Observation.ID != first.Observation.ID {
					t.Fatalf("merge returned %s, want the survivor %s", second.Observation.ID, first.Observation.ID)
				}
				if second.NearDup.IncomingContent != tc.second.Content {
					t.Fatalf("result retained %q, want the verbatim incoming body %q", second.NearDup.IncomingContent, tc.second.Content)
				}
				if second.NearDup.IncomingHash == "" {
					t.Fatal("a merged save must record the folded-away save's content hash")
				}
				if second.NearDup.IncomingHash == first.Observation.ContentHash {
					t.Fatal("incoming hash must be the folded-away save's own hash, not the survivor's")
				}
				var content, incomingHash, normalizedHash, reason string
				if err := f.db.SQL().QueryRow(
					`SELECT incoming_content, incoming_hash, normalized_hash, reason FROM observation_near_dups WHERE survivor_id = ?`,
					first.Observation.ID).Scan(&content, &incomingHash, &normalizedHash, &reason); err != nil {
					t.Fatalf("read provenance: %v", err)
				}
				if content != tc.second.Content {
					t.Fatalf("provenance content = %q, want the verbatim incoming body %q", content, tc.second.Content)
				}
				if incomingHash != second.NearDup.IncomingHash || normalizedHash != second.NearDup.NormalizedHash {
					t.Fatalf("provenance hashes = (%q, %q), want (%q, %q)", incomingHash, normalizedHash, second.NearDup.IncomingHash, second.NearDup.NormalizedHash)
				}
				if reason != string(tc.wantReason) {
					t.Fatalf("provenance reason = %q, want %q", reason, tc.wantReason)
				}
				var access int
				if err := f.db.SQL().QueryRow(`SELECT access_count FROM observations WHERE id = ?`, first.Observation.ID).Scan(&access); err != nil {
					t.Fatalf("read access count: %v", err)
				}
				if access < 1 {
					t.Fatalf("survivor access_count = %d, want the fold to bump it", access)
				}
			default:
				if second.NearDup != nil {
					t.Fatalf("expected no merge, got %+v", second.NearDup)
				}
				if second.Observation.ID == first.Observation.ID {
					t.Fatal("expected a fresh observation, got the existing id")
				}
				if second.Deduped {
					t.Fatal("a fresh insert must not report Deduped")
				}
				if got := f.count(t, `SELECT COUNT(*) FROM observation_near_dups`); got != 0 {
					t.Fatalf("no merge happened but %d provenance rows exist", got)
				}
				if tc.wantHint {
					if second.NearDupHint != first.Observation.ID {
						t.Fatalf("hint = %q, want the out-of-window candidate %s", second.NearDupHint, first.Observation.ID)
					}
				} else if second.NearDupHint != "" {
					t.Fatalf("unexpected hint %q", second.NearDupHint)
				}
			}
			if tc.wantHint {
				// The hint must never be a merge: the survivor is untouched.
				var access int
				if err := f.db.SQL().QueryRow(`SELECT access_count FROM observations WHERE id = ?`, first.Observation.ID).Scan(&access); err != nil {
					t.Fatalf("read access count: %v", err)
				}
				if access != 0 {
					t.Fatalf("hint-only candidate access_count = %d, want 0", access)
				}
			}
		})
	}
}

// TestNearDupMergeKeepsProvenanceOutOfRetrieval is the "does not alter retrieval
// scores" guard: the folded-away body lives only in the provenance table, so a
// token unique to it is unfindable by search and the survivor stays the single
// live row.
func TestNearDupMergeKeepsProvenanceOutOfRetrieval(t *testing.T) {
	ctx := context.Background()
	window := memory.DefaultNearDupWindow
	f := newNearDupFixture(t, &window)
	f.createSession(t, "sess-prov")

	first := f.save(t, memory.SaveInput{
		SessionID: "sess-prov", Title: "rollout plan", Type: memory.TypeContext,
		Project: "proj", Content: "alpha body about sqlite",
	})
	f.advance(time.Hour)
	second := f.save(t, memory.SaveInput{
		SessionID: "sess-prov", Title: "rollout plan", Type: memory.TypeContext,
		Project: "proj", Content: "zanzibar quux wibble",
	})
	if second.NearDup == nil || second.Observation.ID != first.Observation.ID {
		t.Fatalf("expected a session/title merge, got %+v", second)
	}

	results, err := f.svc.Search(ctx, memory.SearchInput{Query: "zanzibar"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("provenance content leaked into search: %+v", results)
	}
	if got := f.count(t, `SELECT COUNT(*) FROM observations`); got != 1 {
		t.Fatalf("merged save left %d live rows, want 1", got)
	}
	// The retained body is available verbatim. This distinct title avoids the
	// session/title rule; a repeated original request could merge again.
	var retained string
	if err := f.db.SQL().QueryRow(`SELECT incoming_content FROM observation_near_dups WHERE survivor_id = ?`, first.Observation.ID).Scan(&retained); err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	if retained != "zanzibar quux wibble" {
		t.Fatalf("retained body = %q, want the verbatim incoming content", retained)
	}
	restored := f.save(t, memory.SaveInput{
		SessionID: "sess-prov", Title: "rollout plan restored", Type: memory.TypeContext,
		Project: "proj", Content: retained,
	})
	if restored.NearDup != nil || restored.Observation.Content != retained {
		t.Fatalf("re-saving the retained body must restore a live row, got %+v", restored)
	}
}

// TestRestoreKeepsContentHashesVerbatim is the regression guard for the
// evolution/Restore invariant: the fidelity path never recomputes the legacy
// content hash, and it carries the derived key through untouched.
func TestRestoreKeepsContentHashesVerbatim(t *testing.T) {
	ctx := context.Background()
	window := memory.DefaultNearDupWindow
	f := newNearDupFixture(t, &window)

	saved := f.save(t, memory.SaveInput{
		Title: "restore me", Content: "Body, with punctuation!", Type: memory.TypePattern, Project: "proj",
	})
	if saved.Observation.ContentHashNormalized == "" {
		t.Fatal("a saved row must carry the derived key")
	}
	restoredRow := *saved.Observation
	restoredRow.ID = "restored-1"
	written, err := f.svc.Restore(ctx, &restoredRow)
	if err != nil {
		t.Fatal(err)
	}
	if !written {
		t.Fatal("restore skipped a new id")
	}

	var contentHash, normalizedHash string
	if err := f.db.SQL().QueryRow(
		`SELECT content_hash, COALESCE(content_hash_normalized, '') FROM observations WHERE id = ?`, "restored-1").
		Scan(&contentHash, &normalizedHash); err != nil {
		t.Fatalf("read restored row: %v", err)
	}
	if contentHash != saved.Observation.ContentHash {
		t.Fatalf("restore rewrote content_hash: got %q want %q", contentHash, saved.Observation.ContentHash)
	}
	if normalizedHash != saved.Observation.ContentHashNormalized {
		t.Fatalf("restore rewrote the derived key: got %q want %q", normalizedHash, saved.Observation.ContentHashNormalized)
	}
}

// TestSaveStoresNormalizedKeyOnlyForReadableContent covers the defensive edge:
// a body with no significant rune at all stores no derived key, so
// punctuation-only bodies can never match each other. A body made only of
// meaning-bearing signs ("---") is deliberately not keyless: the signs survive
// because stripping them would collapse "x=-1" into "x=1".
func TestSaveStoresNormalizedKeyOnlyForReadableContent(t *testing.T) {
	window := memory.DefaultNearDupWindow
	f := newNearDupFixture(t, &window)
	f.save(t, nearDupNote("...!!!"))
	f.advance(time.Minute)
	f.save(t, nearDupNote("???..."))

	if got := f.count(t, `SELECT COUNT(*) FROM observations WHERE content_hash_normalized IS NULL`); got != 2 {
		t.Fatalf("%d punctuation-only rows stored a derived key, want none", 2-got)
	}
	if got := f.count(t, `SELECT COUNT(*) FROM observations`); got != 2 {
		t.Fatalf("punctuation-only bodies merged into %d row(s), want 2", got)
	}
}

// TestCapabilitiesDescribeBothDedupLayers covers design D5: the flag stays true
// and ships with the sentence that bounds it, plus the effective window.
func TestCapabilitiesDescribeBothDedupLayers(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "caps.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	on := memory.NewService(memory.Config{Store: db.Observations()})
	c := on.Capabilities()
	if !c.DeterministicDedup {
		t.Fatal("deterministic_dedup must stay true")
	}
	if c.NearDupWindow != "24h0m0s" {
		t.Fatalf("near_dup_window = %q, want the default 24h", c.NearDupWindow)
	}
	for _, want := range []string{"exact", "unconditional", "near-duplicate", "24h0m0s", "provenance", "probabilistic", "model-derived"} {
		if !strings.Contains(c.DeterministicDedupSemantics, want) {
			t.Errorf("dedup semantics %q must mention %q", c.DeterministicDedupSemantics, want)
		}
	}

	disabled := time.Duration(0)
	off := memory.NewService(memory.Config{Store: db.Observations(), NearDupWindow: &disabled})
	c = off.Capabilities()
	if !c.DeterministicDedup {
		t.Fatal("deterministic_dedup must stay true when near-duplicate merging is off")
	}
	if c.NearDupWindow != "disabled" {
		t.Fatalf("near_dup_window = %q, want disabled", c.NearDupWindow)
	}
	if !strings.Contains(c.DeterministicDedupSemantics, "disabled") || !strings.Contains(c.DeterministicDedupSemantics, "unconditional") {
		t.Fatalf("disabled dedup semantics must name both layers: %q", c.DeterministicDedupSemantics)
	}
}

package memory_test

import (
	"fmt"
	"testing"
	"time"
)

func TestNearDuplicatePreservesCodeMeaning(t *testing.T) {
	for _, tc := range []struct{ name, first, second string }{
		{"comparison", "Retry when status != ready", "Retry when status = ready"},
		{"negation", "Retry when !ready", "Retry when ready"},
		{"double negation", "Retry when !!ready", "Retry when !ready"},
		{"language", "The adapter is written in C#", "The adapter is written in C"},
		{"identifier", "Use the user_id field", "Use the userid field"},
		{"identifier position", "Use the user_id field", "Use the userid_ field"},
		{"conjunction", "Require ready && healthy", "Require ready healthy"},
		{"boolean grouping", "Require a && (b || c)", "Require (a && b) || c"},
		{"field access", "Read obj.field", "Read objfield"},
		{"index", "Read items[0]", "Read items0"},
		{"value type", "Set enabled=\"false\"", "Set enabled=false"},
		{"spaced negation", "Retry when ! ready", "Retry when ready"},
		{"group operand", "Require (a && b && c)", "Require (a && d && c)"},
		{"operator operand", "Require a && b", "Require a && c"},
		{"string spaces", "Set label=\"a b\"", "Set label=\"ab\""},
		{"literal spaces", "Set label=\"a  b\"", "Set label=\"a b\""},
		{"literal case", "Set token=\"ABC\"", "Set token=\"abc\""},
		{"field case", "Read obj.Field", "Read obj.field"},
		{"backtick pattern", "Match `word?`", "Match `word`"},
	} {
		for _, sameSession := range []bool{false, true} {
			for _, reverse := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/session=%v/reverse=%v", tc.name, sameSession, reverse), func(t *testing.T) {
					f := newNearDupFixture(t, nil)
					a, b := nearDupNote(tc.first), nearDupNote(tc.second)
					if reverse {
						a.Content, b.Content = b.Content, a.Content
					}
					if sameSession {
						f.createSession(t, "code-session")
						a.SessionID, b.SessionID = "code-session", "code-session"
					} else {
						b.Title = "distinct assertion"
					}
					first, second := f.save(t, a), f.save(t, b)
					if second.Deduped || first.Observation.ID == second.Observation.ID {
						t.Fatalf("distinct code facts folded: reason=%+v saved=%q submitted=%q", second.NearDup, second.Observation.Content, b.Content)
					}
					if got := f.count(t, "SELECT COUNT(*) FROM observations"); got != 2 {
						t.Fatalf("stored %d observations, want both code facts", got)
					}
				})
			}
		}
	}
}

func TestNearDuplicateCodeLiteralStillMerges(t *testing.T) {
	for _, tc := range []struct{ name, first, second string }{
		{"identifier", "Use the user_id field", "  Use the user_id field  "},
		{"conjunction", "Require ready && healthy", "Require ready && healthy"},
		{"negation", "Retry when !ready", "Retry when !ready"},
		{"group", "Require a && (b || c)", "  Require a && (b || c)  "},
		{"quoted value", "Set enabled=\"false\"", "Set enabled=\"false\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newNearDupFixture(t, nil)
			f.createSession(t, "formatting-session")
			a, b := nearDupNote(tc.first), nearDupNote(tc.second)
			a.SessionID, b.SessionID = "formatting-session", "formatting-session"
			b.Title = "same literal code"
			first, second := f.save(t, a), f.save(t, b)
			if !second.Deduped || first.Observation.ID != second.Observation.ID {
				t.Fatal("unchanged literal code should still permit a near-duplicate fold")
			}
		})
	}
}

func TestNearDuplicateCodeKeepsChangedProse(t *testing.T) {
	f := newNearDupFixture(t, nil)
	f.createSession(t, "literal-session")
	a, b := nearDupNote("Use C# for the adapter"), nearDupNote("Use C# for the gateway")
	a.SessionID, b.SessionID = "literal-session", "literal-session"
	first, second := f.save(t, a), f.save(t, b)
	if second.Deduped || first.Observation.ID == second.Observation.ID {
		t.Fatal("syntax-bearing notes need the whole normalized body to agree")
	}
}

func TestNearDuplicateCodeRejectsLegacyNormalizedKey(t *testing.T) {
	for _, outsideWindow := range []bool{false, true} {
		t.Run(fmt.Sprintf("outside-window=%v", outsideWindow), func(t *testing.T) {
			f := newNearDupFixture(t, nil)
			first := f.save(t, nearDupNote("Use the user_id field"))
			otherInput := nearDupNote("Use the userid field")
			otherInput.Title = "different field"
			other := f.save(t, otherInput)
			// A previous binary removed underscores. Leave only its stale key
			// as a candidate for the spelling without the underscore.
			if _, err := f.db.SQL().Exec(`UPDATE observations SET content_hash_normalized = (SELECT content_hash_normalized FROM observations WHERE id = ?) WHERE id = ?`, other.Observation.ID, first.Observation.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.SQL().Exec(`UPDATE observations SET invalidated_at = ? WHERE id = ?`, f.now, other.Observation.ID); err != nil {
				t.Fatal(err)
			}
			if outsideWindow {
				f.advance(25 * time.Hour)
			}
			got := f.save(t, otherInput)
			if got.Deduped || got.NearDupHint != "" {
				t.Fatalf("stale code key produced a fold or hint: %+v", got)
			}
		})
	}
}

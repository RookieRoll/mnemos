package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

// TestNormalizeContentForHash distinguishes plain-prose equivalence from
// syntax-bearing literal comparison, without parsing a programming language.
func TestNormalizeContentForHash(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "already normalized", content: "use wal mode", want: "use wal mode"},
		{name: "lowercased", content: "Use WAL Mode", want: "use wal mode"},
		{name: "trailing prose punctuation dropped", content: "Use WAL Mode!", want: "use wal mode"},
		{name: "internal punctuation preserved", content: "Use WAL, Mode!", want: "Use WAL, Mode!"},
		{name: "whitespace collapsed to one space", content: "  use\twal\nmode  ", want: "use wal mode"},
		{name: "numeric separators kept", content: "sqlite 3.45.1", want: "sqlite 3.45.1"},
		{name: "cjk interior punctuation kept", content: "使用 WAL 模式，记得", want: "使用 WAL 模式，记得"},
		{name: "symbols kept", content: "done ✅ ok → yes", want: "done ✅ ok → yes"},
		{name: "accents lowercased not stripped", content: "ÜBER Straße", want: "über straße"},
		{name: "word boundary kept", content: "ab c", want: "ab c"},
		{name: "minus and assignment signs kept", content: "x=-1", want: "x=-1"},
		{name: "plus equals percent kept", content: "a+b=c 50%", want: "a+b=c 50%"},
		{name: "code punctuation kept", content: "C# user_id a&&b", want: "C# user_id a&&b"},
		{name: "negation kept", content: "x!=y !ready !!ready !(ready)", want: "x!=y !ready !!ready !(ready)"},
		{name: "literal case and spaces kept", content: "  Set token=\"Ab  c\"  ", want: "Set token=\"Ab  c\""},
		{name: "combining mark kept", content: "e\u0301", want: "e\u0301"},
		{name: "punctuation only", content: "...!!! ???", want: ""},
		{name: "whitespace only", content: "   ", want: ""},
		{name: "empty", content: "", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeContentForHash(tc.content); got != tc.want {
				t.Fatalf("NormalizeContentForHash(%q) = %q, want %q", tc.content, got, tc.want)
			}
		})
	}
}

// TestNormalizeContentForHashKeepsMeaning is the conservative-equivalence guard:
// the three collisions the letters-only rule produced must stay distinct — a lost
// word boundary ("ab c" vs "a bc"), a stripped minus sign ("x=-1" vs "x=1"),
// and a stripped combining mark ("e" + U+0301 vs "e") — while CJK sentence
// punctuation still folds away, because dropping punctuation without introducing
// a boundary is what makes "过期，" and "过期：" the same body.
func TestNormalizeContentForHashKeepsMeaning(t *testing.T) {
	distinct := []struct {
		name string
		a, b string
	}{
		{name: "word boundary", a: "ab c", b: "a bc"},
		{name: "minus sign", a: "x=-1", b: "x=1"},
		{name: "comparison", a: "x<1", b: "x>1"},
		{name: "inequality", a: "x!=y", b: "x=y"},
		{name: "negation", a: "!ready", b: "ready"},
		{name: "double negation", a: "!!ready", b: "!ready"},
		{name: "language", a: "C#", b: "C"},
		{name: "identifier", a: "user_id", b: "userid"},
		{name: "conjunction", a: "a&&b", b: "ab"},
		{name: "division", a: "a/b", b: "ab"},
		{name: "decimal", a: "1.5", b: "15"},
		{name: "version", a: "3.45.1", b: "3451"},
		{name: "address", a: "10.0.0.1", b: "10001"},
		{name: "combining mark", a: "e\u0301", b: "e"},
	}
	for _, tc := range distinct {
		t.Run(tc.name, func(t *testing.T) {
			if NormalizeContentForHash(tc.a) == NormalizeContentForHash(tc.b) {
				t.Fatalf("NormalizeContentForHash collapsed %q and %q into %q", tc.a, tc.b, NormalizeContentForHash(tc.a))
			}
			if hashNormalized(tc.a) == hashNormalized(tc.b) {
				t.Fatalf("hashNormalized collapsed %q and %q", tc.a, tc.b)
			}
		})
	}
	for _, tc := range []struct{ name, a, b string }{
		{name: "cjk comma and colon", a: "过期，", b: "过期："},
		{name: "trailing period and exclamation", a: "shipped.", b: "shipped!"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if NormalizeContentForHash(tc.a) != NormalizeContentForHash(tc.b) {
				t.Fatalf("NormalizeContentForHash(%q) = %q differs from NormalizeContentForHash(%q) = %q", tc.a, NormalizeContentForHash(tc.a), tc.b, NormalizeContentForHash(tc.b))
			}
		})
	}
}

// TestHashNormalizedIgnoresFormattingOnly covers the two ways the normalized
// key must behave: every formatting-only variant produces the same key, and any
// change to a word boundary, a letter, a digit or a meaning-bearing sign
// produces a different one. Punctuation adjacent to a space is formatting;
// gluing two words together with a dropped comma is not, which is why the
// variants below keep their spaces.
func TestHashNormalizedIgnoresFormattingOnly(t *testing.T) {
	base := hashNormalized("Enable WAL for concurrent readers")
	if base == "" {
		t.Fatal("normalized hash of readable content must not be empty")
	}
	for _, variant := range []string{
		"Enable WAL for concurrent readers.",
		"enable wal for concurrent readers",
		"  Enable  WAL\tfor\nconcurrent readers  ",
	} {
		if got := hashNormalized(variant); got != base {
			t.Errorf("hashNormalized(%q) = %q, want the base key %q", variant, got, base)
		}
	}
	for _, variant := range []string{
		"ENABLE WAL, FOR CONCURRENT READERS!",
		"...Enable WAL ...for... concurrent readers...",
		"(Enable WAL) [for] {concurrent} readers",
		"Enable WAL for concurrent reader",
		"Enable WAL for concurrent readers 2",
		"Enable WAL for sequential readers",
		"Enable-WAL for concurrent readers",
		"EnableWALforconcurrentreaders",
	} {
		if got := hashNormalized(variant); got == base {
			t.Errorf("hashNormalized(%q) collided with the base key: a changed boundary, letter or sign must not merge", variant)
		}
	}
	for _, noKey := range []string{"", "...", "!!! ???", "   "} {
		if got := hashNormalized(noKey); got != "" {
			t.Errorf("hashNormalized(%q) = %q, want no key: punctuation-only bodies must not match on nothing", noKey, got)
		}
	}
}

// TestHashContentDefinitionUnchanged locks the legacy content hash to the
// definition it has always had. Near-duplicate merging is a second, derived key:
// if anyone ever re-implements dedup by redefining hashContent, this test and the
// evolution/Restore fixtures fail together.
func TestHashContentDefinitionUnchanged(t *testing.T) {
	h := sha256.New()
	h.Write([]byte(TypePattern))
	h.Write([]byte{0})
	for _, part := range []string{"Title", "  Body  ", "Why"} {
		h.Write([]byte(strings.TrimSpace(part)))
		h.Write([]byte{0})
	}
	want := hex.EncodeToString(h.Sum(nil))
	if got := hashContent(TypePattern, "Title", "  Body  ", "Why"); got != want {
		t.Fatalf("hashContent no longer matches its documented definition: got %q want %q", got, want)
	}
	// The legacy hash stays formatting-sensitive: that is exactly what the
	// normalized key exists to complement rather than replace.
	if hashContent(TypePattern, "Title", "Use WAL") == hashContent(TypePattern, "Title", "use wal") {
		t.Fatal("hashContent must remain case-sensitive; normalization belongs to the derived key")
	}
	if hashContent(TypePattern, "Title", "Use WAL") == hashNormalized("Use WAL") {
		t.Fatal("the derived key must not collapse into the legacy content hash")
	}
}

// TestResolveNearDupWindow covers the configuration contract: unset means the
// 24h default, and an explicit zero — the operator's "0s" — disables merging.
func TestResolveNearDupWindow(t *testing.T) {
	zero := time.Duration(0)
	negative := -time.Minute
	custom := 90 * time.Minute
	tests := []struct {
		name       string
		configured *time.Duration
		want       time.Duration
	}{
		{name: "unset uses default", configured: nil, want: DefaultNearDupWindow},
		{name: "explicit zero disables", configured: &zero, want: 0},
		{name: "negative disables", configured: &negative, want: 0},
		{name: "custom window", configured: &custom, want: custom},
	}
	if DefaultNearDupWindow != 24*time.Hour {
		t.Fatalf("DefaultNearDupWindow = %v, want 24h", DefaultNearDupWindow)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveNearDupWindow(tc.configured); got != tc.want {
				t.Fatalf("resolveNearDupWindow = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNearDupReasonValid(t *testing.T) {
	tests := []struct {
		reason NearDupReason
		want   bool
	}{
		{NearDupReasonContentHash, true},
		{NearDupReasonSessionTitle, true},
		{"", false},
		{"fuzzy", false},
	}
	for _, tc := range tests {
		if got := tc.reason.Valid(); got != tc.want {
			t.Errorf("NearDupReason(%q).Valid() = %v, want %v", tc.reason, got, tc.want)
		}
	}
}

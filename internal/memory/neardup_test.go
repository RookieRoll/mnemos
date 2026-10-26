package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

// TestNormalizeContentForHash pins the normalization contract from design D1:
// letters and digits survive, everything else is dropped, and the surviving
// runes are case-folded. Anything stronger (stemming, synonyms, locale rules)
// would be inference, which the memory layer does not do.
func TestNormalizeContentForHash(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "already normalized", content: "usewalmode", want: "usewalmode"},
		{name: "case folded", content: "Use WAL Mode", want: "usewalmode"},
		{name: "punctuation dropped", content: "Use WAL, Mode!", want: "usewalmode"},
		{name: "whitespace dropped", content: "  use\twal\nmode  ", want: "usewalmode"},
		{name: "digits kept", content: "sqlite 3.45.1", want: "sqlite3451"},
		{name: "cjk letters kept", content: "使用 WAL 模式，记得", want: "使用wal模式记得"},
		{name: "symbols dropped", content: "done ✅ ok → yes", want: "doneokyes"},
		{name: "accents case-folded not stripped", content: "ÜBER Straße", want: "überstraße"},
		{name: "punctuation only", content: "...!!! ---", want: ""},
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

// TestHashNormalizedIgnoresFormattingOnly covers the two ways the normalized
// key must behave: every formatting-only variant produces the same key, and any
// change to a letter or digit produces a different one.
func TestHashNormalizedIgnoresFormattingOnly(t *testing.T) {
	base := hashNormalized("Enable WAL for concurrent readers")
	if base == "" {
		t.Fatal("normalized hash of readable content must not be empty")
	}
	for _, variant := range []string{
		"Enable WAL for concurrent readers.",
		"enable wal for concurrent readers",
		"  Enable  WAL\tfor\nconcurrent readers  ",
		"ENABLE WAL, FOR CONCURRENT READERS!",
		"...Enable-WAL...for...concurrent...readers...",
	} {
		if got := hashNormalized(variant); got != base {
			t.Errorf("hashNormalized(%q) = %q, want the base key %q", variant, got, base)
		}
	}
	for _, variant := range []string{
		"Enable WAL for concurrent reader",
		"Enable WAL for concurrent readers 2",
		"Enable WAL for sequential readers",
	} {
		if got := hashNormalized(variant); got == base {
			t.Errorf("hashNormalized(%q) collided with the base key: a changed letter or digit must not merge", variant)
		}
	}
	for _, noKey := range []string{"", "...", "!!! ---", "   "} {
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

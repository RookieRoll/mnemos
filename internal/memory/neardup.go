package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
	"unicode"
)

// DefaultNearDupWindow is the near-duplicate merge window applied when the
// caller does not configure one. The save path folds a near-duplicate into an
// existing live observation only while that observation was touched inside this
// window; outside it the save inserts and reports the candidate as a hint.
const DefaultNearDupWindow = 24 * time.Hour

// NearDupReason names the save-path rule that folded a save into a survivor.
// It is the provenance record's discriminator, so an audit can tell a
// normalization-level fold from a same-session continuation.
type NearDupReason string

const (
	// NearDupReasonContentHash: the incoming body normalizes identically to a
	// live observation in the same (agent, project) inside the window.
	NearDupReasonContentHash NearDupReason = "content_hash"
	// NearDupReasonSessionTitle: the incoming save carries the same session id
	// and the same non-empty title as a live observation inside the window,
	// with matching numeric tokens. Syntax-bearing content must also match in
	// full; only plain prose may differ beyond normalization. The merged-away
	// body is kept verbatim in the provenance record.
	NearDupReasonSessionTitle NearDupReason = "session_title"
)

// Valid reports whether r is a recognised near-duplicate reason.
func (r NearDupReason) Valid() bool {
	switch r {
	case NearDupReasonContentHash, NearDupReasonSessionTitle:
		return true
	}
	return false
}

// NearDupMerge describes a save that was folded into an existing observation
// instead of inserting a second live row. It mirrors the provenance row that
// the fold wrote: the survivor it landed on, the rule that matched, and the
// incoming body exactly as it arrived, so a caller can report the merge and an
// operator can inspect the original content and restore it manually.
type NearDupMerge struct {
	// SurvivorID is the live observation the save was folded into. The save
	// result carries this observation, never a new row.
	SurvivorID string
	// Reason is the rule that matched.
	Reason NearDupReason
	// IncomingContent is the merged-away body verbatim (never truncated, never
	// re-normalized), as retained in the provenance record.
	IncomingContent string
	// IncomingHash is the incoming save's exact content hash: the legacy
	// content-hash identity, unchanged in meaning by this feature.
	IncomingHash string
	// NormalizedHash is the incoming save's normalized hash, or empty when
	// only removable sentence marks and whitespace remain.
	NormalizedHash string
}

// NormalizeContentForHash returns the form of content used for near-duplicate
// matching. It is deliberately pure and shallow — no stemming, no synonym
// table, no locale rules — because the memory layer makes no inference: the aim
// is "the same sentence typed twice", not semantic matching.
//
// Plain prose permits Unicode simple lowercase, collapsed whitespace and
// removal of a small set of trailing sentence marks. Interior punctuation and
// other symbols are ambiguous without a language parser, so their presence
// selects literal comparison of the whole body, trimming outer whitespace only.
// This preserves grouping, field access, string quoting, case-sensitive names
// and whitespace inside literals, including through the weaker session/title
// rule. Combining marks are never stripped and no NFC pass is applied.
//
// The exact content hash never uses this function; it is a second, derived key.
func NormalizeContentForHash(content string) string {
	literal := strings.TrimSpace(content)
	prose := strings.TrimRightFunc(literal, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(".,!?，。！？、；：", r)
	})
	if hasNearDupSyntax(prose) {
		return literal
	}
	var b strings.Builder
	b.Grow(len(prose))
	pendingSpace := false
	for _, r := range prose {
		if unicode.IsSpace(r) {
			pendingSpace = true
			continue
		}
		if pendingSpace && b.Len() > 0 {
			b.WriteByte(' ')
		}
		pendingSpace = false
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

func hasNearDupSyntax(content string) bool {
	for _, r := range content {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) && !unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

// NearDupCodeContent returns the complete comparison body when it contains
// syntax, or an empty string for plain prose. The weaker session/title rule
// must compare the whole syntax-bearing body, including unpunctuated operands.
func NearDupCodeContent(content string) string {
	normalized := NormalizeContentForHash(content)
	if hasNearDupSyntax(normalized) {
		return normalized
	}
	return ""
}

// hashNormalized returns the SHA-256 hex digest of NormalizeContentForHash
// (content), or the empty string when only removable sentence marks and
// whitespace remain. Sentence-marks-only bodies ("...", "!!!") normalize to
// the empty string; treating that as "no near-duplicate key" keeps them from
// colliding with each other instead of matching on nothing.
func hashNormalized(content string) string {
	normalized := NormalizeContentForHash(content)
	if normalized == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// resolveNearDupWindow maps the configured window to the effective one. An
// unset window selects DefaultNearDupWindow; a pointer to a zero duration is
// the operator's explicit "0s" and disables near-duplicate merging while
// leaving exact-hash dedup in place.
func resolveNearDupWindow(configured *time.Duration) time.Duration {
	if configured == nil {
		return DefaultNearDupWindow
	}
	if *configured <= 0 {
		return 0
	}
	return *configured
}

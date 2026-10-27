package storage

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// This file holds the storage half of near-duplicate save folding (design
// D1-D3). The memory layer owns the derived key: it normalizes content and
// stores the digest on the observation. Storage only compares keys, so it can
// never redefine the legacy content hash.
//
// Every probe and the fold run inside the caller's write transaction. That is
// the point of doing this here rather than in the service: a lookup that misses
// and an insert that follows must not straddle another writer, or two
// near-identical saves arriving together would both insert.

// nearDupHit is one resolved probe: the survivor a save folds into and the rule
// that matched it.
type nearDupHit struct {
	survivor *memory.Observation
	reason   memory.NearDupReason
}

// nearDupLivePredicate is the live-row shape every near-duplicate probe shares,
// plus the evidence exclusion: a candidate that carries evidence references is a
// versioned assertion with locators of its own, and folding into it would mix
// two version identities. Callers bind it with three timestamp parameters, all
// the same "now": the candidate must already be effective (valid_from <= now),
// must not be retired (valid_until unset or later than now), and must not have
// expired (expires_at unset or later than now). A future-effective row is never
// a survivor: folding into it would hide the incoming save behind an
// observation the retrieval filters cannot see yet. valid_from is NOT NULL by
// schema, so the plain comparison carries the whole null contract.
const nearDupLivePredicate = `invalidated_at IS NULL
	   AND valid_from <= ?
	   AND (valid_until IS NULL OR valid_until > ?)
	   AND valid_until IS NULL
	   AND (expires_at IS NULL OR expires_at > ?)
	   AND NOT EXISTS (SELECT 1 FROM observation_evidence e WHERE e.observation_id = observations.id)`

// nearDupEligible reports whether a near-duplicate rule may fold this incoming
// save at all.
//
// A save that declares evidence references is a versioned assertion, not a
// re-typing: its locators are identity metadata a fold would drop, so those
// saves keep exact-hash dedup only. A dream journal is written by the fenced
// pipeline under its cycle ID and never participates in folding. Replacement
// saves never reach the probes: SaveEvolution resolves them on its own branch.
func nearDupEligible(o *memory.Observation) bool {
	if o == nil {
		return false
	}
	if len(o.Evidence) > 0 {
		return false
	}
	return o.Type != memory.TypeDream
}

// resolveNearDupTx applies the near-duplicate rules to one incoming save in
// design order: normalized content hash first, then session and title. It
// returns nil when nothing inside the window matches, including when merging is
// disabled (window <= 0), when normalization left no key, and when the save is
// not eligible. A nil result is not a failure: the caller inserts normally.
//
// "Inside the window" is measured from the candidate's last touch
// (COALESCE(last_accessed_at, created_at)) for the content-hash rule, so a row
// that keeps being re-saved keeps being mergeable, and from created_at for the
// session/title rule, whose index is (session_id, title, created_at). Both are
// inclusive at the cutoff: a candidate touched exactly one window ago still
// merges.
func resolveNearDupTx(ctx context.Context, tx *sql.Tx, o *memory.Observation, window time.Duration, now time.Time) (*nearDupHit, error) {
	if window <= 0 || !nearDupEligible(o) || !o.ValidFrom.Equal(o.CreatedAt) || o.ValidUntil != nil {
		return nil, nil
	}
	cutoff := now.UTC().Add(-window)
	if o.ContentHashNormalized != "" {
		survivor, err := nearDupByNormalizedHashTx(ctx, tx, o, cutoff, now)
		if err != nil {
			return nil, err
		}
		if survivor != nil {
			return &nearDupHit{survivor: survivor, reason: memory.NearDupReasonContentHash}, nil
		}
	}
	if o.SessionID != "" && o.Title != "" {
		survivor, err := nearDupBySessionTitleTx(ctx, tx, o, cutoff, now)
		if err != nil {
			return nil, err
		}
		if survivor != nil {
			return &nearDupHit{survivor: survivor, reason: memory.NearDupReasonSessionTitle}, nil
		}
	}
	return nil, nil
}

// nearDupByNormalizedHashTx finds the live observation in the same
// (agent, project, topic, trust tier, type, pin state, expiry) whose content normalizes identically to
// the incoming save and that was touched inside the window. Scope matches the
// exact-hash rule so a near-duplicate cannot cross a boundary the exact rule
// respects: topics group assertions, projects separate scopes, and trust tiers
// are the quarantine boundary — folding curated content into a raw survivor (or
// the reverse) would move an assertion across that boundary silently.
func nearDupByNormalizedHashTx(ctx context.Context, tx *sql.Tx, o *memory.Observation, cutoff, now time.Time) (*memory.Observation, error) {
	rows, err := tx.QueryContext(ctx, selectObsSQL+` WHERE agent_id = ?
		   AND COALESCE(project, '') = ?
		   AND COALESCE(topic_key, '') = ?
		   AND trust_tier = ?
		   AND obs_type = ? AND pinned = ?
		   AND expires_at IS ?
		   AND content_hash_normalized = ?
		   AND `+nearDupLivePredicate+`
		   AND COALESCE(last_accessed_at, created_at) >= ?
		 ORDER BY COALESCE(last_accessed_at, created_at) DESC, created_at DESC, id DESC`,
		o.AgentID, o.Project, o.TopicKey, string(o.TrustTier), string(o.Type), pinnedInt(o.Pinned), nullableTime(o.ExpiresAt), o.ContentHashNormalized, now.UTC(), now.UTC(), now.UTC(), cutoff.UTC())
	if err != nil {
		return nil, fmt.Errorf("storage: probe near-duplicate content: %w", err)
	}
	return firstNormalizedObs(rows, o.Content, "near-duplicate content")
}

// Stored keys may have been written under an older normalization rule. Verify
// each candidate's content before returning it as either a match or a hint.
func firstNormalizedObs(rows *sql.Rows, content, what string) (*memory.Observation, error) {
	obs, err := scanObsList(rows)
	closeErr := rows.Close()
	if err != nil {
		return nil, fmt.Errorf("storage: scan %s: %w", what, err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("storage: close %s probe: %w", what, closeErr)
	}
	want := memory.NormalizeContentForHash(content)
	for i := range obs {
		if memory.NormalizeContentForHash(obs[i].Content) == want {
			return &obs[i], nil
		}
	}
	return nil, nil
}

// nearDupBySessionTitleTx finds the live observation carrying the same session
// id and the same title, created inside the window. This is the weaker rule:
// the bodies may differ beyond normalization, which is why the merged-away body
// is kept verbatim in the provenance record. Agent, project and topic must also
// match so a session shared across scopes cannot mix observations. Matching
// numeric tokens and the complete syntax-bearing body keep this rule from
// undoing the content rule's distinctions between facts such as != and =.
//
// The probe also requires a non-NULL content_hash_normalized. That column is the
// near-duplicate key, and NULL means "written before the rule existed": the
// migration adds it without backfilling, so a pre-existing row must never become
// a survivor by any route. The content-hash rule excludes those rows on its own
// (NULL never equals a digest); this guard gives the session/title rule — which
// never looks at the key — the same guarantee.
func nearDupBySessionTitleTx(ctx context.Context, tx *sql.Tx, o *memory.Observation, cutoff, now time.Time) (*memory.Observation, error) {
	rows, err := tx.QueryContext(ctx, selectObsSQL+` WHERE session_id = ?
		   AND title = ?
		   AND agent_id = ?
		   AND COALESCE(project, '') = ?
		   AND COALESCE(topic_key, '') = ?
		   AND trust_tier = ?
		   AND obs_type = ? AND pinned = ?
		   AND expires_at IS ?
		   AND content_hash_normalized IS NOT NULL
		   AND `+nearDupLivePredicate+`
		   AND created_at >= ?
		 ORDER BY created_at DESC, id DESC`,
		o.SessionID, o.Title, o.AgentID, o.Project, o.TopicKey, string(o.TrustTier), string(o.Type), pinnedInt(o.Pinned), nullableTime(o.ExpiresAt), now.UTC(), now.UTC(), now.UTC(), cutoff.UTC())
	if err != nil {
		return nil, fmt.Errorf("storage: probe near-duplicate session title: %w", err)
	}
	obs, err := scanObsList(rows)
	closeErr := rows.Close()
	if err != nil {
		return nil, fmt.Errorf("storage: scan near-duplicate session title: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("storage: close near-duplicate session title: %w", closeErr)
	}
	codeContent := memory.NearDupCodeContent(o.Content)
	for i := range obs {
		if slices.Equal(numericTokens(obs[i].Content), numericTokens(o.Content)) &&
			memory.NearDupCodeContent(obs[i].Content) == codeContent {
			return &obs[i], nil
		}
	}
	return nil, nil
}

// Numeric changes can change a fact even when session and title match.
func numericTokens(content string) []string {
	runes := []rune(content)
	var tokens []string
	var token strings.Builder
	for i, r := range runes {
		startsNumber := i+1 < len(runes) && (unicode.IsDigit(runes[i+1]) || (runes[i+1] == '.' && i+2 < len(runes) && unicode.IsDigit(runes[i+2])))
		if (r == '-' || r == '+' || r == '−') && token.Len() == 0 && startsNumber {
			token.WriteRune(r)
			continue
		}
		if unicode.IsDigit(r) || (i+1 < len(runes) && unicode.IsDigit(runes[i+1]) && (r == '.' || (r == ',' && i > 0 && unicode.IsDigit(runes[i-1])))) {
			token.WriteRune(r)
		} else if token.Len() != 0 {
			tokens = append(tokens, token.String())
			token.Reset()
		}
	}
	if token.Len() != 0 {
		tokens = append(tokens, token.String())
	}
	return tokens
}

// nearDupOutsideWindowTx reports a live, normalized-identical observation that
// sits outside the merge window. It never merges: the design calls this a hint,
// so the save proceeds and the result names the candidate. The probe runs only
// on the miss path, so the cost is one index lookup on a save that already
// looked twice.
func nearDupOutsideWindowTx(ctx context.Context, tx *sql.Tx, o *memory.Observation, window time.Duration, now time.Time) (*memory.Observation, error) {
	if window <= 0 || o.ContentHashNormalized == "" || !nearDupEligible(o) {
		return nil, nil
	}
	cutoff := now.UTC().Add(-window)
	rows, err := tx.QueryContext(ctx, selectObsSQL+` WHERE agent_id = ?
		   AND COALESCE(project, '') = ?
		   AND COALESCE(topic_key, '') = ?
		   AND trust_tier = ?
		   AND obs_type = ? AND pinned = ?
		   AND expires_at IS ?
		   AND content_hash_normalized = ?
		   AND `+nearDupLivePredicate+`
		   AND COALESCE(last_accessed_at, created_at) < ?
		 ORDER BY created_at DESC, id DESC`,
		o.AgentID, o.Project, o.TopicKey, string(o.TrustTier), string(o.Type), pinnedInt(o.Pinned), nullableTime(o.ExpiresAt), o.ContentHashNormalized, now.UTC(), now.UTC(), now.UTC(), cutoff.UTC())
	if err != nil {
		return nil, fmt.Errorf("storage: probe out-of-window near-duplicate: %w", err)
	}
	return firstNormalizedObs(rows, o.Content, "out-of-window near-duplicate")
}

// foldNearDupTx performs the merge: bump the survivor's access signal and write
// one provenance row that carries the merged-away body verbatim, so the fold is
// auditable and available for manual recovery. Nothing
// here touches the survivor's content, hashes, timestamps or trust tier: a fold
// is an access signal plus a record, never an edit.
//
// The provenance row is written once and read only by audit queries. It is not
// indexed by FTS and no retrieval path reads it, so it cannot move a score.
func foldNearDupTx(ctx context.Context, tx *sql.Tx, survivorID string, o *memory.Observation, reason memory.NearDupReason, now time.Time) (*memory.NearDupMerge, error) {
	if survivorID == "" {
		return nil, fmt.Errorf("storage: near-duplicate merge without survivor")
	}
	if !reason.Valid() {
		return nil, fmt.Errorf("storage: invalid near-duplicate reason %q", reason)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE observations SET access_count = access_count + 1, last_accessed_at = ? WHERE id = ?`,
		now.UTC(), survivorID); err != nil {
		return nil, fmt.Errorf("storage: bump near-duplicate survivor: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO observation_near_dups(
			survivor_id, incoming_content, incoming_hash, normalized_hash,
			reason, agent_id, project, session_id, title, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		survivorID, o.Content, o.ContentHash, o.ContentHashNormalized,
		string(reason), o.AgentID, nullableStr(o.Project), nullableStr(o.SessionID),
		o.Title, now.UTC()); err != nil {
		return nil, fmt.Errorf("storage: record near-duplicate provenance: %w", err)
	}
	return &memory.NearDupMerge{
		SurvivorID:      survivorID,
		Reason:          reason,
		IncomingContent: o.Content,
		IncomingHash:    o.ContentHash,
		NormalizedHash:  o.ContentHashNormalized,
	}, nil
}

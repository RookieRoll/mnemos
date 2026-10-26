package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

func writeObservationTx(ctx context.Context, tx *sql.Tx, o *memory.Observation) error {
	if o.AgentID == "" {
		o.AgentID = "default"
	}
	if o.SourceKind == "" {
		o.SourceKind = memory.SourceUser
	}
	if o.TrustTier == "" {
		o.TrustTier = memory.TrustCurated
	}
	tags, err := json.Marshal(coalesceSliceStr(o.Tags))
	if err != nil {
		return fmt.Errorf("storage: encode tags: %w", err)
	}
	parents, err := json.Marshal(coalesceSliceStr(o.DerivedFrom))
	if err != nil {
		return fmt.Errorf("storage: encode parents: %w", err)
	}
	// content_hash_normalized is the derived near-duplicate key. It is written
	// exactly as the memory layer computed it and never inferred here: an empty
	// value stays NULL, which is also what every pre-0012 row carries, so a
	// NULL never matches a near-duplicate probe.
	_, err = tx.ExecContext(ctx, `INSERT INTO observations (`+obsColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.ID, nullableStr(o.SessionID), o.AgentID, nullableStr(o.Project), o.Title, o.Content, string(o.Type), string(tags), o.Importance,
		o.AccessCount, nullableTime(o.LastAccessedAt), o.CreatedAt.UTC(), o.ValidFrom.UTC(), nullableTime(o.ValidUntil), nullableTime(o.InvalidatedAt), nullableTime(o.ExpiresAt), nullableStr(o.ContentHash), nullableStr(o.Structured), nullableStr(o.Rationale), encodeVector(o.Embedding), nullableStr(o.EmbeddingModel), nullableTime(o.LastExportedAt), string(o.SourceKind), string(o.TrustTier), string(parents), pinnedInt(o.Pinned), nullableStr(o.TopicKey), nullableStr(o.ContentHashNormalized))
	if err != nil {
		return fmt.Errorf("storage: insert observation version: %w", err)
	}
	return writeEvidenceTx(ctx, tx, o.ID, o.Evidence)
}

func writeEvidenceTx(ctx context.Context, tx *sql.Tx, id string, refs []memory.EvidenceReference) error {
	for _, ref := range refs {
		var checked any
		if ref.CheckedAt != nil {
			checked = ref.CheckedAt.UTC().Format(time.RFC3339Nano)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO observation_evidence(observation_id,evidence_key,locator,relation,revision,selector,fingerprint,status,checked_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(observation_id,evidence_key) DO NOTHING`, id, ref.Key, ref.Locator, ref.Relation, ref.Revision, ref.Selector, ref.Fingerprint, ref.Status, checked)
		if err != nil {
			return fmt.Errorf("storage: insert evidence: %w", err)
		}
	}
	return nil
}

func startEvolutionWrite(ctx context.Context, db *sql.DB) (*sql.Tx, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("storage: begin evolution: %w", err)
	}
	// Open configures BEGIN IMMEDIATE for writable transactions. A deferred
	// BEGIN followed by a no-op UPDATE can still lose a schema/read-to-write
	// upgrade race; SQLITE_BUSY then bypasses the configured busy timeout.
	return tx, nil
}

func (s *obsStore) insertWithEvidence(ctx context.Context, o *memory.Observation) error {
	refs, err := memory.NormalizeEvidence(o.Evidence, true)
	if err != nil {
		return err
	}
	o.Evidence = refs
	topic, err := memory.NormalizeTopic(o.TopicKey)
	if err != nil {
		return err
	}
	o.TopicKey = topic
	tx, err := startEvolutionWrite(ctx, s.db)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := writeObservationTx(ctx, tx, o); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: commit observation: %w", err)
	}
	return nil
}

func normalizeRestoreOwner(o memory.Observation) (memory.Observation, error) {
	return memory.NormalizeRestoreObservation(o)
}

// sameOwnerIdentity is the restore identity check. It deliberately ignores
// content_hash_normalized: an older dump has no such field, and comparing it
// would make a faithful restore of pre-0012 data conflict with nothing. The
// derived key is carried through verbatim when present and never used to decide
// identity.
func sameOwnerIdentity(a, b memory.Observation) bool {
	// Old snapshots have no new metadata; skipping must not erase a later topic.
	if b.TopicKey == "" && len(b.Evidence) == 0 {
		b.TopicKey = a.TopicKey
	}
	return a.ID == b.ID && a.AgentID == b.AgentID && a.Project == b.Project && a.SessionID == b.SessionID && a.Title == b.Title && a.Content == b.Content && a.Type == b.Type && a.CreatedAt.Equal(b.CreatedAt) && a.ValidFrom.Equal(b.ValidFrom) && a.TopicKey == b.TopicKey && a.ContentHash == b.ContentHash && a.Rationale == b.Rationale && a.Structured == b.Structured && a.SourceKind == b.SourceKind && reflect.DeepEqual(coalesceSliceStr(a.Tags), coalesceSliceStr(b.Tags)) && reflect.DeepEqual(coalesceSliceStr(a.DerivedFrom), coalesceSliceStr(b.DerivedFrom))
}

func (s *obsStore) restoreWithEvidence(ctx context.Context, input *memory.Observation) (bool, error) {
	o, err := normalizeRestoreOwner(*input)
	if err != nil {
		return false, err
	}
	tx, err := startEvolutionWrite(ctx, s.db)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	existing, err := scanObs(tx.QueryRowContext(ctx, selectObsSQL+" WHERE id=?", o.ID))
	inserted := false
	if err == nil {
		if !sameOwnerIdentity(*existing, o) {
			return false, fmt.Errorf("storage: restored owner %q differs: %w", o.ID, memory.ErrConflict)
		}
		if len(o.Evidence) > 0 && !sameEvidenceIdentity(existing.Evidence, o.Evidence) {
			return false, fmt.Errorf("storage: restored evidence identity differs: %w", memory.ErrConflict)
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		if err := writeObservationTx(ctx, tx, &o); err != nil {
			return false, err
		}
		inserted = true
	} else {
		return false, fmt.Errorf("storage: read restore owner: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("storage: commit restore owner: %w", err)
	}
	return inserted, nil
}

func sameEvidenceIdentity(a, b []memory.EvidenceReference) bool {
	if len(a) != len(b) {
		return false
	}
	keys := map[string]bool{}
	for _, r := range a {
		keys[r.Key] = true
	}
	for _, r := range b {
		if !keys[r.Key] {
			return false
		}
	}
	return true
}

// SaveEvolution atomically resolves scoped deduplication, replacement replay,
// evidence ownership, and lifecycle writes under a single SQLite reservation.
//
// Dedup runs in the order the design fixes: the exact content hash first, then
// the near-duplicate layers bounded by in.NearDupWindow (normalized content
// hash, then session and title). A near-duplicate hit bumps the survivor's
// access signal, retains the merged-away body verbatim in
// observation_near_dups, and returns the survivor — no second live row. A
// normalized-identical row outside the window is reported as a hint and the
// save inserts.
func (s *obsStore) SaveEvolution(ctx context.Context, in memory.EvolutionSave) (*memory.SaveResult, error) {
	tx, err := startEvolutionWrite(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	o := in.Observation
	// nearDupHint is set when a normalized-identical observation exists outside
	// the merge window: the save still inserts, and the result names the
	// candidate without touching it.
	var nearDupHint string
	if in.ReplacesID != "" {
		rows, err := tx.QueryContext(ctx, "SELECT source_id,COALESCE(replacement_hash,'') FROM observation_links WHERE target_id=? AND link_type='supersedes'", in.ReplacesID)
		if err != nil {
			return nil, fmt.Errorf("storage: read successor: %w", err)
		}
		var successor, hash string
		count := 0
		for rows.Next() {
			if err := rows.Scan(&successor, &hash); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("storage: scan successor: %w", err)
			}
			count++
		}
		scanErr := rows.Err()
		_ = rows.Close()
		if scanErr != nil {
			return nil, fmt.Errorf("storage: scan successor: %w", scanErr)
		}
		if count > 0 {
			if count != 1 || hash == "" || hash != in.RequestHash {
				return nil, fmt.Errorf("storage: competing replacement: %w", memory.ErrConflict)
			}
			existing, err := scanObs(tx.QueryRowContext(ctx, selectObsSQL+" WHERE id=?", successor))
			if err != nil {
				return nil, fmt.Errorf("storage: read replay result: %w", err)
			}
			if err := tx.Commit(); err != nil {
				return nil, fmt.Errorf("storage: commit replacement replay: %w", err)
			}
			return &memory.SaveResult{Observation: existing, Deduped: true}, nil
		}
		old, err := scanObs(tx.QueryRowContext(ctx, selectObsSQL+" WHERE id=?", in.ReplacesID))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("storage: replacement target unavailable: %w", memory.ErrConflict)
		}
		if err != nil {
			return nil, fmt.Errorf("storage: read target: %w", err)
		}
		if err := memory.ReplacementPolicy(o, old, in.Now, in.UnpinReplacement, in.TopicExplicit); err != nil {
			return nil, err
		}
	} else {
		rows, err := tx.QueryContext(ctx, selectObsSQL+` WHERE agent_id=? AND COALESCE(project,'')=? AND content_hash=? AND COALESCE(topic_key,'')=? AND invalidated_at IS NULL AND (valid_until IS NULL OR valid_until>?) AND (expires_at IS NULL OR expires_at>?) ORDER BY created_at,id`, o.AgentID, o.Project, o.ContentHash, o.TopicKey, in.Now, in.Now)
		if err != nil {
			return nil, fmt.Errorf("storage: read dedup candidates: %w", err)
		}
		candidates, scanErr := scanObsList(rows)
		_ = rows.Close()
		if scanErr != nil {
			return nil, fmt.Errorf("storage: scan dedup candidates: %w", scanErr)
		}
		for _, existing := range candidates {
			if sameEvidenceIdentity(existing.Evidence, o.Evidence) {
				if _, err := tx.ExecContext(ctx, "UPDATE observations SET access_count=access_count+1,last_accessed_at=? WHERE id=?", in.Now, existing.ID); err != nil {
					return nil, fmt.Errorf("storage: bump dedup access: %w", err)
				}
				if err := tx.Commit(); err != nil {
					return nil, fmt.Errorf("storage: commit dedup: %w", err)
				}
				return &memory.SaveResult{Observation: &existing, Deduped: true}, nil
			}
		}
		// Near-duplicate layers, in design order and only after the exact hash
		// missed. Lookup, merge, and commit stay in this transaction: a fold is
		// one write decision, never a read here and an insert there.
		if hit, err := resolveNearDupTx(ctx, tx, o, in.NearDupWindow, in.Now); err != nil {
			return nil, err
		} else if hit != nil {
			merge, err := foldNearDupTx(ctx, tx, hit.survivor.ID, o, hit.reason, in.Now)
			if err != nil {
				return nil, err
			}
			if err := tx.Commit(); err != nil {
				return nil, fmt.Errorf("storage: commit near-duplicate merge: %w", err)
			}
			return &memory.SaveResult{Observation: hit.survivor, Deduped: true, NearDup: merge}, nil
		}
		// Miss path only: report a normalized-identical row outside the window
		// as a hint. The save below still inserts; nothing is merged.
		hint, err := nearDupOutsideWindowTx(ctx, tx, o, in.NearDupWindow, in.Now)
		if err != nil {
			return nil, err
		}
		if hint != nil {
			nearDupHint = hint.ID
		}
	}
	if err := writeObservationTx(ctx, tx, o); err != nil {
		return nil, err
	}
	if in.ReplacesID != "" {
		if err := linkAndInvalidateTx(ctx, tx, o.ID, in.ReplacesID, in.Now, o.ValidFrom, in.RequestHash); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("storage: commit evolution save: %w", err)
	}
	return &memory.SaveResult{Observation: o, NearDupHint: nearDupHint}, nil
}

func linkAndInvalidateTx(ctx context.Context, tx *sql.Tx, newID, oldID string, now, effective time.Time, hash string) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO observation_links(source_id,target_id,link_type,created_at,replacement_hash) VALUES(?,?,'supersedes',?,?)", newID, oldID, now, nullableStr(hash))
	if err != nil {
		return fmt.Errorf("storage: write supersession: %w", err)
	}
	result, err := tx.ExecContext(ctx, "UPDATE observations SET invalidated_at=?,valid_until=? WHERE id=? AND invalidated_at IS NULL", now, effective, oldID)
	if err != nil {
		return fmt.Errorf("storage: invalidate superseded version: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: count invalidation: %w", err)
	}
	if n != 1 {
		return fmt.Errorf("storage: target changed: %w", memory.ErrConflict)
	}
	return nil
}

// SupersedeAtomic applies the same scope/trust/pin/lifecycle policy to existing
// versions, without leaving a link behind if invalidation fails.
func (s *obsStore) SupersedeAtomic(ctx context.Context, newID, oldID string, at time.Time) error {
	if newID == oldID {
		return fmt.Errorf("storage: self supersession: %w", memory.ErrConflict)
	}
	tx, err := startEvolutionWrite(ctx, s.db)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM observation_links WHERE source_id=? AND target_id=? AND link_type='supersedes'", newID, oldID).Scan(&count); err != nil {
		return fmt.Errorf("storage: read existing supersession: %w", err)
	}
	if count > 0 {
		return tx.Commit()
	}
	if err := tx.QueryRowContext(ctx, `WITH RECURSIVE ancestors(id) AS (SELECT target_id FROM observation_links WHERE source_id=? AND link_type='supersedes' UNION SELECT l.target_id FROM observation_links l JOIN ancestors a ON l.source_id=a.id WHERE l.link_type='supersedes') SELECT count(*) FROM ancestors WHERE id=?`, oldID, newID).Scan(&count); err != nil {
		return fmt.Errorf("storage: check supersession cycle: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("storage: supersession cycle: %w", memory.ErrConflict)
	}
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM observation_links WHERE target_id=? AND link_type='supersedes'", oldID).Scan(&count); err != nil {
		return fmt.Errorf("storage: check successor: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("storage: conflicting successor: %w", memory.ErrConflict)
	}
	next, err := scanObs(tx.QueryRowContext(ctx, selectObsSQL+" WHERE id=?", newID))
	if errors.Is(err, sql.ErrNoRows) {
		return memory.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("storage: read source version: %w", err)
	}
	old, err := scanObs(tx.QueryRowContext(ctx, selectObsSQL+" WHERE id=?", oldID))
	if errors.Is(err, sql.ErrNoRows) {
		return memory.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("storage: read target version: %w", err)
	}
	if !next.Live(at) {
		return fmt.Errorf("storage: superseding version is not live: %w", memory.ErrConflict)
	}
	if err := memory.ReplacementPolicy(next, old, at, false, next.TopicKey != ""); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE observations SET pinned=?,expires_at=?,topic_key=? WHERE id=?", pinnedInt(next.Pinned), nullableTime(next.ExpiresAt), nullableStr(next.TopicKey), next.ID); err != nil {
		return fmt.Errorf("storage: inherit replacement policy: %w", err)
	}
	if err := linkAndInvalidateTx(ctx, tx, newID, oldID, at, at, ""); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: commit existing supersession: %w", err)
	}
	return nil
}

// CheckEvidenceState updates only the latest caller marker after validating its
// immutable fingerprint baseline in the same write transaction.
func (s *obsStore) CheckEvidenceState(ctx context.Context, in memory.EvidenceCheckInput, at time.Time) (*memory.EvidenceReference, error) {
	tx, err := startEvolutionWrite(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	ref := &memory.EvidenceReference{Key: in.EvidenceKey, Status: in.Status}
	err = tx.QueryRowContext(ctx, "SELECT locator,relation,revision,selector,fingerprint FROM observation_evidence WHERE observation_id=? AND evidence_key=?", in.ObservationID, in.EvidenceKey).Scan(&ref.Locator, &ref.Relation, &ref.Revision, &ref.Selector, &ref.Fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, memory.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("storage: read evidence: %w", err)
	}
	if in.Status == "matched" && ref.Fingerprint == "" {
		return nil, fmt.Errorf("storage: matched check needs fingerprint baseline: %w", memory.ErrInvalidEvolutionInput)
	}
	_, err = tx.ExecContext(ctx, "UPDATE observation_evidence SET status=?,checked_at=? WHERE observation_id=? AND evidence_key=?", in.Status, at.UTC().Format(time.RFC3339Nano), in.ObservationID, in.EvidenceKey)
	if err != nil {
		return nil, fmt.Errorf("storage: update latest evidence marker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("storage: commit evidence check: %w", err)
	}
	stamp := at.UTC()
	ref.CheckedAt = &stamp
	return ref, nil
}

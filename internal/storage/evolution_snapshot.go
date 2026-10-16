package storage

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/polyxmedia/mnemos/internal/memory"
)

func historicalLinksTx(ctx context.Context, tx *sql.Tx) ([]memory.LinkEdge, error) {
	rows, err := tx.QueryContext(ctx, `SELECT l.source_id,l.target_id,l.link_type,l.created_at,COALESCE(l.replacement_hash,''),s.title,s.agent_id,s.created_at,s.trust_tier,t.title,t.agent_id,t.created_at,t.trust_tier FROM observation_links l JOIN observations s ON s.id=l.source_id JOIN observations t ON t.id=l.target_id WHERE l.link_type='supersedes' ORDER BY l.created_at,l.source_id,l.target_id,l.link_type`)
	if err != nil {
		return nil, fmt.Errorf("storage: export historical links: %w", err)
	}
	defer rows.Close()
	out := []memory.LinkEdge{}
	for rows.Next() {
		var e memory.LinkEdge
		if err := rows.Scan(&e.SourceID, &e.TargetID, &e.LinkType, &e.LinkedAt, &e.ReplacementHash, &e.SourceTitle, &e.SourceAgent, &e.SourceCreatedAt, &e.SourceTrustTier, &e.TargetTitle, &e.TargetAgent, &e.TargetCreatedAt, &e.TargetTrustTier); err != nil {
			return nil, fmt.Errorf("storage: scan historical link: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: drain historical links: %w", err)
	}
	return out, nil
}

// ExportEvolutionSnapshot reads owners, inline evidence, and all historical links
// in a coherent non-mutating transaction with no nested connection-pool reads.
func (s *obsStore) ExportEvolutionSnapshot(ctx context.Context) ([]memory.Observation, []memory.LinkEdge, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, fmt.Errorf("storage: begin fidelity snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, selectObsSQL+" ORDER BY created_at,id")
	if err != nil {
		return nil, nil, fmt.Errorf("storage: export snapshot owners: %w", err)
	}
	owners, scanErr := scanObsList(rows)
	_ = rows.Close()
	if scanErr != nil {
		return nil, nil, fmt.Errorf("storage: scan snapshot owners: %w", scanErr)
	}
	links, err := historicalLinksTx(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("storage: finish fidelity snapshot: %w", err)
	}
	return owners, links, nil
}

// ExportHistoricalLinks includes invalidated endpoints and does not bump access.
func (s *obsStore) ExportHistoricalLinks(ctx context.Context) ([]memory.LinkEdge, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("storage: begin link snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	links, err := historicalLinksTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("storage: finish link snapshot: %w", err)
	}
	return links, nil
}

func validateRestoreTx(ctx context.Context, tx *sql.Tx, owners []memory.Observation, links []memory.LinkEdge, requireExisting bool) error {
	byID := map[string]memory.Observation{}
	for _, raw := range owners {
		o, err := normalizeRestoreOwner(raw)
		if err != nil {
			return err
		}
		if o.ID == "" {
			return fmt.Errorf("storage: restore owner ID is required")
		}
		if _, exists := byID[o.ID]; exists {
			return fmt.Errorf("storage: duplicate restore owner %q: %w", o.ID, memory.ErrConflict)
		}
		byID[o.ID] = o
		existing, err := scanObs(tx.QueryRowContext(ctx, selectObsSQL+" WHERE id=?", o.ID))
		if err == nil {
			if !sameOwnerIdentity(*existing, o) {
				return fmt.Errorf("storage: restore owner %q identity differs: %w", o.ID, memory.ErrConflict)
			}
			if len(o.Evidence) > 0 && !sameEvidenceIdentity(existing.Evidence, o.Evidence) {
				return fmt.Errorf("storage: restore owner %q evidence identity differs: %w", o.ID, memory.ErrConflict)
			}
		} else if errors.Is(err, sql.ErrNoRows) {
			if requireExisting {
				return fmt.Errorf("storage: missing metadata owner %q", o.ID)
			}
		} else {
			return fmt.Errorf("storage: validate restore owner: %w", err)
		}
	}
	graph := map[string][]string{}
	successors := map[string]string{}
	registerSuccessor := func(source, target string) error {
		if existing, ok := successors[target]; ok && existing != source {
			return fmt.Errorf("storage: multiple successors for target %q: %w", target, memory.ErrConflict)
		}
		successors[target] = source
		return nil
	}
	existingLinks, err := historicalLinksTx(ctx, tx)
	if err != nil {
		return err
	}
	for _, link := range existingLinks {
		if link.LinkType == memory.LinkSupersedes {
			if err := registerSuccessor(link.SourceID, link.TargetID); err != nil {
				return err
			}
			graph[link.SourceID] = append(graph[link.SourceID], link.TargetID)
		}
	}
	for _, link := range links {
		if link.LinkType != memory.LinkSupersedes {
			return fmt.Errorf("storage: invalid restored link type %q", link.LinkType)
		}
		if _, ok := byID[link.SourceID]; !ok {
			return fmt.Errorf("storage: orphan link source %q", link.SourceID)
		}
		if _, ok := byID[link.TargetID]; !ok {
			return fmt.Errorf("storage: orphan link target %q", link.TargetID)
		}
		if link.ReplacementHash != "" {
			decoded, err := hex.DecodeString(link.ReplacementHash)
			if err != nil || len(decoded) != 32 || link.LinkType != memory.LinkSupersedes {
				return fmt.Errorf("storage: invalid replacement request identity")
			}
		}
		if link.LinkType == memory.LinkSupersedes {
			if err := registerSuccessor(link.SourceID, link.TargetID); err != nil {
				return err
			}
			if link.SourceID == link.TargetID {
				return fmt.Errorf("storage: restored supersession self-cycle: %w", memory.ErrConflict)
			}
			graph[link.SourceID] = append(graph[link.SourceID], link.TargetID)
		}
		var hash string
		var stamp sql.NullTime
		err := tx.QueryRowContext(ctx, "SELECT created_at,COALESCE(replacement_hash,'') FROM observation_links WHERE source_id=? AND target_id=? AND link_type=?", link.SourceID, link.TargetID, link.LinkType).Scan(&stamp, &hash)
		if err == nil {
			if !stamp.Time.Equal(link.LinkedAt) || hash != link.ReplacementHash {
				return fmt.Errorf("storage: existing link identity differs: %w", memory.ErrConflict)
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("storage: check imported link: %w", err)
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(id string) bool {
		if visiting[id] {
			return false
		}
		if visited[id] {
			return true
		}
		visiting[id] = true
		for _, next := range graph[id] {
			if !visit(next) {
				return false
			}
		}
		visiting[id] = false
		visited[id] = true
		return true
	}
	for id := range graph {
		if !visit(id) {
			return fmt.Errorf("storage: imported supersession cycle: %w", memory.ErrConflict)
		}
	}
	return nil
}

// ValidateRestoreMetadata checks owner identities and all new-section orphans
// before the caller begins restoring any owner/session/skill rows.
func (s *obsStore) ValidateRestoreMetadata(ctx context.Context, owners []memory.Observation, links []memory.LinkEdge) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("storage: begin restore preflight: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateRestoreTx(ctx, tx, owners, links, false); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: finish restore preflight: %w", err)
	}
	return nil
}

// RestoreEvolutionSnapshot restores the observation portion of a fidelity dump
// atomically: validate existing identities, insert all owners, then refs and links.
func (s *obsStore) RestoreEvolutionSnapshot(ctx context.Context, owners []memory.Observation, links []memory.LinkEdge) (int, error) {
	tx, err := startEvolutionWrite(ctx, s.db)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := validateRestoreTx(ctx, tx, owners, links, false); err != nil {
		return 0, err
	}
	inserted := 0
	for _, raw := range owners {
		o, err := normalizeRestoreOwner(raw)
		if err != nil {
			return 0, err
		}
		var present int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM observations WHERE id=?", o.ID).Scan(&present); err != nil {
			return 0, fmt.Errorf("storage: check restore owner: %w", err)
		}
		if present == 0 {
			o.Evidence = nil
			if err := writeObservationTx(ctx, tx, &o); err != nil {
				return 0, err
			}
			inserted++
		}
	}
	for _, raw := range owners {
		o, err := normalizeRestoreOwner(raw)
		if err != nil {
			return 0, err
		}
		if err := writeEvidenceTx(ctx, tx, o.ID, o.Evidence); err != nil {
			return 0, err
		}
	}
	for _, link := range links {
		if _, err := tx.ExecContext(ctx, "INSERT INTO observation_links(source_id,target_id,link_type,created_at,replacement_hash) VALUES(?,?,?,?,?) ON CONFLICT(source_id,target_id,link_type) DO NOTHING", link.SourceID, link.TargetID, link.LinkType, link.LinkedAt.UTC(), nullableStr(link.ReplacementHash)); err != nil {
			return 0, fmt.Errorf("storage: restore historical link: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("storage: commit fidelity observations: %w", err)
	}
	return inserted, nil
}

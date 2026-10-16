package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

func scopedReadWhere(in memory.ScopedReadInput) (string, []any, error) {
	if in.Project == "" || in.AgentID == "" {
		return "", nil, fmt.Errorf("storage: scoped read needs project and agent: %w", memory.ErrInvalidReadInput)
	}
	where := "project = ? AND agent_id = ?"
	args := []any{in.Project, in.AgentID}
	if !in.IncludeRaw {
		where += " AND trust_tier != 'raw'"
	}
	if !in.IncludeStale {
		at := in.AsOf
		if at.IsZero() {
			at = in.Now
		}
		if at.IsZero() {
			return "", nil, fmt.Errorf("storage: scoped read needs a visibility instant")
		}
		where += ` AND valid_from <= ?
		 AND (invalidated_at IS NULL OR invalidated_at > ?)
		 AND (valid_until IS NULL OR valid_until > ?)
		 AND (expires_at IS NULL OR expires_at > ?)`
		args = append(args, at.UTC(), at.UTC(), at.UTC(), at.UTC())
	}
	return where, args, nil
}

// ReadScoped fetches one coherent scoped batch without changing access counts.
func (s *obsStore) ReadScoped(ctx context.Context, in memory.ScopedReadInput) ([]memory.Observation, error) {
	if len(in.IDs) < 1 || len(in.IDs) > 32 {
		return nil, fmt.Errorf("storage: scoped batch requires 1..32 IDs: %w", memory.ErrInvalidReadInput)
	}
	where, args, err := scopedReadWhere(in)
	if err != nil {
		return nil, err
	}
	marks := make([]string, len(in.IDs))
	for i, id := range in.IDs {
		marks[i] = "?"
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, selectObsSQL+" WHERE "+where+" AND id IN ("+strings.Join(marks, ",")+")", args...)
	if err != nil {
		return nil, fmt.Errorf("storage: read scoped batch: %w", err)
	}
	defer rows.Close()
	out, err := scanObsList(rows)
	if err != nil {
		return nil, fmt.Errorf("storage: scan scoped batch: %w", err)
	}
	return out, nil
}

// ReadNeighborhoodSnapshot reads the visible anchor and eligible neighbours in
// one transaction, draining each cursor before the next single-connection query.
func (s *obsStore) ReadNeighborhoodSnapshot(ctx context.Context, in memory.NeighborhoodReadInput) (*memory.NeighborhoodSnapshot, error) {
	if in.Before < 0 || in.Before > 10 || in.After < 0 || in.After > 10 {
		return nil, fmt.Errorf("storage: neighborhood limits must be 0..10: %w", memory.ErrInvalidReadInput)
	}
	where, args, err := scopedReadWhere(in.Scope)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("storage: begin neighborhood: %w", err)
	}
	defer tx.Rollback()
	anchorArgs := append(append([]any{}, args...), in.ID)
	anchor, err := scanObs(tx.QueryRowContext(ctx, selectObsSQL+" WHERE "+where+" AND id = ?", anchorArgs...))
	out := &memory.NeighborhoodSnapshot{}
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("storage: finish unavailable neighborhood: %w", err)
		}
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("storage: read neighborhood anchor: %w", err)
	}
	out.Anchor = anchor
	if anchor.SessionID != "" {
		load := func(before bool, limit int) ([]memory.Observation, bool, error) {
			comparison, order := ">", "ASC"
			if before {
				comparison, order = "<", "DESC"
			}
			query := selectObsSQL + " WHERE " + where + " AND session_id = ? AND (created_at " + comparison + " ? OR (created_at = ? AND id " + comparison + " ?)) ORDER BY created_at " + order + ", id " + order + " LIMIT ?"
			bind := append(append([]any{}, args...), anchor.SessionID, anchor.CreatedAt.UTC(), anchor.CreatedAt.UTC(), anchor.ID, limit+1)
			rows, err := tx.QueryContext(ctx, query, bind...)
			if err != nil {
				return nil, false, fmt.Errorf("storage: query neighborhood: %w", err)
			}
			list, scanErr := scanObsList(rows)
			closeErr := rows.Close()
			if scanErr != nil {
				return nil, false, fmt.Errorf("storage: scan neighborhood: %w", scanErr)
			}
			if closeErr != nil {
				return nil, false, fmt.Errorf("storage: close neighborhood: %w", closeErr)
			}
			more := len(list) > limit
			if more {
				list = list[:limit]
			}
			if before {
				for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
					list[i], list[j] = list[j], list[i]
				}
			}
			return list, more, nil
		}
		out.Before, out.HasMoreBefore, err = load(true, in.Before)
		if err != nil {
			return nil, err
		}
		out.After, out.HasMoreAfter, err = load(false, in.After)
		if err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("storage: finish neighborhood: %w", err)
	}
	return out, nil
}

// IncrementAccessMany atomically records each unique returned detail once. A
// missing ID or an SQL failure rolls back the complete aggregate update.
func (s *obsStore) IncrementAccessMany(ctx context.Context, ids []string, at time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > 32 {
		return fmt.Errorf("storage: detail access exceeds batch limit")
	}
	seen := map[string]bool{}
	var marks []string
	args := []any{at.UTC()}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			marks = append(marks, "?")
			args = append(args, id)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storage: begin detail access: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE observations SET access_count = access_count + 1, last_accessed_at = ? WHERE id IN ("+strings.Join(marks, ",")+")", args...)
	if err != nil {
		return fmt.Errorf("storage: increment detail access: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("storage: count detail access: %w", err)
	}
	if n != int64(len(marks)) {
		return fmt.Errorf("storage: detail disappeared during read: %w", memory.ErrNotFound)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storage: commit detail access: %w", err)
	}
	return nil
}

package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/polyxmedia/mnemos/internal/memory"
)

// EmbeddingBackfillUpperBound captures a finite lexical ID horizon without
// holding a read transaction open across provider network calls.
func (s *obsStore) EmbeddingBackfillUpperBound(ctx context.Context) (string, error) {
	var id string
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), '') FROM observations`).Scan(&id); err != nil {
		return "", fmt.Errorf("embedding backfill upper bound: %w", err)
	}
	return id, nil
}

// ListEmbeddingBackfill returns eligible live records in a bounded ID-keyset
// page. Advancing past failures prevents one unavailable record starving others.
func (s *obsStore) ListEmbeddingBackfill(ctx context.Context, in memory.EmbeddingBackfillInput) ([]memory.Observation, error) {
	if in.ThroughID == "" {
		return nil, nil
	}
	if in.Limit <= 0 || in.Limit > 1000 {
		return nil, fmt.Errorf("embedding backfill: limit must be 1..1000")
	}
	if in.RebuildIncompatible && (in.Model == "" || in.Dimension <= 0) {
		return nil, fmt.Errorf("embedding backfill: rebuilding requires model and dimension")
	}
	query := selectObsSQL + ` WHERE id > ? AND id <= ? AND (`
	args := []any{in.AfterID, in.ThroughID}
	query += `embedding IS NULL OR length(embedding)=0`
	if in.RebuildIncompatible {
		query += ` OR COALESCE(embedding_model,'') != ? OR length(embedding) != ?`
		args = append(args, in.Model, int64(in.Dimension)*4)
	}
	query += `) AND ` + liveObsSQL("") + ` ORDER BY id ASC LIMIT ?`
	now := time.Now().UTC()
	args = append(args, now, now, in.Limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("embedding backfill page: %w", err)
	}
	defer rows.Close()
	return scanObsList(rows)
}

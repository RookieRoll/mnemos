package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"unicode/utf8"

	"github.com/polyxmedia/mnemos/internal/continuity"
)

// Continuity returns the atomic persistence adapter for session progress.
func (d *DB) Continuity() continuity.Store { return &continuityStore{db: d.sql} }

type continuityStore struct{ db *sql.DB }

func (s *continuityStore) Open(ctx context.Context, in continuity.OpenRecord) (*continuity.OpenResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("storage: begin continuity open: %w", err)
	}
	defer tx.Rollback()
	result := &continuity.OpenResult{ProtocolVersion: 1, AgentID: in.Input.AgentID, Project: in.Input.Project, WorkspaceKey: in.Workspace.Key, WorkspaceRoot: in.Workspace.Root}
	var hash string
	var ended sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT b.session_id,b.start_request_hash,s.started_at,s.ended_at FROM session_continuity_bindings b JOIN sessions s ON s.id=b.session_id WHERE b.agent_id=? AND b.workspace_key=? AND b.harness_kind=? AND b.start_key=?`, in.Input.AgentID, in.Workspace.Key, in.Input.HarnessKind, in.Input.StartKey).Scan(&result.SessionID, &hash, &result.StartedAt, &ended)
	if err == nil {
		if hash != in.RequestHash || ended.Valid {
			return nil, continuity.ErrConflict
		}
		result.Status = "replayed"
	} else if errors.Is(err, sql.ErrNoRows) {
		if _, err = tx.ExecContext(ctx, `INSERT INTO sessions(id,agent_id,project,goal,status,outcome_tags,started_at,host_session_id) VALUES(?,?,?,?,'ok','[]',?,?)`, in.SessionID, in.Input.AgentID, nullableStr(in.Input.Project), nullableStr(in.Input.Goal), in.Now, nullableStr(in.Input.HostSessionID)); err != nil {
			return nil, fmt.Errorf("storage: insert continuity session: %w", err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO session_continuity_bindings(session_id,agent_id,workspace_key,workspace_root,harness_kind,host_session_id,start_key,start_request_hash) VALUES(?,?,?,?,?,?,?,?)`, in.SessionID, in.Input.AgentID, in.Workspace.Key, in.Workspace.Root, in.Input.HarnessKind, in.Input.HostSessionID, in.Input.StartKey, in.RequestHash); err != nil {
			return nil, fmt.Errorf("storage: insert session binding: %w", err)
		}
		result.SessionID = in.SessionID
		result.StartedAt = in.Now
		result.Status = "applied"
	} else {
		return nil, fmt.Errorf("storage: lookup continuity start: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("storage: commit continuity open: %w", err)
	}
	return result, nil
}

func checkpointConflict(revision int64) error {
	return &continuity.Error{Code: "conflict", Message: "checkpoint revision or request conflicts", CurrentRevision: &revision, Cause: continuity.ErrConflict}
}

func (s *continuityStore) Checkpoint(ctx context.Context, in continuity.Mutation) (*continuity.CheckpointResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("storage: begin checkpoint: %w", err)
	}
	defer tx.Rollback()
	var ended, updated sql.NullTime
	var revision sql.NullInt64
	var id, hash sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT s.ended_at,c.revision,c.last_request_id,c.last_request_hash,c.updated_at FROM sessions s JOIN session_continuity_bindings b ON b.session_id=s.id LEFT JOIN session_checkpoints c ON c.session_id=s.id WHERE s.id=? AND s.agent_id=? AND b.agent_id=? AND b.workspace_key=?`, in.Input.SessionID, in.Input.AgentID, in.Input.AgentID, in.Workspace.Key).Scan(&ended, &revision, &id, &hash, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, continuity.ErrUnavailable
	}
	if err != nil {
		return nil, fmt.Errorf("storage: lookup checkpoint: %w", err)
	}
	current := revision.Int64
	result := &continuity.CheckpointResult{ProtocolVersion: 1, SessionID: in.Input.SessionID}
	if id.Valid && id.String == in.Input.RequestID {
		if hash.String != in.RequestHash {
			return nil, checkpointConflict(current)
		}
		result.Status = "replayed"
		result.Revision = current
		result.UpdatedAt = updated.Time
	} else {
		if in.Input.ExpectedRevision == nil || *in.Input.ExpectedRevision != current || current == math.MaxInt64 {
			return nil, checkpointConflict(current)
		}
		if ended.Valid && in.Input.Action != "clear" {
			return nil, checkpointConflict(current)
		}
		var payload any
		visibility := "owner"
		if in.Input.Action == "save" {
			p := *in.Input.Payload
			if in.Sample != nil {
				p.Workspace = in.Sample(ctx)
			}
			data, err := json.Marshal(p)
			if err != nil || len(data) > continuity.MaxPayloadBytes {
				return nil, &continuity.Error{Code: "validation", Message: "complete checkpoint exceeds byte budget", Cause: continuity.ErrValidation}
			}
			payload = string(data)
			visibility = in.Input.Visibility
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO session_checkpoints(session_id,revision,payload,visibility,updated_at,last_request_id,last_request_hash) VALUES(?,?,?,?,?,?,?) ON CONFLICT(session_id) DO UPDATE SET revision=excluded.revision,payload=excluded.payload,visibility=excluded.visibility,updated_at=excluded.updated_at,last_request_id=excluded.last_request_id,last_request_hash=excluded.last_request_hash`, in.Input.SessionID, current+1, payload, visibility, in.Now, in.Input.RequestID, in.RequestHash)
		if err != nil {
			return nil, fmt.Errorf("storage: replace checkpoint: %w", err)
		}
		result.Status = "applied"
		result.Revision = current + 1
		result.UpdatedAt = in.Now
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("storage: commit checkpoint: %w", err)
	}
	return result, nil
}

func (s *continuityStore) Read(ctx context.Context, in continuity.ReadScope) (*continuity.Snapshot, error) {
	if in.Limit < 1 || in.Limit > 20 {
		return nil, fmt.Errorf("storage: continuity window requires 1..20 entries: %w", continuity.ErrValidation)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("storage: begin continuity read: %w", err)
	}
	defer tx.Rollback()
	out := &continuity.Snapshot{}
	err = tx.QueryRowContext(ctx, `SELECT b.workspace_root FROM sessions s JOIN session_continuity_bindings b ON b.session_id=s.id WHERE s.id=? AND s.agent_id=? AND b.agent_id=? AND b.workspace_key=?`, in.SessionID, in.AgentID, in.AgentID, in.WorkspaceKey).Scan(&out.Root)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, continuity.ErrUnavailable
	}
	if err != nil {
		return nil, fmt.Errorf("storage: read continuity binding: %w", err)
	}
	// Read no checkpoint or summary bodies in this cursor. Streaming the whole
	// visible set preserves conservative ambiguity without retaining history in
	// Go. Time values are compared in Go, rather than rounded SQL date functions
	// or textual ORDER BY that can reorder equivalent/nearby timestamps.
	query := `SELECT s.id,s.ended_at,c.revision,c.updated_at FROM sessions s JOIN session_continuity_bindings b ON b.session_id=s.id LEFT JOIN session_checkpoints c ON c.session_id=s.id WHERE b.workspace_key=? AND ((c.payload IS NOT NULL AND (s.agent_id=? OR c.visibility='workspace')) OR (c.session_id IS NULL AND s.agent_id=? AND s.ended_at IS NOT NULL AND s.summary IS NOT NULL AND s.summary!='')`
	args := []any{in.WorkspaceKey, in.AgentID, in.AgentID}
	if in.SourceSessionID != "" {
		query += ` OR (c.session_id IS NOT NULL AND c.payload IS NULL AND s.agent_id=?)`
		args = append(args, in.AgentID)
	}
	query += `)`
	if in.SourceSessionID != "" {
		query += ` AND s.id=?`
		args = append(args, in.SourceSessionID)
	} else {
		query += ` AND s.id!=?`
		args = append(args, in.SessionID)
		if in.Task != "" {
			query += ` AND c.payload IS NOT NULL AND json_extract(c.payload,'$.task')=?`
			args = append(args, in.Task)
		}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("storage: read continuity sources: %w", err)
	}
	type entry struct {
		id string
		at sql.NullTime
	}
	window := make([]entry, 0, in.Limit)
	newer := func(a, b entry) bool {
		if a.at.Time.Equal(b.at.Time) {
			return a.id < b.id
		}
		return a.at.Time.After(b.at.Time)
	}
	for rows.Next() {
		var candidate entry
		var ended, updated sql.NullTime
		var revision sql.NullInt64
		if err := rows.Scan(&candidate.id, &ended, &revision, &updated); err != nil {
			rows.Close()
			return nil, fmt.Errorf("storage: scan continuity metadata: %w", err)
		}
		candidate.at = ended
		if revision.Valid {
			candidate.at = updated
		}
		out.Matched++
		index := sort.Search(len(window), func(i int) bool { return newer(candidate, window[i]) })
		if index >= in.Limit {
			continue
		}
		if len(window) < in.Limit {
			window = append(window, entry{})
		}
		copy(window[index+1:], window[index:])
		window[index] = candidate
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return nil, fmt.Errorf("storage: read continuity metadata: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("storage: close continuity metadata: %w", closeErr)
	}
	// Drain before issuing further queries on the single connection. All body
	// lookups share this read snapshot with visibility, count and ordering.
	// Legacy summaries are limited in SQL to 4096 bytes plus enough trailing
	// bytes for boundedSummary to trim a partial UTF-8 rune safely.
	for _, candidate := range window {
		var source continuity.Source
		var ended, updated sql.NullTime
		var summary, payload, visibility sql.NullString
		var revision sql.NullInt64
		row := tx.QueryRowContext(ctx, `SELECT s.id,s.agent_id,b.harness_kind,s.ended_at,CASE WHEN c.session_id IS NULL THEN substr(CAST(s.summary AS BLOB),1,4099) ELSE NULL END,c.revision,c.payload,c.visibility,c.updated_at FROM sessions s JOIN session_continuity_bindings b ON b.session_id=s.id LEFT JOIN session_checkpoints c ON c.session_id=s.id WHERE s.id=?`, candidate.id)
		if err := row.Scan(&source.SessionID, &source.AgentID, &source.HarnessKind, &ended, &summary, &revision, &payload, &visibility, &updated); err != nil {
			return nil, fmt.Errorf("storage: read continuity window body: %w", err)
		}
		source.SessionClosed = ended.Valid
		source.Visibility = "owner"
		if revision.Valid {
			source.Revision = revision.Int64
			source.RecordedAt = updated.Time
			source.Visibility = visibility.String
			if payload.Valid {
				var body continuity.Payload
				if err := json.Unmarshal([]byte(payload.String), &body); err != nil {
					return nil, fmt.Errorf("storage: invalid stored checkpoint: %w", err)
				}
				source.SourceKind = "checkpoint"
				source.Payload = &body
				source.State = body.State
			} else {
				source.SourceKind = "cleared"
			}
		} else {
			source.SourceKind = "session_summary"
			source.RecordedAt = ended.Time
			source.Summary = boundedSummary(summary.String, 4096)
		}
		out.Sources = append(out.Sources, source)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("storage: finish continuity snapshot: %w", err)
	}
	return out, nil
}

func boundedSummary(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	s = s[:maxBytes]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// HasContinuity reports whether a logical export would omit local continuity state.
func (d *DB) HasContinuity(ctx context.Context) (bool, error) {
	var found int
	err := d.sql.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM session_continuity_bindings)`).Scan(&found)
	return found != 0, err
}

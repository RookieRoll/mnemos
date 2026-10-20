package storage

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"

	"github.com/polyxmedia/mnemos/internal/workspacediagnostics"
)

// WorkspaceDiagnosticsReader reads existing metadata without using Open's
// initialization, writer pragmas, migrations or access-accounting stores.
type WorkspaceDiagnosticsReader struct {
	path          string
	maxRetryPairs int
}

// NewWorkspaceDiagnosticsReader constructs a reader without touching the path.
func NewWorkspaceDiagnosticsReader(path string, maxRetryPairs ...int) *WorkspaceDiagnosticsReader {
	limit := 200
	if len(maxRetryPairs) > 0 && maxRetryPairs[0] > 0 {
		limit = maxRetryPairs[0]
	}
	return &WorkspaceDiagnosticsReader{path: path, maxRetryPairs: limit}
}

// Snapshot reads grouped metadata and startup collision counts atomically.
// A missing database is empty history, never permission to create its parent.
func (r *WorkspaceDiagnosticsReader) Snapshot(ctx context.Context, maxRoots int) (workspacediagnostics.Snapshot, error) {
	var out workspacediagnostics.Snapshot
	if maxRoots < 1 {
		return out, fmt.Errorf("storage: workspace diagnostics: invalid root limit")
	}
	info, err := os.Stat(r.path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("storage: workspace diagnostics: stat database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return out, fmt.Errorf("storage: workspace diagnostics: database is not a regular file")
	}
	out.Exists = true
	if err := diagnosticWALAvailable(r.path); err != nil {
		return out, err
	}
	abs, err := filepath.Abs(r.path)
	if err != nil {
		return out, fmt.Errorf("storage: workspace diagnostics: absolute database path: %w", err)
	}
	uriPath := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "temp_store(MEMORY)"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return out, fmt.Errorf("storage: workspace diagnostics: read-only open: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, fmt.Errorf("storage: workspace diagnostics: begin read snapshot: %w", err)
	}
	defer tx.Rollback()
	if err := diagnosticSchema(ctx, tx); err != nil {
		return out, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT workspace_root, workspace_key FROM session_continuity_bindings GROUP BY workspace_root, workspace_key)`).Scan(&out.TotalRoots); err != nil {
		return out, fmt.Errorf("storage: workspace diagnostics: count identities: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT b.workspace_root,b.workspace_key,COUNT(*),
		COALESCE(SUM(s.id IS NOT NULL AND s.ended_at IS NULL),0),COUNT(c.session_id),
		COALESCE(SUM(c.session_id IS NOT NULL AND c.payload IS NULL),0),
		COALESCE(SUM(c.payload IS NOT NULL AND c.visibility='owner'),0),
		COALESCE(SUM(c.payload IS NOT NULL AND c.visibility='workspace'),0),
		COALESCE(SUM(s.id IS NULL),0)
		FROM session_continuity_bindings b LEFT JOIN sessions s ON s.id=b.session_id
		LEFT JOIN session_checkpoints c ON c.session_id=b.session_id
		GROUP BY b.workspace_root,b.workspace_key ORDER BY b.workspace_root,b.workspace_key LIMIT ?`, maxRoots)
	if err != nil {
		return out, fmt.Errorf("storage: workspace diagnostics: aggregate metadata: %w", err)
	}
	for rows.Next() {
		var m workspacediagnostics.Metadata
		if err := rows.Scan(&m.Root, &m.Key, &m.Counts.Sessions, &m.Counts.OpenSessions, &m.Counts.Checkpoints, &m.Counts.ClearMarkers, &m.Counts.OwnerCheckpoints, &m.Counts.WorkspaceCheckpoints, &m.Counts.OrphanSessions); err != nil {
			_ = rows.Close()
			return out, fmt.Errorf("storage: workspace diagnostics: scan metadata: %w", err)
		}
		out.Roots = append(out.Roots, m)
	}
	rowErr := rows.Err()
	_ = rows.Close()
	if rowErr != nil {
		return out, fmt.Errorf("storage: workspace diagnostics: metadata rows: %w", rowErr)
	}
	// Compute collisions inside the snapshot. Raw start keys and owner/harness
	// tuples never cross the storage boundary or enter the report.
	rows, err = tx.QueryContext(ctx, `WITH selected AS (
		SELECT workspace_root,workspace_key FROM session_continuity_bindings
		GROUP BY workspace_root,workspace_key ORDER BY workspace_root,workspace_key LIMIT ?)
		SELECT a.workspace_root,a.workspace_key,b.workspace_root,b.workspace_key,COUNT(*)
		FROM session_continuity_bindings a JOIN selected x ON x.workspace_root=a.workspace_root AND x.workspace_key=a.workspace_key
		JOIN session_continuity_bindings b ON a.agent_id=b.agent_id AND a.harness_kind=b.harness_kind AND a.start_key=b.start_key
		JOIN selected y ON y.workspace_root=b.workspace_root AND y.workspace_key=b.workspace_key
		WHERE a.workspace_root<b.workspace_root OR (a.workspace_root=b.workspace_root AND a.workspace_key<b.workspace_key)
		GROUP BY a.workspace_root,a.workspace_key,b.workspace_root,b.workspace_key
		ORDER BY a.workspace_root,a.workspace_key,b.workspace_root,b.workspace_key LIMIT ?`, maxRoots, r.maxRetryPairs+1)
	if err != nil {
		return out, fmt.Errorf("storage: workspace diagnostics: aggregate retry conflicts: %w", err)
	}
	for rows.Next() {
		if len(out.RetryOverlap) >= r.maxRetryPairs {
			out.RetryOverlapTruncated = true
			break
		}
		var c workspacediagnostics.RetryConflict
		if err := rows.Scan(&c.Left.Root, &c.Left.Key, &c.Right.Root, &c.Right.Key, &c.Count); err != nil {
			_ = rows.Close()
			return out, fmt.Errorf("storage: workspace diagnostics: scan retry conflicts: %w", err)
		}
		out.RetryOverlap = append(out.RetryOverlap, c)
	}
	rowErr = rows.Err()
	_ = rows.Close()
	if rowErr != nil {
		return out, fmt.Errorf("storage: workspace diagnostics: retry conflict rows: %w", rowErr)
	}
	if err := tx.Commit(); err != nil {
		return out, fmt.Errorf("storage: workspace diagnostics: finish read snapshot: %w", err)
	}
	return out, nil
}

func diagnosticWALAvailable(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("storage: workspace diagnostics: read database header: %w", err)
	}
	var header [20]byte
	_, err = io.ReadFull(f, header[:])
	_ = f.Close()
	if err != nil {
		return fmt.Errorf("storage: workspace diagnostics: read database header: %w", err)
	}
	if header[18] != 2 && header[19] != 2 {
		return nil
	}
	// mode=ro alone may create missing WAL sidecars. Refuse that state instead
	// of changing files or using immutable=1, which would ignore committed WAL.
	for _, suffix := range []string{"-wal", "-shm"} {
		if info, err := os.Stat(path + suffix); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("storage: workspace diagnostics: WAL read-only limitation: existing regular %s sidecar required; no files were created", suffix)
		}
	}
	return nil
}

func diagnosticSchema(ctx context.Context, tx *sql.Tx) error {
	var ledgerExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='schema_migrations')`).Scan(&ledgerExists); err != nil {
		return fmt.Errorf("storage: workspace diagnostics: inspect migration ledger: %w", err)
	}
	if !ledgerExists {
		return &workspacediagnostics.UnsupportedSchemaError{Reason: "migration ledger unavailable"}
	}
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version); err != nil {
		return fmt.Errorf("storage: workspace diagnostics: read migration ledger: %w", err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		return fmt.Errorf("storage: workspace diagnostics: embedded schema versions: %w", err)
	}
	if version < 9 || len(migrations) == 0 || version > migrations[len(migrations)-1].version {
		return &workspacediagnostics.UnsupportedSchemaError{Reason: fmt.Sprintf("version %d does not support this diagnostic reader", version)}
	}
	for _, spec := range []struct {
		table   string
		columns []string
	}{
		{table: "sessions", columns: []string{"id", "ended_at"}},
		{table: "session_continuity_bindings", columns: []string{"session_id", "workspace_root", "workspace_key", "agent_id", "harness_kind", "start_key"}},
		{table: "session_checkpoints", columns: []string{"session_id", "payload", "visibility"}},
	} {
		table, columns := spec.table, spec.columns
		rows, err := tx.QueryContext(ctx, "PRAGMA table_info("+table+")")
		if err != nil {
			return fmt.Errorf("storage: workspace diagnostics: inspect schema: %w", err)
		}
		found := make(map[string]bool)
		for rows.Next() {
			var id, notnull, pk int
			var name, typ string
			var def any
			if err := rows.Scan(&id, &name, &typ, &notnull, &def, &pk); err != nil {
				_ = rows.Close()
				return fmt.Errorf("storage: workspace diagnostics: inspect column: %w", err)
			}
			found[name] = true
		}
		rowErr := rows.Err()
		_ = rows.Close()
		if rowErr != nil {
			return fmt.Errorf("storage: workspace diagnostics: schema rows: %w", rowErr)
		}
		for _, name := range columns {
			if !found[name] {
				return &workspacediagnostics.UnsupportedSchemaError{Reason: table + "." + name + " unavailable"}
			}
		}
	}
	return nil
}

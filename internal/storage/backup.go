package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrBackupTargetExists reports that a backup destination is already occupied.
// Backups never overwrite: an existing file is either an earlier snapshot the
// operator still wants or an unrelated file that must survive the command.
var ErrBackupTargetExists = errors.New("storage: backup target already exists")

// Backup writes a transactionally consistent snapshot of the live database to
// dest and reports the snapshot's size in bytes, so a caller can print it
// without a second stat.
//
// The snapshot comes from SQLite's VACUUM INTO, the one SQLite feature that
// copies a live write-ahead-log database without stopping writers and without
// copying -wal/-shm sidecars by hand: the copy is built from a single read
// transaction, so it opens cleanly and its row counts describe one point in
// time. It stays CGO-free because it is one SQL statement through the pure-Go
// modernc.org/sqlite driver; the C library's backup API is never involved.
//
// dest must not exist. An existing target yields ErrBackupTargetExists wrapped
// with the path, so a caller can exit non-zero without retrying and without
// touching the file. Missing parent directories are created, matching Open.
func (d *DB) Backup(ctx context.Context, dest string) (int64, error) {
	if strings.TrimSpace(dest) == "" {
		return 0, fmt.Errorf("storage: backup target path required")
	}
	if _, err := os.Stat(dest); err == nil {
		return 0, fmt.Errorf("%w: %s", ErrBackupTargetExists, dest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("storage: backup target: %w", err)
	}
	if dir := filepath.Dir(dest); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 0, fmt.Errorf("storage: backup create dir %q: %w", dir, err)
		}
	}
	if err := d.vacuumInto(ctx, dest); err != nil {
		return 0, err
	}
	info, err := os.Stat(dest)
	if err != nil {
		return 0, fmt.Errorf("storage: backup stat snapshot: %w", err)
	}
	return info.Size(), nil
}

// vacuumInto runs VACUUM INTO against the live database.
//
// The destination travels as a bound parameter first, because a parameter
// cannot be mis-quoted and SQLite accepts any scalar expression after INTO.
// Only if the pinned driver rejects a parameter in that position does it fall
// back to a single-quoted literal with doubled internal quotes — never bare
// string concatenation.
func (d *DB) vacuumInto(ctx context.Context, dest string) error {
	_, boundErr := d.sql.ExecContext(ctx, `VACUUM INTO ?`, dest)
	if boundErr == nil {
		return nil
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		// The bound attempt created output before failing. Retrying would only
		// replace the real cause with VACUUM INTO's "file already exists".
		return fmt.Errorf("storage: backup vacuum into: %w", boundErr)
	}
	if _, literalErr := d.sql.ExecContext(ctx, `VACUUM INTO `+quoteSQLLiteral(dest)); literalErr != nil {
		return fmt.Errorf("storage: backup vacuum into: %w", literalErr)
	}
	return nil
}

// quoteSQLLiteral renders s as a single-quoted SQL string literal, doubling any
// embedded quote. It exists only for the VACUUM INTO fallback path.
func quoteSQLLiteral(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `''`) + `'`
}
